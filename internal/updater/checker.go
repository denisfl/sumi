// internal/updater/checker.go
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultAPIURL = "https://api.github.com/repos/denisfl/sumi/releases/latest"
	defaultTTL    = 24 * time.Hour
	checkTimeout  = 3 * time.Second
)

// cacheEntry is the on-disk JSON representation of an update check result.
type cacheEntry struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// UpdateChecker performs background update checks against the GitHub releases API
// and caches results on disk so that network calls happen at most once per TTL.
type UpdateChecker struct {
	CurrentVersion string
	CachePath      string
	TTL            time.Duration
	APIURL         string

	mu     sync.Mutex
	cached string // latest version string when an update is available; "" otherwise
}

// NewUpdateChecker returns an UpdateChecker configured for the default GitHub repository.
// The cache file defaults to ~/.config/sumi/update_check.json.
func NewUpdateChecker(currentVersion string) (*UpdateChecker, error) {
	dir, err := defaultConfigDir()
	if err != nil {
		return nil, err
	}
	return &UpdateChecker{
		CurrentVersion: currentVersion,
		CachePath:      filepath.Join(dir, "update_check.json"),
		TTL:            defaultTTL,
		APIURL:         defaultAPIURL,
	}, nil
}

func defaultConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("updater: cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "sumi"), nil
}

// readCache returns the cached latest version and true when the cache is present and
// not expired. Returns ("", false) on any error or when the cache is stale.
func (c *UpdateChecker) readCache() (string, bool) {
	data, err := os.ReadFile(c.CachePath)
	if err != nil {
		return "", false
	}
	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return "", false
	}
	if time.Since(entry.CheckedAt) > c.TTL {
		return "", false
	}
	return entry.Latest, true
}

// writeCache persists the cache entry to disk, creating the directory if needed.
// Errors are silently discarded — a failed write only means the next run will
// re-fetch from GitHub.
func (c *UpdateChecker) writeCache(latest string) {
	entry := cacheEntry{
		CheckedAt: time.Now().UTC(),
		Latest:    latest,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	dir := filepath.Dir(c.CachePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(c.CachePath, data, 0o600)
}

// fetchLatest performs an HTTP request to the GitHub releases API and returns the
// latest tag name.
func (c *UpdateChecker) fetchLatest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sumi/"+c.CurrentVersion)

	client := &http.Client{Timeout: checkTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github API returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.TagName == "" {
		return "", fmt.Errorf("empty tag_name in GitHub API response")
	}
	return payload.TagName, nil
}

// hasUpdate returns true and the latest version when latest is strictly newer
// than CurrentVersion. Non-semver current versions (e.g. "dev") are treated as
// outdated so that development builds always see what is available.
func (c *UpdateChecker) hasUpdate(latest string) (bool, string) {
	cur, err := parseSemver(c.CurrentVersion)
	if err != nil {
		// "dev" or other non-release builds: report whatever is latest.
		return true, latest
	}
	lat, err := parseSemver(latest)
	if err != nil {
		return false, ""
	}
	return lat.isNewerThan(cur), latest
}

// ReadCacheSync reads the on-disk cache synchronously without any network call.
// Returns the latest version string when a cached update is available and not
// expired; returns "" otherwise. Safe to call before the first CheckAsync.
func (c *UpdateChecker) ReadCacheSync() string {
	latest, ok := c.readCache()
	if !ok {
		return ""
	}
	ok, ver := c.hasUpdate(latest)
	if !ok {
		return ""
	}
	return ver
}

// CachedResult returns the latest version string stored in memory by the most
// recent CheckAsync call. Returns "" when no update is pending.
func (c *UpdateChecker) CachedResult() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cached
}

// CheckAsync starts a background goroutine that:
//  1. Reads the on-disk cache; if still fresh, uses it.
//  2. If expired, fetches from the GitHub API (3 s timeout) and writes new cache.
//
// On completion the in-memory result is stored so CachedResult() reflects it.
// The caller is never blocked. Network or disk failures are silently discarded.
func (c *UpdateChecker) CheckAsync(ctx context.Context) {
	go func() {
		latest, ok := c.readCache()
		if !ok {
			checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
			defer cancel()
			var err error
			latest, err = c.fetchLatest(checkCtx)
			if err != nil {
				return
			}
			c.writeCache(latest)
		}

		update, ver := c.hasUpdate(latest)
		c.mu.Lock()
		if update {
			c.cached = ver
		} else {
			c.cached = ""
		}
		c.mu.Unlock()
	}()
}

// Check performs a synchronous update check, always contacting the GitHub API.
// It writes the result to the disk cache and returns (latestVersion, hasUpdate, err).
// Intended for use by the "sumi update --check" subcommand.
func (c *UpdateChecker) Check(ctx context.Context) (latestVersion string, hasUpdate bool, err error) {
	checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	latest, err := c.fetchLatest(checkCtx)
	if err != nil {
		return "", false, err
	}
	c.writeCache(latest)
	ok, _ := c.hasUpdate(latest)
	return latest, ok, nil
}
