# sumi

A lightweight, zero-dependency system monitor for macOS, Linux, and Raspberry Pi. Written in Go, styled with the Tokyo Night color palette.

```
╭─ THERMAL ──────────────────────────╮ ╭─ CPU ──────────────────────────────╮
├────────────────────────────────────┤ ├────────────────────────────────────┤
│ temp   55.2°C                      │ │ load   █████████████░░░░░░░░░░░░░░░│
│ arm    1800 MHz                    │ │                                    │
│ gpu    750 MHz                     │ │ cores  4                           │
│ thrt   ok                          │ │                                    │
╰────────────────────────────────────╯ ╰────────────────────────────────────╯

╭─ Memory ───────────────────────────╮ ╭─ Disk ─────────────────────────────╮
│Total: 8.00 GiB                     │ │Mount:  /                           │
│Used:  5.12 GiB                     │ │Total:  120.00 GiB                  │
│Free:  2.88 GiB                     │ │Used:   42.30 GiB                   │
│Usage: 64.0%                        │ │Free:   77.70 GiB                   │
│████████████████████░░░░░░░░░░░░░░  │ │Usage:  35.2%                       │
│Swap:  1.50 GiB / 2.00 GiB          │ │███████████░░░░░░░░░░░░░░░░░░░░░░░  │
│███████████████████████████████░░░  │ ╰────────────────────────────────────╯
╰────────────────────────────────────╯

╭─ Network ──────────────────────────╮ ╭─ Top Processes ────────────────────╮
│Iface: eth0                         │ │NAME                   CPU%   MEM%  │
│IP:    192.168.1.42                 │ │chromium               38.0    5.2  │
│Rx:    124.6 KB/s                   │ │Xorg                   12.4    1.8  │
│Tx:    18.3 KB/s                    │ │code                    8.2    3.4  │
╰────────────────────────────────────╯ │bash                    0.5    0.1  │
                                       │sshd                    0.1    0.0  │
                                       ╰────────────────────────────────────╯

╭─ System ─────────────────────────────────────────────────────────────────────╮
├──────────────────────────────────────────────────────────────────────────────┤
│ host       raspberrypi          platform   rpi            date  2026-03-31   │
│ uptime     12d 04:21:07         time       18:42:05                          │
╰──────────────────────────────────────────────────────────────────────────────╯
```

## Features

- **4-row, 2-column bento grid** — THERMAL, CPU, Memory, Disk, Network, Top Processes, System
- **Tokyo Night color palette** — ANSI TrueColor, no terminal emulator flicker
- **Raspberry Pi support** — ARM/GPU frequency, throttle status via `vcgencmd`
- **macOS support** — temperature via `osx-cpu-temp` (optional), swap, real Rx/Tx KB/s delta
- **Linux support** — `/proc/stat` CPU delta, `/proc/meminfo`, `/sys/class/net`, thermal zone
- **JSON output mode** — pipe-friendly, machine-readable snapshot via `--renderer json`
- **Watch mode** — refresh every N seconds (`--watch --interval 5`)
- **Zero dependencies** — single static binary, no runtime libraries required

## Installation

### Pre-built binary (recommended)

Download the binary for your platform from the [Releases](https://github.com/denisfl/sumi/releases) page.

```bash
# macOS Apple Silicon
curl -L https://github.com/denisfl/sumi/releases/latest/download/sumi-darwin-arm64.tar.gz | tar xz
sudo mv sumi /usr/local/bin/

# macOS Intel
curl -L https://github.com/denisfl/sumi/releases/latest/download/sumi-darwin-amd64.tar.gz | tar xz
sudo mv sumi /usr/local/bin/

# Linux x86_64
curl -L https://github.com/denisfl/sumi/releases/latest/download/sumi-linux-amd64.tar.gz | tar xz
sudo mv sumi /usr/local/bin/

# Linux ARM64 (Raspberry Pi 4/5)
curl -L https://github.com/denisfl/sumi/releases/latest/download/sumi-linux-arm64.tar.gz | tar xz
sudo mv sumi /usr/local/bin/

# Linux ARMv7 (Raspberry Pi 3 32-bit)
curl -L https://github.com/denisfl/sumi/releases/latest/download/sumi-linux-arm.tar.gz | tar xz
sudo mv sumi /usr/local/bin/
```

### Build from source

```bash
git clone https://github.com/denisfl/sumi.git
cd sumi
go build -o sumi ./cmd/monitor
```

Requires Go 1.22 or later.

## Usage

```
sumi [flags]

Flags:
  -w, --watch             Run in watch mode (refresh continuously)
  -n, --interval int      Refresh interval in seconds (default 5)
      --renderer string   Output renderer: tui or json (default "tui")
  -h, --help              Show help
```

### Examples

```bash
# Single snapshot
sumi

# Watch mode, refresh every 3 seconds
sumi --watch --interval 3

# JSON output (pipe-friendly)
sumi --renderer json | jq .CPU.Usage
```

### Optional: macOS temperature

CPU temperature on macOS requires the `osx-cpu-temp` tool:

```bash
brew install osx-cpu-temp
```

Once installed, the THERMAL card will show the real CPU temperature automatically.

## Platforms

| Platform | CPU | Memory | Disk | Network | Thermal |
|---|---|---|---|---|---|
| macOS (Apple Silicon) | Yes | Yes | Yes | Yes | via `osx-cpu-temp` |
| macOS (Intel) | Yes | Yes | Yes | Yes | via `osx-cpu-temp` |
| Linux x86_64 | Yes | Yes | Yes | Yes | `/sys/class/thermal` |
| Raspberry Pi (arm64) | Yes | Yes | Yes | Yes | Full (vcgencmd) |
| Raspberry Pi (armv7) | Yes | Yes | Yes | Yes | Full (vcgencmd) |

## License

MIT — see [LICENSE](LICENSE).

---

Made by [@fedosov.me](https://bsky.app/profile/fedosov.me) · [GitHub](https://github.com/denisfl)
