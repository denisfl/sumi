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

- **Disk I/O speed** — real-time read/write KB/s per mount point shown under the disk bar
- **Network sparkline history** — rolling 30-sample sparklines for Rx (green) and Tx (orange) in the Network card
- **Battery card** — charge percentage, charging status, and time-remaining; auto-hidden on desktops/servers (no battery)
- **Container badges** — `[d]` / `[k]` prefix in Top Processes when a process runs inside Docker or Kubernetes
- **Process detail panel** — press `d` to open a per-process panel with PPID, threads, open FDs, cwd, start time, and scrolling CPU/Mem sparklines; press `d` or `Esc` to close
- **NDJSON streaming** — `--watch --renderer json` emits one compact JSON object per line, pipe-friendly for `jq`/Grafana/Prometheus scrapers
- **Database monitoring (v0.9)** — optional `[[database]]` blocks in the config file; collects connection utilization, avg/p95 query latency, throughput, lock count, replication lag, and top slow queries for PostgreSQL and MySQL/MariaDB; displayed as full-width DB cards below the System card; supports DSN via plain string, `${ENV_VAR}`, or `file:/path`
- **Extended net metrics (v0.7)** — established TCP connection count, gateway ping latency + packet-loss %, TCP retransmit delta, RX/TX interface errors
- **Extended disk metrics (v0.7)** — inode saturation %, average I/O service time (`AwaitMs`), drive health via `smartctl` (`ok`/`warn`/`fail`)
- **System load (v0.7)** — 1/5/15-minute load averages, uptime, file-descriptor saturation %, zombie process count, context-switches/sec
- **System events (v0.7)** — OOM kills, disk errors, SSH failures, service restarts, reboots captured from `dmesg`/`journalctl` (Linux) and pushed to backend
- **WireGuard monitoring (v0.7)** — peer count, online peers (last handshake <180 s), aggregate transfer bytes; cross-platform via `wg show all dump`
- **6 built-in color themes** — tokyo-night (default), gruvbox, catppuccin-mocha, nord, dracula, one-dark
- **4 border styles** — rounded (default), sharp, double, bold
- **Compact mode** — condensed 4-line cards (`--compact`) for smaller terminals
- **Per-core CPU micro-bars** — Unicode bar chart when per-core data is available
- **Raspberry Pi support** — ARM/GPU frequency, throttle status via `vcgencmd`
- **macOS support** — temperature via `osx-cpu-temp` (optional), swap, real Rx/Tx KB/s delta
- **Linux support** — `/proc/stat` CPU delta, `/proc/meminfo`, `/sys/class/net`, thermal zone
- **JSON output mode** — pipe-friendly, machine-readable snapshot via `--renderer json`
- **Watch mode** — refresh every N seconds (`--watch --interval 5`)
- **TOML config file** — persistent settings at `~/.config/sumi/config.toml`
- **Zero runtime dependencies** — single static binary

### Performance

| Platform | Improvement                                                                                        |
| -------- | -------------------------------------------------------------------------------------------------- |
| Linux    | Collect() fan-out: CPU + Net run concurrently (~1 s total vs ~2 s sequential)                      |
| macOS    | CPU usage via `iostat` (lighter than `top`); Net bytes via in-process `syscall.RouteRIB` (no fork) |
| Both     | Static data (hostname, CPU model, core count) cached on first tick — never re-queried              |
| Both     | Disk `TotalBytes` cached by mount hash — refreshed only when mounts change                         |
| Both     | Single 64 KiB buffered write per TUI frame (vs 40–60 write syscalls)                               |
| Both     | Keyboard events processed immediately — never blocked by slow Collect()                            |

### Interactive keybindings (watch mode)

| Key            | Action                                                                        |
| -------------- | ----------------------------------------------------------------------------- |
| `q` / `Ctrl+C` | Quit                                                                          |
| `v`            | Toggle compact / full mode                                                    |
| `t`            | Cycle to next color theme                                                     |
| `j` / `k`      | Move selection down / up in Top Processes                                     |
| `s`            | Toggle sort by CPU% / MEM%                                                    |
| `d`            | Open process detail panel for selected process; press again or `Esc` to close |
| `Enter`        | Prompt to send SIGTERM to selected process                                    |

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
  -w, --watch               Run in watch mode (refresh continuously)
  -n, --interval int        Refresh interval in seconds (default 5)
      --renderer string     Output renderer: tui or json (default "tui")
      --compact             Use compact (4-line) card layout
      --theme string        Color theme name (default "tokyo-night")
      --border string       Border style: rounded|sharp|double|bold (default "rounded")
      --list-themes         List available themes and exit
      --init-config         Write default config to ~/.config/sumi/config.toml and exit
      --version             Print version and exit
  -h, --help                Show help
```

### Examples

```bash
# Single snapshot
sumi

# Watch mode, refresh every 3 seconds
sumi --watch --interval 3

# Use gruvbox theme with bold borders
sumi --theme gruvbox --border bold

# Compact layout with Catppuccin Mocha theme
sumi --compact --theme catppuccin-mocha

# List all available themes
sumi --list-themes

# JSON output (pipe-friendly)
sumi --renderer json | jq .CPU.Usage

