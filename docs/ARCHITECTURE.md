# Architecture

This document is the technical deep-dive into Metronous internals: component boundaries, data flows, the plugin state machine, benchmark pipeline, and decision engine rules.

For benchmark methodology and scoring formulas see [BENCHMARKS.md](BENCHMARKS.md).

---

## Table of Contents

- [Component Overview](#component-overview)
- [Component Diagram](#component-diagram)
- [Communication Protocols](#communication-protocols)
- [Data Flow: Event Ingestion](#data-flow-event-ingestion)
- [Data Flow: Benchmark Pipeline](#data-flow-benchmark-pipeline)
- [Plugin State Machine](#plugin-state-machine)
- [Plugin: Cost Tracking](#plugin-cost-tracking)
- [TUI Tab Layout](#tui-tab-layout)
- [Daemon Lifecycle](#daemon-lifecycle)
- [Scheduler](#scheduler)
- [SQLite Schema (simplified)](#sqlite-schema-simplified)
- [Directory Layout](#directory-layout)
- [Failure Handling](#failure-handling)
- [Extensibility](#extensibility)

---

## Component Overview

| Component | Language | Responsibility |
|-----------|----------|----------------|
| `metronous-plugin.ts` | TypeScript | OpenCode plugin; captures agent events and forwards them to the daemon via HTTP POST |
| `metronous mcp` (shim) | Go | stdio↔HTTP bridge launched by OpenCode as an MCP server; translates MCP messages to HTTP calls |
| `metronous server --daemon-mode` | Go | Long-lived background daemon (systemd/launchd/SCM); ingests events, stores in SQLite, runs weekly benchmarks |
| `internal/tracking/` | Go | HTTP ingest handler + async write queue |
| `internal/store/sqlite/` | Go | SQLite implementations of `EventStore` and `BenchmarkStore` |
| `internal/benchmark/` | Go | Metric computation from raw events (`AggregateMetrics`) |
| `internal/decision/` | Go | Threshold evaluation and verdict assignment |
| `internal/runner/` | Go | Orchestrates the benchmark pipeline (weekly and intraweek) |
| `internal/scheduler/` | Go | cron-based weekly job scheduler embedded in the daemon |
| `internal/tui/` | Go | Bubble Tea 5-tab terminal UI |
| `cmd/metronous/` | Go | CLI entrypoint (Cobra commands: install, dashboard, service, …) |

---

## Component Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│  OpenCode process                                                     │
│                                                                       │
│  ┌──────────────────────────┐   HTTP POST /ingest                    │
│  │  metronous-plugin.ts     │──────────────────────────────────┐     │
│  │  (OpenCode plugin)       │                                  │     │
│  └──────────────────────────┘                                  │     │
│                                                                 │     │
│  ┌──────────────────────────┐   MCP stdio   ┌──────────────┐  │     │
│  │  OpenCode (MCP client)   │◄─────────────►│ metronous mcp│  │     │
│  └──────────────────────────┘               │ (shim)       │──┘     │
│                                             └──────────────┘         │
└─────────────────────────────────────────────────────────────────────┘
                                       │ HTTP POST /ingest
                                       ▼
                          ┌────────────────────────┐
                          │  metronous daemon       │
                          │  (systemd user service) │
                          │                         │
                          │  ┌───────────────────┐  │
                          │  │ tracking.IngestHandler│
                          │  │ → EventQueue        │  │
                          │  └────────┬──────────┘  │
                          │           │ async write  │
                          │  ┌────────▼──────────┐  │
                          │  │  tracking.db      │  │
                          │  │  (SQLite WAL)     │  │
                          │  └───────────────────┘  │
                          │                         │
                          │  ┌───────────────────┐  │
                          │  │  benchmark.db     │  │
                          │  │  (SQLite WAL)     │  │
                          │  └─────────▲─────────┘  │
                          │            │             │
                          │  ┌─────────┴─────────┐  │
                          │  │  Scheduler (cron)  │  │
                          │  │  Monday 02:00      │  │
                          │  │  Runner → Engine   │  │
                          │  └───────────────────┘  │
                          └────────────────────────┘
                                       │
                          ┌────────────▼────────────┐
                          │  metronous dashboard     │
                          │  (TUI, reads SQLite)     │
                          └─────────────────────────┘
```

---

## Communication Protocols

### Plugin → Daemon (HTTP)

The plugin sends events directly to the daemon via HTTP POST, bypassing the MCP shim entirely. The daemon's port is read from `~/.metronous/data/mcp.port`.

- **Endpoint**: `POST http://127.0.0.1:<port>/ingest`
- **Body**: JSON payload with `agent_id`, `session_id`, `event_type`, `model`, `timestamp`, and optional cost/token fields
- **Reconnection**: on `ECONNREFUSED`, the plugin re-reads `mcp.port` and retries up to 3 times with 500ms backoff
- **Pre-ready queue**: events emitted before the daemon is ready are buffered (max 500) and flushed once the port file appears

### Shim → Daemon (HTTP, for MCP-originated calls)

The `metronous mcp` shim translates MCP `tools/call ingest` messages from OpenCode into HTTP POST requests to the same `/ingest` endpoint.

### TUI / CLI → SQLite (direct)

The TUI and CLI read both SQLite files directly (no HTTP layer). The daemon keeps the files open in WAL mode, so concurrent reads from the TUI are safe.

---

## Data Flow: Event Ingestion

```
1. OpenCode fires an event (chat.message, tool.execute.after, event hook)
2. metronous-plugin.ts constructs a JSON payload
3. callIngest() → httpPost() → POST http://127.0.0.1:<port>/ingest
4. Daemon HTTP handler → tracking.HandleIngest()
5. validateIngestRequest() validates required fields (agent_id, session_id, event_type, model, timestamp)
6. toStoreEvent() converts to store.Event
7. EventQueue.Enqueue() buffers event for async write
8. EventQueue background goroutine → EventStore.InsertEvent() → tracking.db
```

**Valid event types**: `start`, `tool_call`, `retry`, `complete`, `error`

---

## Data Flow: Benchmark Pipeline

```
Trigger (weekly cron or F5 intraweek from Benchmark Detailed tab)
    │
    ▼
Runner.run()
    │
    ├── discoverAgents() ─── QueryEvents() filtered to non-error events
    │
    ├── readActiveModelFromConfig() ─── reads ~/.config/opencode/opencode.json
    │       determines currently configured model per agent
    │
    └── for each agentID:
            │
            ├── FetchEventsForWindow() ─── all events in [start, end)
            │
            ├── group by normalized model name
            │
            └── for each (agentID, model):
                    │
                    ├── AggregateMetrics()
                    │       Accuracy, ErrorRate, AvgTurnMs, P50/P95/P99,
                    │       PromptTokens, CompletionTokens, TotalCostUSD,
                    │       SessionCount, ROIScore
                    │
                    ├── assignRunStatus()
                    │       model == activeModel → run_status = 'active'
                    │       otherwise           → run_status = 'superseded'
                    │       (fallback: highest event count if agent not in config)
                    │
                    ├── DecisionEngine.Evaluate()
                    │       EvaluateRulesWithPricing() → VerdictType
                    │       BuildReasonWithPricing() → human reason string
                    │       recommendModel() → suggested replacement
                    │
                    └── bestAlternativeModel()
                            selects a better model from current window data
                            (accuracy-first, then ROI, then avg turn time)

After all agents:
    ├── MarkSupersededRuns() → updates prior 'active' rows to 'superseded'
    │       when the active model has changed since the previous cycle
    ├── GenerateArtifact() → ~/.metronous/artifacts/<timestamp>.json
    └── BenchmarkStore.SaveRun() → benchmark.db (one row per agentID/model pair)
```

---

## Plugin State Machine

The plugin maintains a `sessions` Map from `sessionID → SessionState`. Sessions are created lazily on first event and retained indefinitely (the map resets on OpenCode restart).

```
Event received
    │
    ├── chat.message / chat.params
    │       ├── Resolve agentId (METRONOUS_AGENT_ID > chat.agent > "opencode")
    │       ├── Build model string ("providerID/modelID")
    │       ├── getOrCreateSession() — restores cost from session_costs.json if new
    │       ├── Update state.model if currently "unknown" and new model has "/"
    │       ├── Update state.lastActiveModel only if new model contains "/" (provider prefix)
    │       └── If new session → callIngest({ event_type: "start" })
    │
    ├── tool.execute.after
    │       ├── Increment toolCalls / successfulToolCalls / errors
    │       ├── Rework detection: same tool within 60s → reworkCount++
    │       └── callIngest({ event_type: "tool_call", cost_usd: state.totalCostUsd })
    │           (cost_usd here is a lagging snapshot from the last step-finish)
    │
    └── event hook
            ├── message.part.updated (step-finish)
            │       ├── state.totalCostUsd += max(0, part.cost)  ← per-call cost
            │       ├── state.promptTokens += part.tokens.input
            │       ├── state.completionTokens += part.tokens.output
            │       └── saveCostCache(sessionId, totalCostUsd) → session_costs.json
            │
            ├── session.idle
            │       ├── Snapshot durationMs = Date.now() - startTime
            │       ├── saveCostCache()
            │       ├── calculateQualityProxy() (penalizes failures, rework, errors)
            │       ├── callIngest({ event_type: "complete", cost_usd: totalCostUsd })
            │       └── state.lastIdleAt = now (kept in memory, not evicted)
            │
            └── session.error
                    ├── state.errors++
                    └── callIngest({ event_type: "error" })
```

### Dead fields in SessionState

`completedSegmentsCost` and `lastStepCost` are retained for struct compatibility but are never written to or read from in active code. Cost is tracked exclusively via `totalCostUsd` (accumulated from step-finish deltas).

---

## Plugin: Cost Tracking

The plugin computes session cost by accumulating the `cost` field from `step-finish` parts delivered via `message.part.updated`:

```typescript
state.totalCostUsd += Math.max(0, part.cost)  // per-LLM-call cost, not cumulative
```

This matches provider billing because each `step-finish` reports the cost of one LLM request.

**Cost cache** (`session_costs.json`):
- Loaded at plugin startup; restores `totalCostUsd` for sessions active before a restart
- Written synchronously on each `step-finish` and on `session.idle`
- Non-finite values are rejected at both read and write time

**Model selection rule**: `lastActiveModel` is only updated when the model string contains a `/` (provider prefix). This prevents bare model names like `claude-sonnet-4-6` from overwriting a fully-qualified `opencode/claude-sonnet-4-6` already recorded.

---

## TUI Tab Layout

The TUI uses Bubble Tea and is composed of five sub-models rendered as numbered tabs:

| # | Tab Name | Model | File |
|---|----------|-------|------|
| 1 | **Benchmark History Summary** | `BenchmarkSummaryModel` | `benchmark_summary_view.go` |
| 2 | **Benchmark Detailed** | `BenchmarkDetailedModel` | `benchmark_view.go` |
| 3 | **Tracking** | `TrackingModel` | `tracking_view.go` |
| 4 | **Charts** | `ChartsModel` | `charts_view.go` |
| 5 | **Config** | `ConfigModel` | `config_view.go` |

Tab switching is handled by `app.go`. All tabs except Config refresh on a 2-second tick from the SQLite stores.

**Config reload propagation**: when the Config tab saves thresholds, it emits `ConfigReloadedMsg`. Both `BenchmarkSummaryModel` and `ChartsModel` subscribe to this message and update their `minROI` field so health and responsibility scores are recalculated with the new threshold immediately.

---

## Daemon Lifecycle

```
metronous server --daemon-mode
    │
    ├── os.MkdirAll(DataDir)
    ├── sqlite.NewEventStore(tracking.db)  ← WAL mode
    ├── sqlite.NewBenchmarkStore(benchmark.db)  ← WAL mode
    ├── loadThresholds() from ~/.metronous/thresholds.json (defaults on error)
    ├── decision.NewDecisionEngine(&thresholds)
    ├── runner.NewRunner(es, bs, engine, artifactDir)
    ├── scheduler.NewSchedulerWithContext(ctx, runner, 7, logger)
    │       └── RegisterWeeklyJob("0 0 2 * * 1")  ← Monday 02:00 local
    ├── tracking.NewEventQueue(es, DefaultBufferSize)  ← async write buffer
    ├── mcp.NewStdioServer()  ← also starts HTTP server on dynamic port
    │       └── writes port to mcp.port
    └── srv.ServeDaemon(ctx)  ← blocks until ctx cancelled

On shutdown (SIGTERM / service stop):
    └── ctx cancelled → scheduler stops → queue drains → WAL checkpoint → DBs close
```

The daemon runs as a **systemd user service** on Linux:
- Unit file: `~/.config/systemd/user/metronous.service`
- Enabled with `--user` scope; starts on login, kept alive with lingering
- Restart policy: `Restart=on-failure`, `RestartSec=5s`
- Logs: `~/.metronous/data/metronous.log` + `journalctl --user -u metronous`

---

## Scheduler

The weekly benchmark scheduler (`internal/scheduler/cron.go`) is embedded directly in the daemon process using `robfig/cron/v3` with second-precision parsing.

- **Schedule**: `"0 0 2 * * 1"` — Monday at 02:00 local time
- **Window**: last 7 days (`DefaultWindowDays = 7`)
- **Intraweek (F5)**: `Runner.RunIntraweek()` derives the window start from `max(run_at)` of all previous benchmark runs, so only events since the last run are evaluated
- **Cancellation**: the scheduler context is derived from the daemon context; in-progress jobs observe cancellation on daemon shutdown

---

## SQLite Schema (simplified)

### tracking.db — events table

```sql
id               TEXT PRIMARY KEY,
agent_id         TEXT NOT NULL,
session_id       TEXT NOT NULL,
event_type       TEXT NOT NULL,  -- start | tool_call | retry | complete | error
model            TEXT NOT NULL,
timestamp        DATETIME NOT NULL,
duration_ms      INTEGER,         -- complete events only; tool_call always 0
prompt_tokens    INTEGER,         -- complete events only
completion_tokens INTEGER,        -- complete events only
cost_usd         REAL,            -- cumulative per session at time of event
quality_score    REAL,
rework_count     INTEGER,
tool_name        TEXT,
tool_success     BOOLEAN,
metadata         JSON
```

### benchmark.db — benchmark_runs table

```sql
run_at            DATETIME NOT NULL,
agent_id          TEXT NOT NULL,
model             TEXT NOT NULL,   -- normalized model name (provider prefix stripped)
raw_model         TEXT,            -- full provider-prefixed name (e.g. opencode/claude-sonnet-4-6)
run_kind          TEXT,            -- weekly | intraweek
run_status        TEXT,            -- active | superseded
window_days       INTEGER,
window_start      DATETIME,
window_end        DATETIME,
accuracy          REAL,
avg_latency_ms    REAL,            -- deprecated alias for avg_turn_ms
p50_latency_ms    REAL,
p95_latency_ms    REAL,
p99_latency_ms    REAL,
avg_turn_ms       REAL,            -- mean turn duration (complete events only)
p95_turn_ms       REAL,
tool_success_rate REAL,            -- always 1.0 in practice
roi_score         REAL,
total_cost_usd    REAL,
sample_size       INTEGER,
verdict           TEXT,            -- KEEP | SWITCH | URGENT_SWITCH | INSUFFICIENT_DATA
recommended_model TEXT,
decision_reason   TEXT,
artifact_path     TEXT
```

---

## Directory Layout

```
~/.metronous/
├── data/
│   ├── tracking.db          # Raw event stream (WAL mode)
│   ├── benchmark.db         # Benchmark run history (WAL mode)
│   ├── mcp.port             # Dynamic HTTP port (runtime)
│   ├── metronous.pid        # Daemon PID (runtime)
│   ├── session_costs.json   # Plugin cost cache (survives restarts)
│   └── plugin.log           # Plugin debug log (METRONOUS_DEBUG=true)
├── artifacts/
│   └── <timestamp>.json     # Decision artifact per benchmark run
└── thresholds.json          # User-editable thresholds config
```

---

## Failure Handling

| Failure | Detection | Mitigation |
|---------|-----------|------------|
| Port file missing at plugin startup | `readPortFile()` returns null | `waitForServer()` polls for 30s; events buffered in `preReadyQueue` (max 500) |
| Daemon restarted mid-session | `ECONNREFUSED` on HTTP POST | Plugin re-reads `mcp.port` and retries up to 3 times with 500ms backoff |
| Plugin restart mid-session | Session not in memory | `getOrCreateSession()` restores cost from `session_costs.json` |
| Daemon crash | systemd `Restart=on-failure` | Daemon restarts within 5s; plugin reconnects on next event |
| SQLite disk full | Store returns error on insert | Queue logs error; event is dropped; daemon continues |
| Non-finite cost in step-finish | `Number.isFinite()` check | Silently clamped to 0; error logged to `plugin.log` |
| All events are errors for an agent | `discoverAgents()` filters out error-only agents | Agent is excluded from benchmark; no misleading `INSUFFICIENT_DATA` row |

---

## Extensibility

### Adding a new tracked metric

1. Add a column to `tracking.db` events table in `internal/store/sqlite/event_store.go`
2. Add the field to `store.Event` struct in `internal/store/store.go`
3. Emit the field from the plugin (`metronous-plugin.ts`)
4. Handle it in `tracking.validateIngestRequest()` and `toStoreEvent()`
5. Consume it in `benchmark.AggregateMetrics()` and expose via `WindowMetrics`

### Switching storage backend

Replace `internal/store/sqlite/` with an implementation of the `store.EventStore` and `store.BenchmarkStore` interfaces. The TUI and CLI also read SQLite directly; those paths would need to query via HTTP or a new interface.

### Adding a new TUI tab

1. Create a new Bubble Tea sub-model implementing `Init()`, `Update()`, `View()`
2. Register it in `internal/tui/app.go` alongside the existing tab switch logic
3. Wire it to the appropriate store via `NewXxxModel(es, bs)`
