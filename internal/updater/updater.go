// internal/updater/updater.go
package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	downloadTimeout = 5 * time.Minute
	maxBinarySize   = 64 * 1024 * 1024 // 64 MiB upper bound for a sumi binary
)

// RunConfig holds options for the update subcommand.
type RunConfig struct {
	CurrentVersion string
	CheckOnly bool
	TargetVersion string
	APIURL string
}

// Run executes the update subcommand according to cfg.
func Run(ctx context.Context, cfg RunConfig) error {
	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}

	dir, err := defaultConfigDir()
	if err != nil {
		return err
	}
	checker := &UpdateChecker{
		CurrentVersion: cfg.CurrentVersion,
		CachePath:      filepath.Join(dir, "update_check.json"),
		TTL:            defaultTTL,
		APIURL:         cfg.APIURL,
	}

	if cfg.CheckOnly {
		latest, hasUpdate, err := checker.Check(ctx)
		if err != nil {
			return fmt.Errorf("check failed: %w", err)
		}
		if hasUpdate {
			fmt.Printf("update available: %s  ->  %s\n", cfg.CurrentVersion, latest)
			fmt.Println("run: sumi update")
		} else {
			fmt.Printf("sumi is up to date (%s)\n", cfg.CurrentVersion)
		}
		return nil
	}

	// Resolve target version.
	targetVersion := cfg.TargetVersion
	if targetVersion == "" {
		fmt.Println("checking for updates...")
		latest, _, err := checker.Check(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch latest version: %w", err)
		}
		targetVersion = latest
	}

	if targetVersion == cfg.CurrentVersion {
		fmt.Printf("sumi is already at %s\n", cfg.CurrentVersion)
		return nil
	}

	// Detect Homebrew installation before doing anything else.
	execPath, err := resolveExecutable()
	if err != nil {
		return err
	}
	if strings.Contains(execPath, "/homebrew/") || strings.Contains(execPath, "/Homebrew/") {
		return fmt.Errorf("sumi was installed via Homebrew — run: brew upgrade sumi")
	}

	fmt.Printf("updating sumi %s -> %s\n", cfg.CurrentVersion, targetVersion)

	// Fetch release metadata.
	release, err := fetchRelease(ctx, cfg.APIURL, targetVersion)
	if err != nil {
		return fmt.Errorf("failed to fetch release %s: %w", targetVersion, err)
	}

	// Find asset URL for the current platform.
	assetURL, sha256URL, err := selectAsset(release, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	// Download archive to a temporary file.
	fmt.Printf("downloading %s...\n", assetURL)
	tmpArchive, err := downloadToTemp(ctx, assetURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(tmpArchive)

	// Verify SHA256 when a checksum file is available.
	if sha256URL != "" {
		fmt.Println("verifying checksum...")
		if err := verifyChecksum(ctx, tmpArchive, sha256URL); err != nil {
			return fmt.Errorf("checksum verification failed: %w", err)
		}
	}

	// Extract the "sumi" binary from the archive.
	fmt.Println("extracting binary...")
	newBinary, err := extractBinary(tmpArchive)
	if err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}
	defer os.Remove(newBinary)

	// Atomically replace the current binary and validate the new one.
	fmt.Printf("installing to %s...\n", execPath)
	if err := atomicReplace(execPath, newBinary, targetVersion); err != nil {
		return fmt.Errorf("installation failed: %w", err)
	}

	fmt.Printf("sumi %s installed successfully\n", targetVersion)
	if hint := detectRestartHint(); hint != "" {
		fmt.Printf("note: restart your daemon to apply the update:\n  %s\n", hint)
	}
	return nil
}

// resolveExecutable returns the real path of the running binary, following symlinks.
func resolveExecutable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot determine executable path: %w", err)
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("cannot resolve symlinks: %w", err)
	}
	return p, nil
}

// releaseAsset is a single file attached to a GitHub release.
type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// releaseInfo holds the fields of a GitHub releases API response that we use.
type releaseInfo struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

