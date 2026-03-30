# How Metronous Works

Metronous is a local telemetry, benchmarking, and model calibration system for AI agents built on top of the Gentle AI SDD (Spec-Driven Development) framework. Its goal is to help teams make data‑driven decisions about which language models to use for their agents, balancing accuracy, latency, tool usage, and cost.

This document explains the **methodology** behind Metronous: what data it collects, how it aggregates and scores models, and how it arrives at actionable SWITCH/KEEP verdicts.

---

## Overview of the Data Flow

```
[OpenCode Agent] → (MCP via metronous mcp shim) → [Metronous Daemon (systemd service)]
                                                                   ↓
                                                         [SQLite Databases]
                                                                   ↓
                                                      [Benchmark Engine (weekly)]
                                                                   ↓
                                                   [Metrics, Scores, Verdicts]
                                                                   ↓
                                             [TUI Dashboard & CLI Reports]
```

1. **Telemetry Ingestion**  
   Every time an agent invokes a tool (or starts/ends a session), the OpenCode plugin captures:
   - `agent_id`, `session_id`
   - `event_type` (`start`, `tool_call`, `complete`, `error`, etc.)
   - `model` name (as configured in `opencode.json`)
   - `timestamp`
   - `input_tokens`, `output_tokens` (from the LLM provider)
   - `cost_usd` (derived from token counts × model price)
   - `quality_score` (optional, from the agent’s own validation)
   - Arbitrary payload (e.g., tool arguments, result)

   This payload is sent as an MCP `tools/call ingest` request to the **metronous mcp shim**, which forwards it via HTTP to the long‑running **Metronous daemon** (a systemd user service). The daemon writes the event into two SQLite databases:
   - `tracking.db` – raw event stream (for the TUI Tracking tab and ad‑hoc queries)
   - `benchmark.db` – pre‑aggregated data used by the weekly benchmark engine

2. **Storage Schema (Simplified)**  
   ```sql
   -- tracking.db.events
   id INTEGER PRIMARY KEY,
   agent_id TEXT NOT NULL,
   session_id TEXT NOT NULL,
   event_type TEXT NOT NULL,
   model TEXT NOT NULL,
   timestamp DATETIME NOT NULL,
   input_tokens INTEGER,
   output_tokens INTEGER,
   cost_usd REAL,
   quality_score REAL,
   payload JSON
   ```

   The `benchmark.db` contains summary tables (`agent_summaries`, `benchmark_runs`) that are updated incrementally as new events arrive.

3. **Weekly Benchmark Pipeline**  
   By default, Metronous runs a benchmark analysis every Sunday at 02:00 local time (configurable via the TUI or environment variable). The pipeline consists of four stages:

   ### a. Data Collection  
   For each model seen in the selected time window (default: last 7 days), gather:
   - Total number of events (`N`)
   - Sum of input and output tokens
   - Sum of `cost_usd`
   - Count of events with a non‑null `quality_score`
   - Sum of `quality_score` (only over events where it is present)
   - Latency measurements (end‑to‑end duration of agent sessions or tool calls, depending on configuration)
   - Tool usage rate (fraction of events that are `tool_call`)
   - Outcome metrics: proportion of `KEEP` vs `SWITCH` veredicts emitted by the agent (if applicable)

   ### b. Normalization  
   Each raw metric is converted to a **score in [0, 1]** so that disparate units can be combined.  
   - For metrics where **higher is better** (accuracy, tool usage rate, quality score):  
     `score = (value − min) / (max − min)`  
   - For metrics where **lower is better** (latency, cost):  
     `score = (max − value) / (max − min)`  

   `min` and `max` are computed **across all models evaluated in the same window**. This makes the scoring *relative*: a model’s score reflects how it performs compared to the other models tested recently, not against an absolute ideal.

   Special case: if all models have the same value for a metric (max = min), every model receives a score of `0.5` (the midpoint) to avoid division by zero.

   ### c. Weighted Scoring  
   Each model receives a final score:
   ```
   final_score =
       w_acc   * score_accuracy   +
       w_lat   * score_latency    +
       w_tool  * score_tool_rate  +
       w_cost  * score_cost       +
       w_qual  * score_quality    (if quality_score is tracked)
   ```
   The weights (`w_*`) are defined in `internal/config/thresholds.json` under the `weights` key. They must sum to 1.0 (the system will renormalize if they don’t, but it’s best to keep them normalized).  
   Example weights from the default configuration:
   ```json
   "weights": {
     "accuracy": 0.40,
     "latency":  0.30,
     "tool":     0.10,
     "cost":     0.10,
     "quality":  0.10
   }
   ```

   Because the cost score is `1.0` for any free model (price = 0 → min cost = 0 → score_cost = 1.0), free models automatically receive the full benefit of the `w_cost` term. This is intentional: Metronous should favor cheaper alternatives when other factors are equal.

   ### d. Decision Thresholds  
   The system does not declare a SWITCH simply because one model has a higher score. It requires a **minimum improvement** to avoid flapping on negligible differences.

   - Let `S_base` be the score of the currently active model (the “baseline” – usually the model whose `command` appears in `opencode.json` under `mcpServers.metronous.command`).
   - Let `S_cand` be the score of the candidate model being evaluated.
   - Compute `delta = S_cand − S_base`.

   Then:
   - If `delta > switch_threshold` → **SWITCH** to the candidate model.
   - If `delta < −keep_threshold` → **KEEP** the baseline (the candidate is significantly worse).
   - Otherwise → **INSUFFICIENT_DATA** (or effectively KEEP if the UI treats it as such).  
     Typical defaults: `switch_threshold = 0.05`, `keep_threshold = 0.03`.

   These thresholds are also configurable in `thresholds.json`.

