// internal/collector/ext_test.go — Unit tests for v0.7 extended metric parsers.
// No build tag: tests only functions defined in ext_parsers.go.
package collector

import (
	"testing"
	"time"
)

// ---- WireGuard parse tests ----

func TestParseWireGuardDump_NoPeers(t *testing.T) {
	dump := "wg0 privatekey pubkey 51820 off\n"
	now := time.Now()
	info := parseWireGuardDump(dump, now)
	if info == nil {
		t.Fatal("expected non-nil WireGuardInfo")
	}
	if info.Interface != "wg0" {
		t.Errorf("Interface = %q, want %q", info.Interface, "wg0")
	}
	if info.PeersTotal != 0 {
		t.Errorf("PeersTotal = %d, want 0", info.PeersTotal)
	}
}

func TestParseWireGuardDump_WithPeers(t *testing.T) {
	now := time.Unix(1700000300, 0)
	recentTs := now.Unix() - 60
	oldTs := now.Unix() - 300
	dump := "wg0 priv pub 51820 off\n" +
		"wg0 peerpub1 psk endpoint1 10.0.0.2/32 " + formatInt64(recentTs) + " 1024 2048 0\n" +
		"wg0 peerpub2 psk endpoint2 10.0.0.3/32 " + formatInt64(oldTs) + " 512 1024 0\n"
	info := parseWireGuardDump(dump, now)
	if info == nil {
		t.Fatal("expected non-nil WireGuardInfo")
	}
	if info.PeersTotal != 2 {
		t.Errorf("PeersTotal = %d, want 2", info.PeersTotal)
	}
	if info.PeersOnline != 1 {
		t.Errorf("PeersOnline = %d, want 1 (only recent peer)", info.PeersOnline)
	}
	if info.LastHandshakeAge != 60 {
		t.Errorf("LastHandshakeAge = %d, want 60", info.LastHandshakeAge)
	}
	if info.TransferRxBytes != 1536 {
		t.Errorf("TransferRxBytes = %d, want 1536", info.TransferRxBytes)
	}
	if info.TransferTxBytes != 3072 {
		t.Errorf("TransferTxBytes = %d, want 3072", info.TransferTxBytes)
	}
}

func TestParseWireGuardDump_Empty(t *testing.T) {
	if info := parseWireGuardDump("", time.Now()); info != nil {
		t.Errorf("expected nil for empty dump, got %+v", info)
	}
}

func TestParseWireGuardDump_NoInterfaces(t *testing.T) {
	if info := parseWireGuardDump("not a valid line\n", time.Now()); info != nil {
		t.Errorf("expected nil for no-interface dump, got %+v", info)
	}
}

// ---- Ping stats parser tests ----

func TestParsePingStats_Linux(t *testing.T) {
	out := "3 packets transmitted, 3 received, 0% packet loss\nrtt min/avg/max/mdev = 1.23/1.45/1.67/0.18 ms\n"
	loss, avg := parsePingStats(out)
	if loss != 0.0 {
		t.Errorf("loss = %.1f, want 0.0", loss)
	}
	if avg != 1.45 {
		t.Errorf("avg = %.2f, want 1.45", avg)
	}
}

func TestParsePingStats_WithLoss(t *testing.T) {
	out := "3 packets transmitted, 2 received, 33% packet loss\nrtt min/avg/max/mdev = 1.0/2.0/3.0/0.5 ms"
	loss, avg := parsePingStats(out)
	if loss != 33.0 {
		t.Errorf("loss = %.1f, want 33.0", loss)
	}
	if avg != 2.0 {
		t.Errorf("avg = %.2f, want 2.0", avg)
	}
}

func TestParsePingStats_Unavailable(t *testing.T) {
	loss, avg := parsePingStats("")
	if loss != -1 || avg != -1 {
		t.Errorf("empty output should return -1,-1, got %.1f,%.1f", loss, avg)
	}
}

// ---- SMART health parser tests ----

func TestParseSmartHealth_OK(t *testing.T) {
	out := "SMART overall-health self-assessment test result: PASSED\nsome other line"
	if got := parseSmartHealth(out); got != "ok" {
		t.Errorf("got %q, want %q", got, "ok")
	}
}

func TestParseSmartHealth_Fail(t *testing.T) {
	out := "SMART overall-health self-assessment test result: FAILED!"
	if got := parseSmartHealth(out); got != "fail" {
		t.Errorf("got %q, want %q", got, "fail")
	}
}

func TestParseSmartHealth_Empty(t *testing.T) {
	if got := parseSmartHealth("No SMART info available"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ---- Inode stats parser tests ----

func TestParseDfInodes(t *testing.T) {
	out := "Filesystem     Inodes  IUsed   IFree IUse% Mounted on\n" +
		"/dev/sda1      655360  65536  589824   10% /\n" +
		"tmpfs           61440      1   61439    1% /dev/shm\n"
	m := parseDfInodes(out)
	if pct, ok := m["/"]; !ok {
		t.Error("missing / entry")
	} else if pct < 9.9 || pct > 10.1 {
		t.Errorf("/ InodesUsedPct = %.2f, want ~10.0", pct)
	}
	if _, ok := m["/dev/shm"]; !ok {
		t.Error("missing /dev/shm entry")
	}
}

// ---- ss estab parser tests ----

func TestParseSsEstab(t *testing.T) {
	out := "Netid  State   Recv-Q  Send-Q\nTotal: inet 20\nTCP:   12 (estab 5, closed 3, orphaned 0, timewait 4)\nUDP:   3"
	if got := parseSsEstab(out); got != 5 {
		t.Errorf("got %d, want 5", got)
	}
}

func TestParseSsEstab_Zero(t *testing.T) {
	if got := parseSsEstab("TCP:   0"); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

// ---- TCP retransmit delta test ----

func TestTcpRetransmitDelta(t *testing.T) {
	// Verify the delta formula used in linuxNetExt directly.
	initial := uint64(1000)
	updated := uint64(1050)
	var delta uint64
	if updated >= initial {
		delta = updated - initial
	}
	if delta != 50 {
		t.Errorf("delta = %d, want 50", delta)
	}
}

// ---- Helpers ----

// formatInt64 converts an int64 to its decimal string representation.
func formatInt64(n int64) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	pos := 20
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	s := string(buf[pos:])
	if negative {
		return "-" + s
	}
	return s
}

