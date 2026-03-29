<p align="center">
  <img src="assets/logo.png" alt="Metronous" width="100%"/>
</p>

# Metronous

> Local AI agent telemetry, benchmarking, and model calibration for the [Gentle AI](https://github.com/Gentleman-Programming/gentle-ai) ecosystem.

Metronous tracks every tool call, session, and cost from your OpenCode agents — then runs weekly benchmarks to tell you which agents are underperforming and which model would save you money.

## What it does

- **Tracks** agent sessions, tool calls, tokens, and cost in real-time
- **Benchmarks** each SDD agent (sdd-orchestrator, sdd-apply, sdd-explore, etc.) against its mission
- **Recommends** model switches with estimated cost savings
- **Visualizes** everything in a terminal dashboard (TUI)

## Architecture

```
OpenCode → metronous plugin → HTTP POST /ingest → metronous server → SQLite
                                                                        ↓
                                                              ./metronous dashboard
```

- **MCP Server**: Spawned by OpenCode, manages SQLite via stdio MCP protocol
- **HTTP Endpoint**: Dynamic port for plugin telemetry ingestion
- **Plugin**: TypeScript OpenCode plugin that captures events and sends them
- **TUI Dashboard**: 3-tab terminal UI (Tracking / Benchmark / Config)

## Prerequisites

- [OpenCode](https://opencode.ai) with [OpenCode Zen](https://opencode.ai/docs/zen/) configured
- Go 1.22+
- The [Gentle AI](https://github.com/Gentleman-Programming/gentle-ai) SDD agent setup

## Installation

### 1. Build the binary

```bash
git clone https://github.com/Gentleman-Programming/metronous
cd metronous
go build -o metronous ./cmd/metronous
sudo mv metronous /usr/local/bin/  # or add to PATH
```

### 2. Install the OpenCode plugin

```bash
# Symlink the plugin into OpenCode's plugin directory
ln -sf $(pwd)/plugins/metronous-opencode ~/.config/opencode/plugins/metronous-opencode

# Or copy the pre-built plugin
cp plugins/metronous-opencode/dist/index.js ~/.config/opencode/plugins/metronous-opencode/dist/index.js
```

### 3. Configure OpenCode

Add to your `~/.config/opencode/opencode.json`:

```json
{
  "mcp": {
    "metronous": {
      "command": ["/usr/local/bin/metronous", "server", "--data-dir", "~/.metronous/data"],
      "type": "local"
    }
  },
  "plugins": ["metronous-opencode"]
}
```

### 4. Restart OpenCode

Metronous will appear as **"Metronous Connected"** in your MCP server list.

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

Metronous tracks all SDD agents defined in your `opencode.json`:

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
