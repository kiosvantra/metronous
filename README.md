<p align="center">
  <img src="assets/logo.png" alt="Metronous" width="100%"/>
</p>

# Metronous

> Local AI agent telemetry, benchmarking, and model calibration for OpenCode agents.

*Originally developed within the Gentle AI ecosystem.*

Metronous tracks every tool call, session, and cost from your OpenCode agents — then runs weekly benchmarks to tell you which agents are underperforming and which model would save you money.

## What it does

- **Tracks** agent sessions, tool calls, tokens, and cost in real-time
- **Benchmarks** each agent with a defined mission against its performance criteria
- **Recommends** model switches with estimated cost savings
- **Visualizes** everything in a terminal dashboard (TUI)

## Architecture

> For component details and protocols, see [docs/architecture.md](docs/architecture.md).  
> For benchmark methodology, see [docs/how-it-works.md](docs/how-it-works.md).

```
OpenCode → metronous mcp (shim) → HTTP → metronous daemon (system service) → SQLite
                                                                        ↓
                                                              ./metronous dashboard
```

- **Shim (metronous mcp)**: stdio↔HTTP bridge launched by OpenCode plugin, forwards MCP calls to the daemon
- **Daemon (metronous)**: Long-lived system service (systemd on Linux, Windows SCM on Windows) that handles telemetry ingestion, storage, and weekly benchmarks
- **HTTP Endpoint**: Dynamic port (written to `~/.metronous/mcp.port`) for shim-to-daemon communication
- **TUI Dashboard**: 3-tab terminal UI (Tracking / Benchmark / Config)

## Prerequisites

- [OpenCode](https://opencode.ai) installed and configured
- Go 1.22+
- OpenCode agents configured (e.g., from Gentle AI's SDD suite)

## Installation

### Zero-friction (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/kiosvantra/metronous/main/install.sh | sh
metronous install
# Done — daemon running as systemd user service, OpenCode configured to use ["metronous", "mcp"]
```

### Via Go (alternative)

```bash
go install github.com/kiosvantra/metronous/cmd/metronous@v0.9.9
```

### Manual installation (alternative)

```bash
git clone https://github.com/kiosvantra/metronous
cd metronous
go build -o metronous ./cmd/metronous
# Add the binary to your PATH or use the full path below

# Install as a systemd user service and patch opencode.json automatically
./metronous install

# Manual steps if you prefer:
# 1. Initialize Metronous (creates ~/.metronous/ and databases)
# ./metronous init
# 2. Start the daemon manually (for testing):
# ./metronous server --data-dir ~/.metronous/data --daemon-mode
# 3. Or install the systemd service yourself:
# ./metronous install   # does steps 1-4 below
#   a) writes ~/.config/systemd/user/metronous.service
#   b) systemctl --user daemon-reload
#   c) systemctl --user enable metronous
#   d) systemctl --user start metronous
#   e) patches ~/.config/opencode/opencode.json to use ["metronous", "mcp"]
```

### Windows installation

```powershell
go install github.com/kiosvantra/metronous/cmd/metronous@v0.9.9
metronous install
# Done — service registered via Windows SCM, OpenCode configured
```

> **Note:** `metronous install` on Windows requires an elevated terminal (Run as Administrator) to register the Windows service. Use `metronous service status` or `sc query metronous` to verify.

For manual control:
```powershell
metronous service start    # Start the service
metronous service stop     # Stop the service
metronous service status   # Check service status
metronous service uninstall # Remove the service
```

### Configure OpenCode (automatically done by `metronous install`)

After running `metronous install`, your `~/.config/opencode/opencode.json` will contain:

```json
{
  "mcp": {
    "metronous": {
      "command": ["metronous", "mcp"],
      "type": "local"
    }
  },
  "plugins": ["metronous-opencode"]
}
```

Then restart OpenCode and it will show **"Metronous Connected"**.

## Usage

### Dashboard

```bash
metronous dashboard
```

- **[1] Tracking** — Real-time event stream with tokens and cost per tool call
- **[2] Benchmark** — Agent performance history with verdict, recommended model, and savings estimate
- **[3] Config** — Edit performance thresholds (saved to `~/.metronous/thresholds.json`)

### Manual benchmark

```bash
METRONOUS_DATA_DIR=~/.metronous/data go run cmd/run-benchmark/main.go
```

## Data directory

All data lives in `~/.metronous/`:

```
~/.metronous/
├── data/
│   ├── tracking.db      # Event telemetry (SQLite)
│   ├── benchmark.db     # Benchmark run history (SQLite)
│   ├── mcp.port         # Dynamic HTTP port (runtime)
│   └── metronous.pid    # Server PID (runtime)
└── thresholds.json      # Performance thresholds (editable via TUI)
```

## Agents tracked

Metronous automatically discovers all agents from your OpenCode configuration:

- **Built-in agents**: `build`, `plan`, `general`, `explore`  
- **Custom agents**: any agent defined in `opencode.json` or `~/.config/opencode/agents/*.md` (at global or project level), with type `primary`, `subagent`, or `all`

For benchmarking, Metronous requires each agent to have a **mission** defined (via the `description` field in `opencode.json` or YAML frontmatter in the agent's markdown file). Here's an example with the Gentle AI SDD agents:

| Agent | Mission |
|-------|---------|
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