# NDJSON streaming (one JSON object per line — for pipes, Grafana, etc.)
sumi --watch --renderer json | jq '.CPU.Usage'
```

## Configuration

sumi reads a TOML config file at `~/.config/sumi/config.toml`  
(or `$XDG_CONFIG_HOME/sumi/config.toml` if set).

### Generate default config

```bash
sumi --init-config
```

This writes an annotated config to `~/.config/sumi/config.toml`:

```toml
# sumi configuration — https://github.com/denisfl/sumi

# Refresh interval in seconds (watch mode)
interval = 5

# Output renderer: "tui" or "json"
renderer = "tui"

# Color theme. Built-in: tokyo-night, gruvbox, catppuccin-mocha, nord, dracula, one-dark
theme = "tokyo-night"

# Card border style: "rounded", "sharp", "double", "bold"
border_style = "rounded"

# Show compact (3-line) cards instead of full detail cards
compact_mode = false

# Card order (remove a name to hide that card)
widgets = ["thermal", "cpu", "memory", "disk", "network", "processes", "system"]
```

### Config options

| Key            | Type     | Default         | Description                                        |
| -------------- | -------- | --------------- | -------------------------------------------------- |
| `interval`     | int      | `5`             | Refresh interval in seconds (watch mode)           |
| `renderer`     | string   | `"tui"`         | Output renderer: `tui` or `json`                   |
| `theme`        | string   | `"tokyo-night"` | Color theme name                                   |
| `border_style` | string   | `"rounded"`     | Border style: `rounded`, `sharp`, `double`, `bold` |
| `compact_mode` | bool     | `false`         | Use compact card layout                            |
| `widgets`      | []string | all             | Card order; remove a name to hide that card        |

CLI flags always override config file values.

### Database monitoring

Add one `[[database]]` block per database to `~/.config/sumi/config.toml`:

```toml
[[database]]
name       = "prod-postgres"
driver     = "postgres"
dsn        = "host=db.example.com port=5432 user=mon password=secret dbname=app sslmode=require"
interval_s = 30

[[database]]
name       = "analytics"
driver     = "mysql"
dsn        = "${MYSQL_DSN}"         # reads DSN from environment variable
interval_s = 60
```

Supported drivers: `postgres` / `postgresql`, `mysql` / `mariadb`.

DSN formats:
- **Plain string** — used as-is (dev use only; prefer env var or file for credentials)
- **`${ENV_VAR}`** — value read from the named environment variable at startup
- **`file:/absolute/path`** — first line of the file, trimmed; suitable for secrets mounted by Docker/k8s

Each DB card shows:
- Connection utilization bar (green < 60 %, yellow < 85 %, red ≥ 85 %)
- Average and p95 query latency, queries-per-interval throughput
- Active lock count (highlighted red if > 0), replication lag / `primary` label
- Top slow query template (requires `pg_stat_statements` on PostgreSQL / `performance_schema` on MySQL)

The DB section is hidden entirely when no `[[database]]` entries are configured.

### Custom themes

Place a custom TOML theme file at `~/.config/sumi/themes/<name>.toml`.  
User themes take precedence over built-ins with the same name.

Theme file format:

```toml
name = "my-theme"

[border]
r = 86; g = 95; b = 137

[title]
r = 122; g = 162; b = 247

[text]
r = 192; g = 202; b = 245

[green]
r = 158; g = 206; b = 106

[yellow]
r = 224; g = 175; b = 104

[red]
r = 247; g = 118; b = 142

[cyan]
r = 125; g = 207; b = 255

[purple]
r = 187; g = 154; b = 247

[orange]
r = 255; g = 158; b = 100

[teal]
r = 42; g = 195; b = 222
```

Once installed, the THERMAL card will show the real CPU temperature automatically.

### Optional: macOS temperature

CPU temperature on macOS requires an external tool:

**Intel Macs** — install via Homebrew:

```bash
brew install osx-cpu-temp
```

**Apple Silicon (M1/M2/M3/M4)** — build `smctemp` from source:

```bash
git clone https://github.com/narugit/smctemp.git
cd smctemp
make
sudo make install
```

sumi tries `osx-cpu-temp` first, then falls back to `smctemp` automatically. Once either tool is installed and on `$PATH`, the THERMAL card will display the real CPU temperature.

## Platforms

| Platform              | CPU | Memory | Disk | Disk I/O | Network | Thermal                           | Battery | Containers |
| --------------------- | --- | ------ | ---- | -------- | ------- | --------------------------------- | ------- | ---------- |
| macOS (Apple Silicon) | Yes | Yes    | Yes  | Yes      | Yes     | via `smctemp` (build from source) | Yes     | —          |
| macOS (Intel)         | Yes | Yes    | Yes  | Yes      | Yes     | via `osx-cpu-temp` or `smctemp`   | Yes     | —          |
| Linux x86_64          | Yes | Yes    | Yes  | Yes      | Yes     | `/sys/class/thermal`              | Yes     | Docker/k8s |
| Raspberry Pi (arm64)  | Yes | Yes    | Yes  | Yes      | Yes     | Full (vcgencmd)                   | —       | Docker/k8s |
| Raspberry Pi (armv7)  | Yes | Yes    | Yes  | Yes      | Yes     | Full (vcgencmd)                   | —       | Docker/k8s |

## License

MIT — see [LICENSE](LICENSE).

---

Made by [@fedosov.me](https://bsky.app/profile/fedosov.me) · [GitHub](https://github.com/denisfl)