// fetchRelease returns release metadata for the given version tag (or the latest
// release when version is empty). version must be a vX.Y.Z string.
func fetchRelease(ctx context.Context, baseURL, version string) (*releaseInfo, error) {
	apiURL := baseURL
	if version != "" {
		// Validate version format before embedding it in a URL.
		if _, err := parseSemver(version); err != nil {
			return nil, fmt.Errorf("invalid version %q: %w", version, err)
		}
		// Replace /releases/latest with /releases/tags/<version>.
		tag := url.PathEscape(version)
		apiURL = strings.Replace(baseURL, "/releases/latest", "/releases/tags/"+tag, 1)
		if apiURL == baseURL {
			// baseURL does not end with /releases/latest — construct manually.
			repoBase := strings.TrimSuffix(baseURL, "/releases/latest")
			apiURL = repoBase + "/releases/tags/" + tag
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sumi-updater")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned HTTP %d for %s", resp.StatusCode, apiURL)
	}

	var info releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

// selectAsset returns the download URL and optional checksum URL for goos/goarch.
// Asset naming matches the release workflow: sumi-<os>-<arch>.tar.gz
// (e.g. sumi-linux-amd64.tar.gz, sumi-darwin-arm64.tar.gz).
// Legacy releases (pre-v0.9.0) used underscores (sumi_os_arch.tar.gz); both
// variants are tried so that binaries built before the naming change can still
// locate assets in newer releases.
func selectAsset(release *releaseInfo, goos, goarch string) (assetURL, sha256URL string, err error) {
	// Primary: canonical dash-separated name (current convention).
	dashName := fmt.Sprintf("sumi-%s-%s.tar.gz", goos, goarch)
	// Fallback: legacy underscore-separated name (pre-v0.9.0).
	underName := fmt.Sprintf("sumi_%s_%s.tar.gz", goos, goarch)

	for _, a := range release.Assets {
		switch a.Name {
		case dashName:
			assetURL = a.BrowserDownloadURL
			sha256URL = "" // reset; prefer dash-based SHA too
		case dashName + ".sha256":
			sha256URL = a.BrowserDownloadURL
		case underName:
			if assetURL == "" { // only use legacy name when dash variant absent
				assetURL = a.BrowserDownloadURL
			}
		case underName + ".sha256":
			if sha256URL == "" {
				sha256URL = a.BrowserDownloadURL
			}
		}
	}

	if assetURL == "" {
		return "", "", fmt.Errorf(
			"no release asset found for %s/%s (expected %s)", goos, goarch, dashName,
		)
	}
	return assetURL, sha256URL, nil
}

// downloadToTemp downloads url into a temporary file and returns its path.
// The caller is responsible for removing the file when done.
func downloadToTemp(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "sumi-updater")

	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	f, err := os.CreateTemp("", "sumi-update-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// verifyChecksum fetches the SHA-256 checksum file from sha256URL and validates
// that tmpFile matches it.
func verifyChecksum(ctx context.Context, tmpFile, sha256URL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sha256URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "sumi-updater")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return err
	}

	// Checksum file format: "<hex>  <filename>" or just "<hex>".
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return fmt.Errorf("empty checksum file")
	}
	expectedHex := strings.ToLower(fields[0])

	f, err := os.Open(tmpFile)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actualHex := hex.EncodeToString(h.Sum(nil))

	if actualHex != expectedHex {
		return fmt.Errorf("checksum mismatch: got %s, want %s", actualHex, expectedHex)
	}
	return nil
}

// extractBinary extracts the "sumi" binary from a tar.gz archive into a temporary
// file and returns that file's path. The caller is responsible for removing it.
func extractBinary(tarGzPath string) (string, error) {
	f, err := os.Open(tarGzPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(hdr.Name) != "sumi" {
			continue
		}

		tmp, err := os.CreateTemp("", "sumi-new-*")
		if err != nil {
			return "", err
		}
		// LimitReader guards against decompression bombs.
		if _, err := io.Copy(tmp, io.LimitReader(tr, maxBinarySize)); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return "", err
		}
		tmp.Close()
		if err := os.Chmod(tmp.Name(), 0o755); err != nil {
			os.Remove(tmp.Name())
			return "", err
		}
		return tmp.Name(), nil
	}
	return "", fmt.Errorf("sumi binary not found in archive")
}

// atomicReplace backs up execPath, installs newBinary, validates the new binary
// runs and reports expectedVersion, then removes the backup.
// If validation fails the backup is restored so the system is never left broken.
func atomicReplace(execPath, newBinary, expectedVersion string) error {
	backupPath := execPath + ".bak"

	if err := copyFile(execPath, backupPath); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	if err := os.Rename(newBinary, execPath); err != nil {
		_ = os.Rename(backupPath, execPath)
		return fmt.Errorf("rename failed (permissions?): %w — try: sudo sumi update", err)
	}

	// Validate the installed binary before removing the backup.
	out, err := exec.Command(execPath, "--version").Output() // #nosec G204 -- executes our own binary
	if err != nil {
		_ = os.Rename(backupPath, execPath)
		return fmt.Errorf("new binary failed to run: %w", err)
	}
	if !strings.Contains(strings.TrimSpace(string(out)), expectedVersion) {
		_ = os.Rename(backupPath, execPath)
		return fmt.Errorf(
			"version mismatch after install: got %q want %s — rolled back",
			strings.TrimSpace(string(out)), expectedVersion,
		)
	}

	_ = os.Remove(backupPath)
	return nil
}

// copyFile copies src to dst preserving mode bits.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// detectRestartHint returns a shell command the user should run to restart their
// sumi daemon after an update, or "" when no managed service is detected.
func detectRestartHint() string {
	switch runtime.GOOS {
	case "linux":
		return detectSystemdHint()
	case "darwin":
		return detectLaunchdHint()
	}
	return ""
}

// detectSystemdHint checks whether a sumi systemd unit is active (system-level
// first, then user-level) and returns the appropriate restart command.
func detectSystemdHint() string {
	out, err := exec.Command("systemctl", "is-active", "sumi").Output() // #nosec G204
	if err == nil && strings.TrimSpace(string(out)) == "active" {
		return "sudo systemctl restart sumi"
	}
	out, err = exec.Command("systemctl", "--user", "is-active", "sumi").Output() // #nosec G204
	if err == nil && strings.TrimSpace(string(out)) == "active" {
		return "systemctl --user restart sumi"
	}
	return ""
}

// detectLaunchdHint scans common launchd plist directories for a service whose
// filename contains "sumi" and returns the matching kickstart command.
func detectLaunchdHint() string {
	dirs := []string{
		"/Library/LaunchDaemons",
		"/Library/LaunchAgents",
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Library", "LaunchAgents"))
	}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".plist") {
				continue
			}
			if !strings.Contains(strings.ToLower(name), "sumi") {
				continue
			}
			label := strings.TrimSuffix(name, ".plist")
			if dir == "/Library/LaunchDaemons" {
				return "sudo launchctl kickstart -k system/" + label
			}
			// User-level agent: requires the GUI UID domain.
			return "launchctl kickstart -k gui/$(id -u)/" + label
		}
	}
	return ""
}
