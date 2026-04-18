<p align="center">
  <img src="assets/logo.png" alt="Metronous" width="100%"/>
</p>

# Metronous

[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![GitHub release](https://img.shields.io/github/v/release/kiosvantra/metronous?display_name=tag)](https://github.com/kiosvantra/metronous/releases)
[![GitHub stars](https://img.shields.io/github/stars/kiosvantra/metronous?style=social)](https://github.com/kiosvantra/metronous/stargazers)

> Local AI agent telemetry, benchmarking, and model calibration for OpenCode agents.

*Originally developed within the Gentle AI ecosystem.*

Metronous tracks every tool call, session, and cost from your OpenCode agents — then runs weekly benchmarks to tell you which agents are underperforming and which model would save you money.

## What it does

- **Tracks** agent sessions, tool calls, tokens, and cost in real-time
- **Benchmarks** each agent against accuracy and ROI thresholds
- **Recommends** model switches with data-driven reasoning
- **Visualizes** everything in a 5-tab terminal dashboard (TUI)

## Architecture

```
OpenCode → metronous-plugin.ts → HTTP POST /ingest → metronous daemon → SQLite
                                                              ↓
                                                   metronous dashboard (TUI)
```

- **Plugin (`metronous-plugin.ts`)**: OpenCode plugin that captures agent events and forwards them to the daemon via HTTP. Accumulates cost from `step-finish` events, can send `X-Metronous-Auth` when `METRONOUS_INGEST_TOKEN` is set, and persists session cost to `~/.metronous/data/session_costs.json` across restarts.
- **MCP shim (`metronous mcp`)**: stdio↔HTTP bridge launched by OpenCode as an MCP server. Reads the daemon port from `~/.metronous/data/mcp.port`, forwards events, and mirrors the same optional ingest token.
- **Daemon (`metronous server --daemon-mode`)**: Long-lived background service (systemd on Linux) that ingests events, stores them in SQLite, serves `/timeline` plus `/api/timeline/*`, and runs weekly benchmarks at Monday 02:00 local time. If `METRONOUS_INGEST_TOKEN` is set, it validates ingest headers and logs unauthenticated requests during the transition.
- **TUI Dashboard**: 5-tab terminal UI with live tracking, benchmark results, cost charts, and config editing.

For full component details see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).  
For benchmark methodology see [docs/BENCHMARKS.md](docs/BENCHMARKS.md).

## Prerequisites

1. **[OpenCode](https://opencode.ai) installed** — `curl -fsSL https://opencode.ai/install | bash`

Metronous works with OpenCode's built-in agents out of the box. If you have an `opencode.json`, Metronous will patch it automatically. If not, it will create one and you can add providers and agents to it later.

Go 1.22+ is only required for source builds and `go install`.

## Installation

### Support matrix

- **Linux**: official install flow (one command)
- **macOS**: official install flow (one command)
- **Windows**: experimental/manual

### Linux (recommended — one command)

```bash
curl -fsSL https://github.com/kiosvantra/metronous/releases/latest/download/install.sh | bash
```

This script downloads the latest release, verifies the checksum, installs the binary to `~/.local/bin`, and runs `metronous install` to set up the systemd service and configure OpenCode automatically.

> Do not run with `sudo`. Must run as the same normal user that runs OpenCode.

### Linux (manual)

```bash
VERSION=$(curl -sSL https://api.github.com/repos/kiosvantra/metronous/releases/latest | grep '"tag_name"' | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1)
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
TARBALL="metronous_${VERSION#v}_linux_${ARCH}.tar.gz"
curl -fsSLO "https://github.com/kiosvantra/metronous/releases/download/${VERSION}/${TARBALL}"
curl -fsSLO "https://github.com/kiosvantra/metronous/releases/download/${VERSION}/checksums.txt"
sha256sum -c --ignore-missing checksums.txt
tar -xzf "${TARBALL}"
mkdir -p ~/.local/bin
install -m 0755 ./metronous ~/.local/bin/metronous
rm -f "${TARBALL}" checksums.txt
~/.local/bin/metronous install
```

### Via Go (Linux, with systemd user services)

```bash
go install github.com/kiosvantra/metronous/cmd/metronous@latest
# Ensure the installed binary is on your PATH, then run:
metronous install
```

### Manual/source build

```bash
git clone https://github.com/kiosvantra/metronous
cd metronous
go build -o metronous ./cmd/metronous
```

Linux:

```bash
mkdir -p ~/.local/bin
install -m 0755 ./metronous ~/.local/bin/metronous
~/.local/bin/metronous install
```

### Windows manual testing flow (experimental)

```powershell
# Download the matching Windows archive from GitHub Releases,
# for example: metronous_<version>_windows_amd64.zip
# Run PowerShell as Administrator before continuing.
$archive = "metronous_<version>_windows_amd64.zip"
Expand-Archive -Path $archive -DestinationPath .\metronous-release -Force
$exe = Get-ChildItem .\metronous-release -Recurse -Filter metronous.exe | Select-Object -First 1
$dest = "$env:LOCALAPPDATA\Programs\Metronous"
New-Item -ItemType Directory -Force -Path $dest | Out-Null
Move-Item $exe.FullName "$dest\metronous.exe" -Force
& "$dest\metronous.exe" install
```

Optionally verify the archive before extracting it by comparing its SHA-256 hash with `checksums.txt` from the same release.

Run the elevated PowerShell session as the same Windows user account that runs OpenCode.

Windows support is currently experimental. The native service/install flow is still being hardened, so Linux is the only officially supported installer path.

### macOS (one command)

```bash
curl -fsSL https://github.com/kiosvantra/metronous/releases/latest/download/install.sh | bash
```

Same as Linux — downloads the latest release, verifies the checksum, installs the binary to `~/.local/bin`, and runs `metronous install` to set up the daemon and configure OpenCode automatically.

Supports both Intel (amd64) and Apple Silicon (arm64).

> Do not run with `sudo`. Must run as the same normal user that runs OpenCode.

### Windows service notes

```powershell
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" install
```

> **Note:** `metronous install` on Windows requires an elevated terminal (Run as Administrator) to register the Windows service.

For manual control:
```powershell
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" service start
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" service stop
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" service status
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" service uninstall
```

### Configure OpenCode (automatically done by `metronous install` on Linux)

After running `metronous install` on Linux, your OpenCode will be configured with:

1. **MCP shim**: the installed executable path plus `mcp` for telemetry ingestion
2. **OpenCode plugin**: `metronous.ts` copied to `~/.config/opencode/plugins/`

The plugin captures agent sessions and forwards events to the daemon via HTTP.

Then restart OpenCode and it will show **"Metronous Connected"**.

## Usage

### Dashboard

```bash
metronous dashboard
```

The dashboard has five tabs (press the number key to switch):

| # | Tab | Description |
|---|-----|-------------|
| 1 | **Benchmark History Summary** | Weighted historical view of all (agent, model) pairs active in the last 4 weekly cycles. Cascade sort: active model first (marked `●`), superseded models below. Verdict shown only for the active model. |
| 2 | **Benchmark Detailed** | Per-run history grouped into Sunday-bounded run cycles. Press `Enter` to freeze/unfreeze the detail panel. PgUp/PgDn to navigate cycles. Press `F5` to trigger an intraweek benchmark run. |
| 3 | **Tracking** | Real-time session stream (last 20 sessions, refreshes every 2s). Press `Enter` on a session to open the Session Timeline popup showing per-event cost breakdown. |
| 4 | **Charts** | Monthly cost chart by model (log scale, stacked bars) with a day tooltip. Also shows Performance and Responsibility top-3 cards. `←`/`→` to navigate months, `k`/`l` or mouse to move the day cursor. |
| 5 | **Config** | Edit performance thresholds. Changes are saved to `~/.metronous/thresholds.json` and propagated live to the benchmark engine. |

For keyboard navigation see [docs/tui-controls.md](docs/tui-controls.md).

### Session Timeline popup (Tracking tab)

Press `Enter` on any session row in the Tracking tab to open a popup with the full event timeline for that session. The popup shows:

| Column | Meaning |
|--------|---------|
| `Spent(acc)` | Accumulated cost up to this event (cumulative snapshot stored in the DB) |
| `Spent(step)` | Delta between consecutive events = per-LLM-call cost (matches provider billing) |

`Spent(step)` is the most useful column for validating costs: each entry corresponds to one LLM request and its value should match what the provider charges per call.

### Plugin cost tracking

The plugin computes session cost by **summing** the `cost` field from every `step-finish` event emitted by OpenCode's `message.part.updated` hook. This gives the actual per-request cost that accumulates across the session.

Key behaviors:
- Cost is persisted to `~/.metronous/data/session_costs.json` so restarts mid-session do not lose accumulated cost.
- `lastActiveModel` only updates when the model string has a provider prefix (e.g. `opencode/claude-sonnet-4-6`). Bare model names like `claude-sonnet-4-6` are never used to downgrade a known provider-prefixed model.
- NaN and non-finite values in cost and token fields are silently dropped.

### Manual benchmark

```bash
# Via TUI: press F5 on the Benchmark Detailed tab
# Via CLI:
METRONOUS_DATA_DIR=~/.metronous/data go run cmd/run-benchmark/main.go
```

### Offline reports (local-only)

```bash
# Summarize telemetry by semantic phase tag (sdd_phase)
metronous report semantic --data-dir ~/.metronous/data

# Optional: filter by agent and output JSON
metronous report semantic --agent sdd-apply --format json

# Archive pipeline usage (bronze/silver/gold)
metronous report archive-usage --data-dir ~/.metronous/data
```

These commands are local-only: they read local files/SQLite and perform offline aggregation.
No report data is sent to remote services.

For archive pipeline details see [docs/LOCAL_ARCHIVE_PIPELINE.md](docs/LOCAL_ARCHIVE_PIPELINE.md).

## Data directory

All data lives in `~/.metronous/`:

```
~/.metronous/
├── data/
│   ├── tracking.db          # Event telemetry (SQLite, WAL mode)
│   ├── benchmark.db         # Benchmark run history (SQLite)
│   ├── timeline.db          # IGRIS↔BERU portal timeline / handoffs / acks (SQLite)
│   ├── mcp.port             # Dynamic HTTP port (runtime)
│   ├── metronous.pid        # Server PID (runtime)
│   ├── session_costs.json   # Persisted session costs across plugin restarts
│   └── plugin.log           # Plugin debug log (when METRONOUS_DEBUG=true)
├── archive/                 # Optional local archive pipeline (default disabled)
│   ├── bronze/              # Raw captured interactions (local)
│   ├── silver/              # User-filtered/accepted interactions
│   └── gold/                # Manually curated benchmark cases
├── config.yaml              # Runtime config (scheduler + archive toggles)
└── thresholds.json          # Performance thresholds (editable via TUI)
```

## Config thresholds

The Config tab (`5`) exposes three active fields:

| Field | Default | Effect |
|-------|---------|--------|
| **Min Accuracy** | 0.85 | Accuracy below this triggers `SWITCH` |
| **Min ROI Score** | 0.05 | ROI below this triggers `SWITCH` (paid models only) |
| **Max Cost/Session** | $0.50 | Reference for cost semaphore color in the Tracking tab; also used for urgent spike detection |

ROI = `accuracy / avg_cost_per_session`. Higher ROI means more accurate output per dollar spent.

For the full threshold schema and urgent triggers see [docs/BENCHMARKS.md](docs/BENCHMARKS.md).

## Benchmark methodology

- **Accuracy** = `(total_events - error_events) / total_events`
- **ROI** = `accuracy / cost_per_session` where cost_per_session = `sum of MAX(cost_usd) per session_id`
- **Health score** = 60 pts accuracy + 25 pts verdict + 15 pts ROI (0–100 scale)
- **Verdicts**: `KEEP` / `SWITCH` / `URGENT_SWITCH` / `INSUFFICIENT_DATA`
- Minimum sample size: **50 events** (below this → `INSUFFICIENT_DATA`)
- `URGENT_SWITCH` triggers when accuracy < 0.60 or error rate > 30%

For full methodology details see [docs/BENCHMARKS.md](docs/BENCHMARKS.md).

## Agents tracked

Metronous automatically discovers all agents from events in the tracking database:

- **Built-in agents**: `build`, `plan`, `general`, `explore`
- **Custom agents**: any agent defined in `opencode.json` or `~/.config/opencode/agents/*.md`

For benchmarking, each agent is evaluated independently per model used. Here is an example set from the Gentle AI SDD ecosystem:

| Agent | Role |
|-------|------|
| `sdd-orchestrator` | Coordinates sub-agents, never does work inline |
| `sdd-apply` | Implements code changes from task definitions |
| `sdd-explore` | Investigates codebase and thinks through ideas |
| `sdd-verify` | Validates implementation against specs |
| `sdd-spec` | Writes detailed specifications from proposals |
| `sdd-design` | Creates technical design from proposals |
| `sdd-propose` | Creates change proposals from explorations |
| `sdd-tasks` | Breaks down specs and designs into tasks |
| `sdd-init` | Bootstraps SDD context and project configuration |
| `sdd-archive` | Archives completed change artifacts |

## License

MIT — see [LICENSE](LICENSE)
