// internal/pusher/pusher.go
package pusher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sumi/internal/config"
	"sumi/internal/model"
)

// stableDeviceID returns a deterministic 64-hex-char identifier for this host.
func stableDeviceID(hostname string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(hostname))))
	return fmt.Sprintf("%x", sum)
}

// Start launches a background goroutine that pushes snapshots to the cloud.
// getSnapshot is called once per push interval to obtain the latest snapshot.
// The goroutine stops when ctx is cancelled. Returns immediately.
func Start(ctx context.Context, cfg config.Config, clientVersion string, getSnapshot func() model.Snapshot) {
	interval := time.Duration(cfg.PushInterval) * time.Second
	if interval <= 0 {
		interval = time.Duration(cfg.Interval) * time.Second
	}
	if interval <= 0 {
		interval = 60 * time.Second
	}

	client := &http.Client{Timeout: 15 * time.Second}
	revoked := false

	go func() {
		// Send one snapshot immediately so the device appears in the dashboard right away.
		push(ctx, client, cfg, clientVersion, getSnapshot, &revoked)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if revoked {
					return
				}
				push(ctx, client, cfg, clientVersion, getSnapshot, &revoked)
			}
		}
	}()
}

// push performs one push attempt with exponential backoff on transient errors.
func push(ctx context.Context, client *http.Client, cfg config.Config, clientVersion string, getSnapshot func() model.Snapshot, revoked *bool) {
	snap := getSnapshot()
	snap.DeviceID = stableDeviceID(snap.Hostname)
	snap.ClientVersion = clientVersion

	body, err := json.Marshal(snap)
	if err != nil {
		slog.Warn("push: marshal failed", "err", err)
		return
	}

	backoff := 5 * time.Second
	const maxBackoff = 5 * time.Minute
	const maxAttempts = 5

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.PushURL, bytes.NewReader(body))
		if err != nil {
			slog.Warn("push: build request failed", "err", err)
			return
		}
		req.Header.Set("Authorization", "Bearer "+cfg.PushToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("push: request failed", "attempt", attempt, "err", err)
			sleep(ctx, backoff)
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}
		resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusAccepted: // 202
			return
		case http.StatusUnauthorized: // 401
			slog.Error("push: token rejected (401) — renew at https://app.getsumi.dev/settings/tokens")
			*revoked = true
			return
		case http.StatusTooManyRequests: // 429
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil {
					sleep(ctx, time.Duration(secs)*time.Second)
				}
			}
			return // skip this cycle; do not retry
		default:
			slog.Warn("push: unexpected status", "status", resp.StatusCode, "attempt", attempt)
			sleep(ctx, backoff)
			if backoff < maxBackoff {
				backoff *= 2
			}
		}
	}
	slog.Warn("push: giving up after max attempts", "attempts", maxAttempts)
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// StartEventPusher launches a background goroutine that polls for OS-level events
// and pushes them to <push_url_base>/events.
// getEvents is called once per interval and receives the time of the last successful poll.
// Returns immediately; stops when ctx is cancelled.
func StartEventPusher(ctx context.Context, cfg config.Config, clientVersion string,
	getEvents func(since time.Time) []model.SystemEvent) {

	interval := time.Duration(cfg.PushInterval) * time.Second
	if interval <= 0 {
		interval = time.Duration(cfg.Interval) * time.Second
	}
	if interval <= 0 {
		interval = 60 * time.Second
	}

	// Derive events endpoint: replace trailing /push (or any path) with /events.
	eventsURL := cfg.PushURL
	if idx := strings.LastIndex(eventsURL, "/push"); idx >= 0 {
		eventsURL = eventsURL[:idx] + "/events"
	} else if !strings.HasSuffix(eventsURL, "/events") {
		eventsURL += "/events"
	}

	client := &http.Client{Timeout: 15 * time.Second}

	go func() {
		last := time.Now().Add(-interval)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				events := getEvents(last)
				last = t
				if len(events) == 0 {
					continue
				}
				pushEvents(ctx, client, eventsURL, cfg.PushToken, clientVersion, events)
			}
		}
	}()
}

// pushEvents POSTs a slice of SystemEvents to the events endpoint.
func pushEvents(ctx context.Context, client *http.Client, url, token, clientVersion string, events []model.SystemEvent) {
	body, err := json.Marshal(events)
	if err != nil {
		slog.Warn("pushEvents: marshal failed", "err", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		slog.Warn("pushEvents: build request failed", "err", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sumi-Version", clientVersion)

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("pushEvents: request failed", "err", err)
		}
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		slog.Warn("pushEvents: unexpected status", "status", resp.StatusCode)
	}
}
