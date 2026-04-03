// internal/collector/wireguard.go
package collector

import (
	"context"
	"strconv"
	"strings"
	"time"

	"sumi/internal/model"
)

// collectWireGuard runs `wg show all dump` and returns aggregate WireGuard info.
// Returns nil when wg is not installed, exits non-zero, or no interfaces exist.
func collectWireGuard(ctx context.Context) *model.WireGuardInfo {
	out, err := runCmd(ctx, "wg", "show", "all", "dump")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	return parseWireGuardDump(out, time.Now())
}

// parseWireGuardDump parses the output of `wg show all dump`.
// Each line is either an interface line (5 fields) or a peer line (9 fields):
//
//	interface: <iface> <private-key> <public-key> <listen-port> <fwmark>
//	peer:      <iface> <public-key> <preshared-key> <endpoint> <allowed-ips> <latest-handshake> <rx-bytes> <tx-bytes> <persistent-keepalive>
func parseWireGuardDump(out string, now time.Time) *model.WireGuardInfo {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 {
		return nil
	}

	var interfaces []string
	var info model.WireGuardInfo
	var mostRecentHandshake int64 // unix timestamp of the newest handshake across all peers
	const onlineThreshold = 180   // seconds

	seenIfaces := map[string]bool{}

	for _, line := range lines {
		fields := strings.Fields(line)
		switch len(fields) {
		case 5:
			// Interface line: iface private-key public-key listen-port fwmark
			iface := fields[0]
			if !seenIfaces[iface] {
				seenIfaces[iface] = true
				interfaces = append(interfaces, iface)
			}
		case 9:
			// Peer line: iface pubkey psk endpoint allowed-ips handshake rx tx keepalive
			info.PeersTotal++
			handshakeStr := fields[5]
			handshakeTs, err := strconv.ParseInt(handshakeStr, 10, 64)
			if err == nil && handshakeTs > 0 {
				age := now.Unix() - handshakeTs
				if age <= onlineThreshold {
					info.PeersOnline++
				}
				if handshakeTs > mostRecentHandshake {
					mostRecentHandshake = handshakeTs
				}
			}
			// Accumulate transfer bytes for the first/only interface context
			rx, _ := strconv.ParseInt(fields[6], 10, 64)
			tx, _ := strconv.ParseInt(fields[7], 10, 64)
			info.TransferRxBytes += rx
			info.TransferTxBytes += tx
		}
	}

	if len(interfaces) == 0 {
		return nil
	}

	info.Interface = strings.Join(interfaces, ",")
	if mostRecentHandshake > 0 {
		info.LastHandshakeAge = now.Unix() - mostRecentHandshake
	}

	return &info
}
