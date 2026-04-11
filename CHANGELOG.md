# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

## [0.9.0] - 2026-04-10

### Added

- **Database monitoring** (`p1-db-monitoring`): new optional `[[database]]` config section supports PostgreSQL and MySQL/MariaDB; each configured database is polled concurrently via the new `internal/collector/db` package
- Add `DBConnections`, `NormalizedQuery`, `DBSnapshot` types to `internal/model`; `Snapshot.Databases []DBSnapshot` carries per-database metrics in every push payload
- PostgreSQL collector (`lib/pq`): reads connection state from `pg_stat_activity`, max connections from `pg_settings`, delta query stats from `pg_stat_statements` (optional extension, graceful fallback), ungranted lock count from `pg_locks`, replica lag from `pg_last_xact_replay_timestamp()`
- MySQL/MariaDB collector (`go-sql-driver/mysql`): reads thread counts from `information_schema.global_status`, throughput delta from `Queries` counter, slow query digest from `performance_schema.events_statements_summary_by_digest` (graceful fallback), lock-wait count from `INFORMATION_SCHEMA.INNODB_TRX`, replica lag from `SHOW REPLICA STATUS`
- DSN resolver in `db.Manager`: supports `${ENV_VAR}`, `file:/path`, and plain string DSNs; fails fast at startup if env var is unset
- New **DB card** in TUI (`renderDBCard`): connection utilization bar (red >80%, yellow >60%), avg/p95 latency, queries/interval throughput, lock count, replication lag, and the top slow query template
- New `db.Manager.Enrich(ctx, *Snapshot)` enrichment step called by `runOnce` and the watch-mode background goroutine; per-collector deadline 10 s; errors surfaced as `DBSnapshot.Error` without blocking other collectors or the main snapshot

### Cloud integration (sumi-cloud)

- Mirror `DBConnections`, `NormalizedQuery`, `DBSnapshot` types in `internal/model/snapshot.go`
- Add `Databases []DBSnapshot` field to cloud `Snapshot`; the push handler stores the full snapshot as JSONB so DB metrics are automatically persisted without schema migration
- Query DB metrics from the `device_metrics` hypertable using JSONB operators, e.g.:
  ```sql
  -- Connection utilization over time for a specific database
  SELECT ts,
         (payload->'Databases'->0->>'Name')                                      AS db_name,
         (payload->'Databases'->0->'Connections'->>'Active')::int                AS active,
         (payload->'Databases'->0->'Connections'->>'Max')::int                   AS max_conns
    FROM device_metrics
   WHERE device_id = '<device_id>'
     AND payload->'Databases' IS NOT NULL
   ORDER BY ts DESC
   LIMIT 100;
  ```

### Dependencies

- Add `github.com/lib/pq v1.12.3` (pure-Go PostgreSQL driver)
- Add `github.com/go-sql-driver/mysql v1.9.3` (pure-Go MySQL/MariaDB driver)

## [0.8.1] - 2026-04-10

## [0.6.0] - 2026-04-02

### Performance

- Add parallel fan-out to `linuxCollector.Collect()`: Group A (CPU + Net, ~1 s each) runs concurrently; Group B (Mem/Disk/Procs/Thermal/Battery) and Group C (GPU) run independently — total wall time collapses from ~2 s to ~1 s on Linux
- Add `sync.Once` static cache for hostname, CPU model, and core count on both darwin and linux collectors (read once on first tick, reused every subsequent tick)
- Add disk-total cache keyed by sorted mount-point hash in `linuxCollector`; `TotalBytes` served from `map[string]uint64` when mount list is stable
- Replace `top -l 2 -n 0` with `iostat -c 2 -w 1` for CPU usage on macOS — no process enumeration, same 1 s window
- Replace `netstat` subprocess with in-process `syscall.RouteRIB(NET_RT_IFLIST2, 0)` for network byte counters on macOS — parses `unix.IfMsghdr2.Data.Ibytes/Obytes` (64-bit counters) without forking
- Add 64 KiB `bufio.Writer` for TUI output; single `Flush()` per frame replaces ~40–60 individual `write(2)` syscalls
- Replace blocking `ticker.C → Collect()` watch loop with non-blocking `snapCh chan model.Snapshot` (cap 1); background goroutine collects on ticker; main event loop never stalls keyboard input

## [0.5.0] - 2026-04-02

### Added

- Add `ReadKBps` and `WriteKBps` to `DiskInfo`; macOS uses `iostat -d -c 2 1` delta, Linux uses `/proc/diskstats` delta; Disk card shows I/O speed per mount
- Add `NetRxSpark` and `NetTxSpark` to `History`; Net card in full mode shows two sparklines (green Rx, orange Tx) below KB/s rows
- Add NDJSON streaming mode: `--watch --renderer json` emits one compact JSON object per line, flushed immediately — pipe-friendly for `jq`, Grafana, and Prometheus
- Add `BatteryInfo` struct and battery card; macOS via `pmset -g batt`, Linux via `/sys/class/power_supply/`; card omitted on desktops without a battery
- Add container badge detection in process list: reads `/proc/PID/cgroup` on Linux to detect Docker (`[d]`) and Kubernetes (`[k]`) namespaces; macOS always empty
- Add process detail panel opened with `d` key: shows PPID, thread count, open FD count, cwd, start time, and per-PID CPU/Mem sparklines; close with `d` again or `Esc`

## [0.4.0] - 2026-04-02

### Added

- Add optional `*GPUInfo` to `Snapshot`; collectors detect Nvidia (`nvidia-smi`), AMD (`rocm-smi`), and Apple (`powermetrics`); GPU card appears between CPU and Memory when data is available
- Add `internal/history.Ring` fixed-capacity ring buffer with `Sparkline()` method; CPU and Memory cards show sparkline history in full mode
- Break `Snapshot.Disk DiskInfo` into `Snapshot.Disks []DiskInfo`; Disk card shows up to 4 mount points with scroll counter
- Add interactive raw-terminal mode via `golang.org/x/term`; keybindings: `q`/`Ctrl+C` quit, `v` toggle compact, `t` cycle theme, `j`/`k` process selection, `s` sort toggle, `Enter` SIGTERM with confirmation
- Add configurable alert thresholds in TOML config; breached card borders flash; `\a` bell fires once per cycle; alert count shown in System card

## [0.3.0] - 2026-04-01

### Added

- Add `internal/theme` package with `Theme` struct (10 RGB colors), `ANSI()` converter, and `Load()` that checks user dir then embedded files
- Add six built-in themes embedded at build time: `tokyo-night`, `gruvbox`, `catppuccin-mocha`, `nord`, `dracula`, `one-dark`
- Add `--theme NAME` and `--list-themes` CLI flags
- Add `BorderStyle` config field (`rounded`, `sharp`, `double`, `bold`) backed by `BoxChars` struct

### Changed

- Refactor `tui.go` to remove hardcoded ANSI color constants; renderer now accepts `theme.Theme` and `BoxChars` at construction time

## [0.2.0] - 2026-03-31

### Added

- Add TOML config file at `~/.config/sumi/config.toml` via `BurntSushi/toml`; CLI flags always override config; `--init-config` writes annotated defaults
- Add compact layout mode (3-line cards with key metric + bar); `v` key toggles compact/full at runtime
- Add `CoreUsages []float64` to `CPU` in `Snapshot`; full mode renders per-core micro-bars under the main CPU bar

[Unreleased]: https://github.com/denisfl/sumi/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/denisfl/sumi/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/denisfl/sumi/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/denisfl/sumi/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/denisfl/sumi/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/denisfl/sumi/releases/tag/v0.2.0
