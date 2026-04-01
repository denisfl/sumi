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

CPU temperature on macOS requires the `osx-cpu-temp` tool:

```bash
brew install osx-cpu-temp
```

Once installed, the THERMAL card will show the real CPU temperature automatically.

## Platforms

| Platform              | CPU | Memory | Disk | Network | Thermal              |
| --------------------- | --- | ------ | ---- | ------- | -------------------- |
| macOS (Apple Silicon) | Yes | Yes    | Yes  | Yes     | via `osx-cpu-temp`   |
| macOS (Intel)         | Yes | Yes    | Yes  | Yes     | via `osx-cpu-temp`   |
| Linux x86_64          | Yes | Yes    | Yes  | Yes     | `/sys/class/thermal` |
| Raspberry Pi (arm64)  | Yes | Yes    | Yes  | Yes     | Full (vcgencmd)      |
| Raspberry Pi (armv7)  | Yes | Yes    | Yes  | Yes     | Full (vcgencmd)      |

## License

MIT — see [LICENSE](LICENSE).

---

Made by [@fedosov.me](https://bsky.app/profile/fedosov.me) · [GitHub](https://github.com/denisfl)
