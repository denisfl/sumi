// internal/pusher/pusher_test.go
package pusher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sumi/internal/config"
	"sumi/internal/model"
)

func newTestConfig(url string) config.Config {
	cfg := config.Default()
	cfg.PushEnabled = true
	cfg.PushURL = url
	cfg.PushToken = "test_token_abc"
	cfg.PushInterval = 1
	return cfg
}

func newTestSnapshot() model.Snapshot {
	return model.Snapshot{
		Hostname:  "test-host",
		Platform:  "linux",
		Timestamp: time.Now(),
		CPU:       model.CPU{Usage: 10.0, Cores: 4},
		Mem:       model.Mem{UsedBytes: 1 << 30, TotalBytes: 4 << 30},
	}
}

func TestAuthorizationHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := newTestConfig(srv.URL)
	snap := newTestSnapshot()
	client := &http.Client{Timeout: 5 * time.Second}
	revoked := false
	push(context.Background(), client, cfg, "v0.0.1", func() model.Snapshot { return snap }, &revoked)

	if gotAuth != "Bearer test_token_abc" {
		t.Errorf("expected 'Bearer test_token_abc', got %q", gotAuth)
	}
}

func TestClientVersionAndDeviceID(t *testing.T) {
	var received model.Snapshot
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := newTestConfig(srv.URL)
	snap := newTestSnapshot()
	client := &http.Client{Timeout: 5 * time.Second}
	revoked := false
	push(context.Background(), client, cfg, "v1.2.3", func() model.Snapshot { return snap }, &revoked)

	if received.ClientVersion != "v1.2.3" {
		t.Errorf("expected ClientVersion 'v1.2.3', got %q", received.ClientVersion)
	}
	if received.DeviceID == "" {
		t.Error("expected non-empty DeviceID")
	}
}

func TestDeviceIDIsHex64(t *testing.T) {
	id := stableDeviceID("test-host")
	if len(id) != 64 {
		t.Errorf("expected 64-char hex DeviceID, got len %d: %q", len(id), id)
	}
	for _, c := range id {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("DeviceID contains non-hex char %q", c)
		}
	}
}

func TestRevokedOn401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := newTestConfig(srv.URL)
	snap := newTestSnapshot()
	client := &http.Client{Timeout: 5 * time.Second}
	revoked := false
	push(context.Background(), client, cfg, "dev", func() model.Snapshot { return snap }, &revoked)

	if !revoked {
		t.Error("expected revoked = true after 401 response")
	}
}

func TestNoRetryOn429(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	cfg := newTestConfig(srv.URL)
	snap := newTestSnapshot()
	client := &http.Client{Timeout: 5 * time.Second}
	revoked := false
	push(context.Background(), client, cfg, "dev", func() model.Snapshot { return snap }, &revoked)

	if calls != 1 {
		t.Errorf("expected exactly 1 request on 429, got %d", calls)
	}
}
