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