4. **Verdict Propagation**  
   The benchmark engine writes the winning model (or the directive to keep the current one) into `~/.metronous/thresholds.json` under the `active_model` key.  
   The TUI and CLI read this file to display the active recommendation.  
   OpenCode itself does **not** automatically switch models; it continues to use whatever `command` is listed in its `opencode.json`.  
   The intended workflow is:
   1. Wait for the weekly benchmark to run (or trigger it manually with `metronous benchmark --model <name>`).
   2. Observe the verdict in the TUI (Benchmark tab) or via `metronous report`.
   3. If the verdict is `SWITCH`, manually update `opencode.json` (or run `metronous install` again, which will preserve the active model choice) to point to the new model.
   4. Restart OpenCode (or reload its MCP server) to start using the new model.

   This manual step ensures that teams retain control and can verify the change in a staging environment before rolling it out to production.

5. **Why This Methodology Works**  
   - **Robustness to Noise**: By aggregating over a window and normalizing relative to peers, Metronous filters out day‑to‑day jitter (e.g., temporary network latency, varying prompt complexity).  
   - **Actionable Trade‑offs**: The weighted sum forces an explicit consideration of accuracy vs. speed vs. cost vs. tool usage—trade‑offs that teams already make intuitively but now see quantified.  
   - **Cost‑Awareness**: Free models are not punished; they receive the maximum possible score on the cost dimension, making it easy to spot zero‑cost alternatives when they are comparable in other regards.  
   - **Low Operational Overhead**: Once installed as a systemd service, Metronous runs in the background with virtually no maintenance. The only periodic action is checking the benchmark verdict and updating `opencode.json` if a SWITCH is advised.  
   - **Extensibility**: New metrics (e.g., carbon footprint, safety scores) can be added by extending the event schema, adding a normalization rule, and assigning a weight—without changing the core pipeline logic.

6. **Limitations & Design Trade‑offs**  
   - **Window Granularity**: The default weekly window may be too slow for teams that want to react to changes within a day. The window can be shortened (e.g., to 24h) via configuration, but this increases variance in the scores.  
   - **Linear Weighting Assumption**: The model assumes metrics contribute independently and linearly to overall utility. Real‑world utility may have interactions (e.g., accuracy below a certain threshold causes catastrophic failures regardless of low cost). Teams with strong domain knowledge can adjust weights or add custom rules outside Metronous.  
   - **No Causal Inference**: Metronous observes correlation, not causation. A SWITCH verdict could be driven by a concurrent change in prompts, retrieval pipeline, or other environmental factors. For high‑stakes decisions, teams should run a controlled A/B test or manually verify the change before rolling out broadly.  
   - **Reliance on Accurate Token Counting**: Cost calculations depend on the token counts reported by the LLM provider. If a provider under‑ or over‑reports tokens, the cost dimension will be skewed. Most major providers are accurate, but it’s worth auditing if cost estimates seem off.  
   - **Quality Score Sparsity**: Many events (especially `tool_call` or `start`) do not have a `quality_score`. The system only averages quality over events where it is present; if quality is rarely reported, this dimension has little influence. Teams should ensure their agents emit a quality signal (e.g., self‑assessment, unit test pass/fail) for the metric to be meaningful.

   - **Web/Mobile CSP Limitation**: The OpenCode web and mobile interfaces are subject to browser Content Security Policy (CSP) restrictions that block connections to non-same-origin endpoints (e.g., `http://localhost:<port>` or `http://<IP>:<port>`). By default, OpenCode’s CSP only allows connections to '`self`' and '`data:`', preventing the MCP shim (`metronous mcp`) from reaching the Metronous daemon even when the daemon is listening on `0.0.0.0`. To use web/mobile interfaces, you must adjust OpenCode’s CSP (via environment variables or source modification) to include your daemon’s IP/port, or deploy a reverse proxy to make the daemon appear same-origin. This is a client-side limitation, not a Metronous daemon issue.
7. **How to Verify or Tweak the Methodology**  
   - **Inspect Raw Data**: `sqlite3 ~/.metronous/data/tracking.db "SELECT * FROM events ORDER BY timestamp DESC LIMIT 20;"`  
   - **Check Aggregates**: `sqlite3 ~/.metronous/data/benchmark.dump` or use the CLI: `metronous benchmark --debug` (prints intermediate normalizations, weights, and delta).  
   - **Adjust Weights/Thresholds**: Edit `~/.metronous/thresholds.json` (fields `weights`, `switch_threshold`, `keep_threshold`). Changes take effect on the next benchmark run.  
   - **Change Benchmark Window**: Set the environment variable `METRONOUS_BENCHMARK_WINDOW_DAYS=3` before running `metronous benchmark` or modify the daemon’s config.  
   - **Run Manual Benchmark**: `metronous benchmark --model gemma-2-9b-free --days 14` to evaluate a specific model over a custom window.

8. **Bottom Line**  
   Metronous trades off some analytical sophistication (e.g., no time‑series forecasting, no causal models) for **simplicity, transparency, and low operational friction**. Its methodology is deliberately chosen to give teams a clear, actionable signal: *Is there a model that is meaningfully better overall, or should we stay with what we have?*  

   By focusing on relative performance within a recent window, normalizing across dimensions, and applying thoughtful decision thresholds, Metronous provides a reliable compass for navigating the fast‑moving landscape of language models—without demanding constant attention or expert statistical knowledge from its users.
