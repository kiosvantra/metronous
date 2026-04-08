# Architecture Overview

This document describes the runtime architecture of Metronous, focusing on components, communication protocols, data flow, and deployment details. It complements [`how-it-works.md`](how-it-works.md) which covers the methodological approach to benchmarking and model recommendation.

## Table of Contents
- [Core Components](#core-components)
- [Communication Protocols](#communication-protocols)
- [Data Flow](#data-flow)
- [Systemd Service Details](#systemd-service-details)
- [Failure Handling & Resilience](#failure-handling--resilience)
- [Directory Layout](#directory-layout)
- [Extensibility](#extensibility)
- [Sequence Diagram (Textual)](#sequence-diagram-textual)

---

## Core Components

| Component | Responsibility | Technology | Notes |
|-----------|----------------|------------|-------|
| **OpenCode Plugin** | Captures telemetry events from agent sessions/tool calls and forwards them via MCP | TypeScript (bundled in `plugins/metronous-opencode/`) | Runs inside OpenCode; sends MCP `tools/call ingest` to the shim |
| **metronous mcp shim** (`metronous mcp`) | stdio↔HTTP bridge; translates MCP stdio messages to HTTP POST/GET to the daemon | Go | Launched by OpenCode plugin per session; lightweight, short-lived per invocation |
| **Metronous Daemon** (`metronous server --daemon-mode`) | Long-lived systemd user service that ingests telemetry, stores it in SQLite, runs weekly benchmarks, exposes HTTP API | Go | Managed by systemd; one instance per user; survives OpenCode restarts |
| **SQLite Stores** | Persistent storage for raw events (`tracking.db`) and pre-aggregated benchmark data (`benchmark.db`) | SQLite via `internal/store/sqlite/` | File-based; located in `~/.metronous/data/` |
| **CLI (`metronous` command)** | User-facing commands: `install`, `init`, `benchmark`, `report`, `dashboard`, etc. | Go/Cobra | Interacts with daemon via direct function calls (when run locally) or HTTP (if daemon remote—not currently supported) |
| **TUI Dashboard** (`metronous dashboard`) | Terminal UI showing five tabs: Benchmark History Summary, Benchmark Detailed, Tracking, Charts, and Config | Go/Bubbletea | Reads directly from SQLite files; presents telemetry, cost charts, and benchmark results |
| **OpenCode MCP Configuration** | Tells OpenCode how to reach the shim | JSON in `~/.config/opencode/opencode.json` | After `metronous install`: `{ "mcp": { "metronous": { "command": ["/absolute/path/to/metronous", "mcp"], "type": "local" } } }` |

---

## Communication Protocols

### 1. OpenCode → Shim (MCP over stdio)
- **Direction**: OpenCode plugin → shim (stdin/stdout of the `metronous mcp` process)
- **Protocol**: [Model Context Protocol (MCP)](https://modelcontextprotocol.io) over stdio, using JSON-RPC 2.0
- **Messages**:
  - `tools/call` with `{ name: "ingest", arguments: { ...event payload... } }` (most common)
  - `tools/list` (to discover available tools; shim currently only advertises `ingest`)
  - `initialize` / `notifications/initialized` (handled per spec)

### 2. Shim → Daemon (HTTP)
- **Direction**: shim → daemon (outgoing HTTP POST/GET from shim to daemon)
- **Protocol**: HTTP/1.1 over TCP (loopback only, `127.0.0.1`)
- **Endpoints**:
  - `POST /ingest` – receives telemetry event (JSON body matches MCP `ingest` arguments)
  - `GET /health` – liveness probe (returns `{status:"ok"}`)
  - `GET /status` – alias for `/health`
  - `GET /tools` – returns list of supported tools (currently only `ingest`)
- **Details**:
  - shim reads the port from `~/.metronous/data/mcp.port`
  - if `METRONOUS_INGEST_TOKEN` is set, the shim includes `X-Metronous-Auth` on ingest requests and the daemon validates it during the transition
  - shim performs a health check (`GET /health`) before using a cached port to avoid dead daemon connections
  - shim uses a short HTTP timeout; on failure it treats the daemon as unhealthy and retries via the normal daemon startup path

### 3. Daemon → OpenCode (Indirect)
- The daemon does **not** push data to OpenCode. OpenCode pulls insights via:
  - TUI Dashboard (reads SQLite files directly)
  - `metronous report` CLI command
  - Manual inspection of `~/.metronous/thresholds.json` (which contains the active model recommendation after a benchmark run)

### 4. CLI / TUI → Daemon (Local Function Calls)
- When the `metronous` CLI or TUI is run on the same machine, it accesses the SQLite files **directly** (no HTTP involved). This is faster and avoids an extra hop.
- The daemon, when running as a systemd service, still holds the SQLite files open; concurrent reads are safe thanks to SQLite's read‑only transaction isolation and WAL mode.

---

## Data Flow

1. **Event Capture**  
   OpenCode plugin detects an agent event (e.g., `tool_call`) and builds a JSON payload matching the `ingest` tool schema.

2. **MCP Transmission**  
   Plugin sends the event as an MCP `tools/call ingest` message to the `metronous mcp` shim over stdio.

3. **Shim Processing**  
    - Shim parses the MCP message, extracts the JSON payload.
    - Shim reads the current daemon port from `~/.metronous/data/mcp.port`.
    - If `METRONOUS_INGEST_TOKEN` is set, Shim adds `X-Metronous-Auth` to the HTTP request.
    - Shim forwards the payload as an HTTP POST to `http://127.0.0.1:<port>/ingest`.

4. **Daemon Ingestion**  
    - Daemon’s HTTP handler (`ingestHandler`) receives the POST, deserializes the JSON, and passes it to `tracking.HandleIngest`.
    - `tracking.HandleIngest` writes the event into `tracking.db` (events table) and updates benchmark aggregates (via `upsertAgentSummary` inside a transaction).

5. **Storage**  
   - `tracking.db` stores every raw event (append‑only, WAL mode for concurrent readers/writers).
   - `benchmark.db` holds pre‑aggregated summaries used by the weekly benchmark engine (updated incrementally as events arrive).

6. **Weekly Benchmark**  
    - At the configured time (default: Sundays 02:00 local), the daemon triggers the benchmark engine. An intraweek run can be triggered manually via `F5` in the **Benchmark Detailed** tab.
    - Engine reads aggregates from `benchmark.db`, computes per‑model scores (accuracy, latency_p95, tool_rate, cost, quality), normalizes them against the min/max observed across all models in the window, applies weights, and calculates delta vs. baseline.
    - **Active model lookup**: at run time, the runner reads `~/.config/opencode/opencode.json` to determine which model is currently configured for each agent. That model's row receives `run_status = 'active'`; all others in the same cycle receive `run_status = 'superseded'`. The full provider-prefixed name is stored in `raw_model`; table displays use the normalized (prefix-stripped) name.
    - **Cross-cycle superseding**: `MarkSupersededRuns()` updates previous `active` rows to `superseded` when the agent's active model has changed since the last cycle.
    - Benchmark discovery excludes effectively error-only agents to avoid placeholder rows like `opencode/unknown` and `INSUFFICIENT_DATA`.
    - Result: a verdict (`KEEP`, `SWITCH`, or `INSUFFICIENT_DATA`) per model, plus an `active_model` recommendation written to `~/.metronous/thresholds.json`.

7. **Presentation**  
    - TUI Dashboard reads `tracking.db` (for real-time event stream and daily cost charts) and `benchmark.db`/`thresholds.json` (for the Benchmark History Summary, Benchmark Detailed, and Charts tabs).
   - `metronous report` CLI prints formatted tables from the same sources.
   - User reviews the recommendation in the dashboard or CLI report and manually decides whether to change the active model in OpenCode.

---

## Systemd Service Details

- **Service File Location**: `~/.config/systemd/user/metronous.service` (created by `metronous install`)
- **Unit File Contents** (simplified):
  ```ini
  [Unit]
  Description=Metronous Agent Intelligence Daemon
  After=network.target

  [Service]
  Type=simple
  ExecStart=/path/to/metronous server --data-dir /home/user/.metronous/data --daemon-mode
  Restart=on-failure
  RestartSec=5s
  StandardOutput=append:/home/user/.metronous/data/metronous.log
  StandardError=inherit

  [Install]
  WantedBy=default.target
  ```
- **Key Points**:
  - Runs as **user‑level** service (no `sudo` required).
  - `--daemon-mode` flag tells the daemon to use `ServeDaemon()` (HTTP‑only, blocks on context, not stdin).
  - `Restart=on-failure` + `RestartSec=5s` provides basic resilience against crashes.
  - Logs go to `~/.metronous/data/metronous.log` (append‑only) and `stderr` (inherited by systemd, viewable via `journalctl --user -u metronous`).

---

## Failure Handling & Resilience

| Failure Point | Detection | Mitigation |
|---------------|-----------|------------|
| **Shim cannot read port file** | `readShimPort()` returns error | Shim enters `ensureDaemonRunning`: acquires file lock, checks again, starts daemon if needed. |
| **Port file exists but daemon dead** | Shim’s health check (`GET /health`) fails (connection refused, timeout, non‑200) | Shim deletes stale port file, proceeds to start a new daemon via `ensureDaemonRunning`. |
| **Daemon fails to start** (e.g., bad binary, missing data dir) | `systemctl --user status metronous` shows `failed`; journal shows error | User must fix underlying issue (e.g., reinstall binary, ensure `~/.metronous/data` exists and is writable). Systemd will retry per `Restart=on-failure`. |
| **SQLite disk full or corruption** | Daemon logs error on insert/aggregation; may return HTTP 500 on `/ingest` | Manual intervention required: free space, restore from backup, or delete and let databases recreate (losing historical data). |
| **Systemd user instance not running** | `systemctl --user` commands fail | Ensure systemd user services are available on the host. On WSL this means enabling `systemd=true` in `/etc/wsl.conf` and restarting WSL. |
| **Network issues between shim and daemon** (unlikely on localhost) | Shim HTTP requests timeout or fail | Shim treats as daemon-unhealthy, deletes port file, attempts restart. |
| **Too many OpenCode sessions** (many shims) | Each shim opens a HTTP connection to daemon; daemon’s HTTP server has limited concurrent capacity | Daemon’s `http.Server` uses default `MaxHeaderBytes` etc.; under extreme load may see latency or dropped requests. Consider raising daemon’s HTTP timeouts or using a reverse proxy if needed (not currently required for typical usage). |

---

## Directory Layout

```
~/.metronous/
├── data/
│   ├── tracking.db          # Raw event stream (WAL mode)
│   ├── benchmark.db         # Pre‑aggregated summaries for benchmarking
│   ├── mcp.port             # Current daemon port
│   └── metronous.pid        # PID of the daemon (if running)
└── thresholds.json          # User‑editable config: active model, weights, thresholds, model prices
```

The CLI and TUI read/write these files directly when possible; the daemon also holds them open while running.

---

## Extensibility

### Adding a New Tool (e.g., `report`)
1. Define the tool’s argument struct and handler function in `internal/mcp/` (similar to `ingestHandler`).
2. Register the handler in `daemon/service.go` via `mcp.RegisterReportHandler(srv, handler)`.
3. Expose the tool in the shim’s `tools/list` response (currently hardcoded to only `ingest`; to make it dynamic, the shim would need to fetch `/tools` from the daemon—planned future work).
4. Update the OpenCode plugin to know about the new tool (if you want it to invoke it from the agent side; otherwise the daemon can call it internally based on events).

### Adding a New Metric to Benchmarking
1. Add a column to the `benchmark_runs` table (or a new summary table) in `internal/store/sqlite/benchmark_store.go`.
2. Update the aggregation logic (`AggregateRun`) to compute the metric from raw events.
3. Extend the normalization and scoring pipeline in `internal/benchmark/engine.go`:
   - Add a normalization function for the new metric (higher/lower is better).
   - Add a weight in `thresholds.json` under `weights`.
   - Ensure the score is included in `final_score`.
4. (Optional) Add a user‑editable price or threshold if the metric requires it (e.g., cost per token already modeled).

### Changing Storage Backend (e.g., to Postgres)
- Replace the `internal/store/sqlite/` implementations with versions that use `database/sql` and the desired driver.
- The interface (`internal/store/store.go`) is already abstracted (`EventStore`, `BenchmarkStore`), so the swap is confined to the store implementations.
- Note: The TUI and CLI currently read the SQLite files directly; switching to Postgres would require changing those to query over HTTP or a socket. For now, SQLite is kept for simplicity and zero‑config local operation.

---

## Sequence Diagram (Textual)

```
OpenCode Plugin          Shim (metronous mcp)        Daemon (metronous)          SQLite DBs
     |                          |                          |                           |
     |--- MCP tools/call ingest --->|                          |                           |
     |                          |--- HTTP POST /ingest --->|                           |
     |                          |                          |--- HandleIngest --> tracking.db
     |                          |                          |--- upsertAgentSummary --> benchmark.db
     |                          |<--- HTTP 200 OK ---------|                           |
     |<--- MCP tools/call resp ---|                          |                           |
     |                          |                          |                           |
     | (event loop continues)   |                          |                           |
```

**Benchmark Trigger (internal to daemon, weekly):**

```
Daemon (internal timer)          Benchmark Engine          SQLite DBs           thresholds.json
     |                              |                           |                           |
     |--- trigger benchmark ------->|--- read aggregates ------->|                           |
     |                              |--- compute scores -------->|                           |
     |                              |--- apply weights ---------->|                           |
     |                              |--- calculate delta -------->|                           |
     |                              |--- decide verdict ---------->|                           |
     |                              |<--- write active_model ----|                           |
     |                              |                           |                           |
     |<--- benchmark complete -----|                           |                           |
```

---

## Closing Notes

This architecture delivers:
- **Zero‑friction Linux installation** (`curl -fsSL https://github.com/kiosvantra/metronous/releases/latest/download/install.sh | bash` → running daemon + configured OpenCode).
- **Shared local daemon**: OpenCode sessions on the same machine talk to the same daemon via the shim.
- **Observability**: logs, SQLite files, and systemd status give full visibility into operation.
- **Simplicity**: few moving parts, no external dependencies beyond Go and SQLite (already vendored).

For any questions about extending or adapting this architecture, please refer to the source code or open a discussion in the repository.
