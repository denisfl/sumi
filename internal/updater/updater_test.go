// internal/updater/updater_test.go
package updater

import (
	"testing"
)

func makeRelease(assets []releaseAsset) *releaseInfo {
	return &releaseInfo{TagName: "v0.9.0", Assets: assets}
}

func TestSelectAsset_DashName(t *testing.T) {
	rel := makeRelease([]releaseAsset{
		{Name: "sumi-darwin-arm64.tar.gz", BrowserDownloadURL: "https://example.com/dash.tar.gz"},
		{Name: "sumi-darwin-arm64.tar.gz.sha256", BrowserDownloadURL: "https://example.com/dash.sha256"},
	})
	url, sha, err := selectAsset(rel, "darwin", "arm64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://example.com/dash.tar.gz" {
		t.Errorf("url: got %q", url)
	}
	if sha != "https://example.com/dash.sha256" {
		t.Errorf("sha256: got %q", sha)
	}
}

func TestSelectAsset_UnderscoreFallback(t *testing.T) {
	// Simulates a release that only has legacy underscore-named assets
	// (present on pre-v0.9.0 releases). A v0.8.x binary built with the
	// new code should still find them.
	rel := makeRelease([]releaseAsset{
		{Name: "sumi_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.com/under.tar.gz"},
		{Name: "sumi_darwin_arm64.tar.gz.sha256", BrowserDownloadURL: "https://example.com/under.sha256"},
	})
	url, sha, err := selectAsset(rel, "darwin", "arm64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://example.com/under.tar.gz" {
		t.Errorf("url: got %q", url)
	}
	if sha != "https://example.com/under.sha256" {
		t.Errorf("sha256: got %q", sha)
	}
}

func TestSelectAsset_DashTakesPriorityOverUnderscore(t *testing.T) {
	// When both naming conventions exist, dash wins.
	rel := makeRelease([]releaseAsset{
		{Name: "sumi_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.com/under.tar.gz"},
		{Name: "sumi-darwin-arm64.tar.gz", BrowserDownloadURL: "https://example.com/dash.tar.gz"},
	})
	url, _, err := selectAsset(rel, "darwin", "arm64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://example.com/dash.tar.gz" {
		t.Errorf("expected dash URL to win; got %q", url)
	}
}

func TestSelectAsset_NotFound(t *testing.T) {
	rel := makeRelease([]releaseAsset{
		{Name: "sumi-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux.tar.gz"},
	})
	_, _, err := selectAsset(rel, "darwin", "arm64")
	if err == nil {
		t.Fatal("expected error for missing asset, got nil")
	}
}

func TestSelectAsset_NoSHA256(t *testing.T) {
	// SHA256 is optional — no error expected when absent.
	rel := makeRelease([]releaseAsset{
		{Name: "sumi-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux.tar.gz"},
	})
	url, sha, err := selectAsset(rel, "linux", "amd64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url == "" {
		t.Error("expected non-empty asset URL")
	}
	if sha != "" {
		t.Errorf("expected empty sha256 URL when absent; got %q", sha)
	}
}
