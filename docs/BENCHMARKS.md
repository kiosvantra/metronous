# Benchmark Methodology

This document describes exactly how Metronous computes metrics, assigns verdicts, and calculates the composite scores displayed in the TUI. Everything here is derived directly from the source code.

---

## Table of Contents

- [Minimum Sample Size](#minimum-sample-size)
- [Metric Computation](#metric-computation)
  - [Accuracy](#accuracy)
  - [Turn Latency](#turn-latency)
  - [Token Counts](#token-counts)
  - [Cost Per Session](#cost-per-session)
  - [ROI Score](#roi-score)
- [Verdict Logic](#verdict-logic)
  - [INSUFFICIENT_DATA](#insufficient_data)
  - [URGENT_SWITCH](#urgent_switch)
  - [SWITCH](#switch)
  - [KEEP](#keep)
  - [ROI suppression rules](#roi-suppression-rules)
  - [Model recommendation](#model-recommendation)
- [Health Score](#health-score)
- [Responsibility Score](#responsibility-score)
- [Config Thresholds](#config-thresholds)
  - [Active fields (TUI Config tab)](#active-fields-tui-config-tab)
  - [Urgent triggers](#urgent-triggers)
  - [Per-agent overrides](#per-agent-overrides)
  - [Model pricing](#model-pricing)
- [Benchmark Run Types](#benchmark-run-types)
- [Active Model Determination](#active-model-determination)
- [Run Status](#run-status)
- [Per-model evaluation](#per-model-evaluation)
- [Best alternative model selection](#best-alternative-model-selection)
- [Verdict Trend](#verdict-trend)
- [Time Display](#time-display)
- [Raw Model Field](#raw-model-field)
- [Deprecated fields](#deprecated-fields)

---

## Minimum Sample Size

```go
const MinSampleSize = 50  // internal/benchmark/metrics.go
```

Any agent/model pair with fewer than 50 events in the evaluation window receives `INSUFFICIENT_DATA`. All metric computations are still performed; the verdict is forced regardless of the actual values.

---

## Metric Computation

All metrics are computed by `AggregateMetrics()` in `internal/benchmark/fetcher.go`.

### Accuracy

```
Accuracy = (total_events - error_events) / total_events
```

- `total_events` = all events in the window (any type)
- `error_events` = events with `event_type == "error"`
- Range: [0.0, 1.0]. Returns 0 if total_events is 0.

```go
// internal/benchmark/metrics.go
func CalculateAccuracy(completed, total int) float64 {
    if total == 0 { return 0 }
    return float64(completed) / float64(total)
}
```

### Turn Latency

Turn latency is derived **exclusively from `complete` events** with `duration_ms > 0`.

- `tool_call` events always have `duration_ms == 0` and are excluded.
- `AvgTurnMs` = arithmetic mean of all complete event durations
- `P50TurnMs`, `P95TurnMs`, `P99TurnMs` = nearest-rank percentiles (floor-based index)

```go
// internal/benchmark/metrics.go — percentile index formula
idx := rank * n / 100
if idx > 0 && rank*n%100 == 0 { idx-- }
```

Note: `duration_ms` on `complete` events is the wall-clock time from the beginning of the session to the completion event, not a per-call LLM latency. It is useful as a relative comparison but should not be interpreted as strict per-request latency.

### Token Counts

`AvgPromptTokens` and `AvgCompletionTokens` are means computed over complete events only:

```
AvgPromptTokens = sum(prompt_tokens on complete events) / count(complete events with tokens)
```

### Cost Per Session

Cost is tracked as the **maximum `cost_usd` value per distinct `session_id`**, then summed across all sessions in the window:

```
TotalCostUSD = sum over sessions of MAX(cost_usd) per session
```

This is correct because the plugin emits `cost_usd` as the **accumulated** session cost at the time of each event. Taking the max per session recovers the final cost without double-counting intermediate snapshots.

```
CostPerSession = TotalCostUSD / SessionCount
```

Events with no `session_id` and non-zero cost are dropped with a warning (they cannot be attributed to a session).

### ROI Score

```
ROI = Accuracy / CostPerSession
```

- Measures accurate output per dollar spent
- Returns 0 when `CostPerSession == 0` (no billing data available)
- The old formula used `ToolSuccessRate / CostPerSession` but `ToolSuccessRate` is always 1.0 in practice, so it was replaced by `Accuracy` which carries real signal

```go
// internal/benchmark/fetcher.go
if costPerSession > 0 {
    m.ROIScore = m.Accuracy / costPerSession
}
```

---

## Verdict Logic

Verdicts are assigned by `EvaluateRulesWithPricing()` in `internal/decision/verdict.go`. Rules are checked in this exact order:

### INSUFFICIENT_DATA

```
if SampleSize < 50 → INSUFFICIENT_DATA
```

Checked first. No other rule fires.

### URGENT_SWITCH

```
if Accuracy < urgent.MinAccuracy (default 0.60) → URGENT_SWITCH
if ErrorRate > urgent.MaxErrorRate (default 0.30) → URGENT_SWITCH
```

Urgent triggers are checked before soft thresholds. Any single urgent condition triggers `URGENT_SWITCH`.

### SWITCH

```
if Accuracy < thresholds.MinAccuracy (default 0.85) → SWITCH
if ROI_active AND ROIScore < thresholds.MinROIScore (default 0.05) → SWITCH
```

Latency is intentionally excluded from SWITCH triggers. The `duration_ms` field reflects cumulative session time, not per-call latency, and is too noisy to use as a threshold trigger.

### KEEP

```
if none of the above triggered → KEEP
```

### ROI suppression rules

ROI is excluded from decision evaluation when either condition holds:

1. **Free model**: the model is listed in `model_pricing` with `price == 0`
2. **Unreliable cost data**: `TotalCostUSD == 0` (no billing events were collected)

```go
// internal/decision/verdict.go
func roiActive(model string, m benchmark.WindowMetrics, thresholds *config.Thresholds) bool {
    if thresholds.IsModelFree(model) { return false }
    if m.TotalCostUSD == 0 { return false }
    return true
}
```

When ROI is suppressed, the reason string shows `roi=N/A (free model)` or `roi=N/A (no billing data)`.

### Model recommendation

When the verdict is `SWITCH` or `URGENT_SWITCH`, the engine first tries `bestAlternativeModel()` to find a better model from **real benchmark data in the same window**. If none is found, it falls back to config-based recommendations:

- **Accuracy failure** → `model_recommendations.accuracy_model` (default: `claude-opus-4-5`)
- **ROI failure** → `model_recommendations.performance_model` (default: `claude-haiku-4-5`)
- **Fallback** → `model_recommendations.default_model` (default: `claude-sonnet-4-5`)

---

## Health Score

The health score is a composite 0–100 value displayed in the Benchmark History Summary tab. It combines three signals:

```
HealthScore = AccuracyPart + VerdictPart + ROIPart
```

| Component | Weight | Formula |
|-----------|--------|---------|
| **Accuracy** | 60 pts | `accuracy * 60` |
| **Verdict** | 0–25 pts | KEEP=25, INSUFFICIENT_DATA=10, SWITCH=5, URGENT_SWITCH=0 |
| **ROI** | 0–15 pts | `15 * min(1, roiScore / minROIScore)` — neutral 7 pts when no cost data |

```go
// internal/tui/benchmark_summary_view.go
func computeHealthScore(accuracy, _ float64, verdict store.VerdictType, roiScore, minROIScore float64) float64 {
    accPart     := accuracy * 60
    verdictPart := // see table above
    roiPart     := // 7 neutral, or 15 * min(1, roi/minROI)
    return clamp(accPart + verdictPart + roiPart, 0, 100)
}
```

Latency is **excluded** from the health score for the same reason it is excluded from SWITCH triggers: `p95_latency_ms` currently reflects cumulative session time, not per-call latency.

**Color coding**:
- `>= 80` → green
- `>= 50` → yellow
- `< 50`  → red

---

## Responsibility Score

The Responsibility Score appears in the Charts tab "Responsibility Top 3" card. It measures a model's health contribution weighted by the **business importance of the agents** that use it.

```
ResponsibilityScore = sum(HealthScore(run) * agentWeight(run.AgentID) * run.SampleSize)
                    / sum(run.SampleSize)
```

Agent weights are defined in `internal/tui/charts_view.go`:

| Agent | Weight |
|-------|--------|
| `sdd-orchestrator` | 1.00 |
| `sdd-apply` | 0.98 |
| `sdd-verify` | 0.96 |
| `sdd-explore` | 0.94 |
| `sdd-design` | 0.92 |
| `sdd-spec` | 0.90 |
| `sdd-propose` | 0.88 |
| `sdd-tasks` | 0.87 |
| `sdd-archive` | 0.86 |
| `sdd-init` | 0.85 |
| Other `sdd-*` | 0.90 |
| `build`, `plan`, `general`, `explore` | 0.80 |
| All others | 0.75 |

A model with high health on `sdd-orchestrator` and `sdd-apply` (top-weight agents) scores higher than one with identical health concentrated on archival agents.

When `roleWeightSum == 0` (no benchmark runs with sufficient data), `ResponsibilityScore = HealthScore * 0.75`.

---

## Config Thresholds

Thresholds are stored in `~/.metronous/thresholds.json` and loaded by the daemon on startup.

### Active fields (TUI Config tab)

These three fields are editable via the Config tab (`5`):

| Field | JSON key | Default | Description |
|-------|----------|---------|-------------|
| **Min Accuracy** | `defaults.min_accuracy` | `0.85` | Accuracy below this → `SWITCH` |
| **Min ROI Score** | `defaults.min_roi_score` | `0.05` | ROI below this → `SWITCH` (paid models only) |
| **Max Cost/Session** | `defaults.max_cost_usd_per_session` | `0.50` | Reference for cost semaphore color in Tracking tab; spike detection base |

### Urgent triggers

These are not exposed in the Config tab but are present in `thresholds.json`:

| Field | JSON key | Default | Description |
|-------|----------|---------|-------------|
| Urgent min accuracy | `urgent_triggers.min_accuracy` | `0.60` | Below this → `URGENT_SWITCH` |
| Max error rate | `urgent_triggers.max_error_rate` | `0.30` | Above this → `URGENT_SWITCH` |
| Max cost spike multiplier | `urgent_triggers.max_cost_spike_multiplier` | `3.0` | Used for cost spike color threshold in Tracking tab |

### Per-agent overrides

Any threshold in `defaults` can be overridden per agent under `per_agent.<agentID>`:

```json
{
  "per_agent": {
    "sdd-verify": {
      "min_accuracy": 0.95
    }
  }
}
```

Only non-zero fields override the default; missing fields inherit from `defaults`.

### Model pricing

The `model_pricing.models` map lists model output prices per 1M tokens. A value of `0.0` marks a model as free; absent models are treated as paid.

```json
{
  "model_pricing": {
    "models": {
      "gemma-2-9b-free": 0.0,
      "opencode/claude-sonnet-4-6": 15.0
    }
  }
}
```

Free models skip ROI and cost checks in the decision engine. They can still receive `SWITCH` or `URGENT_SWITCH` verdicts based on accuracy or error rate.

---

## Benchmark Run Types

| Type | Trigger | Window |
|------|---------|--------|
| `weekly` | Monday 02:00 local (cron `"0 0 2 * * 1"`) | Last 7 days from `now` |
| `intraweek` | `F5` in **Benchmark Detailed** tab | From `last_run_at + 1ms` to `now` (falls back to 7 days if no prior run) |

Both types use the same `Runner.run()` implementation and produce identical `BenchmarkRun` rows in `benchmark.db`, tagged with `run_kind`. Intraweek runs are useful for getting up-to-date metrics mid-week without waiting for the Sunday schedule.

---

---

## Active Model Determination

At the time each benchmark run executes, the runner determines which model is currently active for each agent by reading `~/.config/opencode/opencode.json`.

- The model configured for the agent in `opencode.json` at benchmark time is marked `run_status = 'active'`.
- All other models benchmarked in the same cycle (i.e., models that appeared in the event window but are no longer configured) receive `run_status = 'superseded'`.
- **Fallback**: if the agent is not found in `opencode.json` (e.g., a custom agent not declared there), the runner falls back to a heuristic: the model with the most events in the evaluation window is treated as the active model.

This per-run determination is performed at write time: the active model is stamped into the `benchmark_runs` row as it is saved.

### Cross-cycle superseding (`MarkSupersededRuns`)

When a new run completes for a given agent, the runner also calls `MarkSupersededRuns`: any previous `active` run for that agent whose model differs from the newly active model is updated to `run_status = 'superseded'`. This ensures that across cycles, only the run that reflects the current configured model carries `active` status.

---

## Run Status

Each row in `benchmark_runs` has a `run_status` field with one of two values:

| Value | Meaning |
|-------|---------|
| `active` | This model is currently configured for the agent in `opencode.json`. Its metrics are used for verdict display, health score, and the Benchmark History Summary view. |
| `superseded` | This model was active in a previous cycle but has since been replaced. It is shown in the TUI with a `—` in the verdict column and does not drive SWITCH/KEEP decisions. |

**TUI display implications**:
- In the **Benchmark History Summary** tab, the `●` marker indicates the active model row. Verdict is shown only for that row; superseded rows show `—`.
- In the **Benchmark Detailed** tab, superseded rows are labeled `CHANGED` in the status column to indicate that the agent has since moved to a different model.

---

## Per-model evaluation

Each benchmark run evaluates every **distinct model** used by an agent separately. Events are grouped by `NormalizeModelName(e.Model)` before metric aggregation. This means:

- `opencode/claude-sonnet-4-6` and `opencode/claude-haiku-4-5` produce separate rows for the same agent
- If an agent switched models mid-window, both models are evaluated independently
- The **Benchmark History Summary** tab shows one row per `(agent, model)` pair, but only for pairs active in the last 4 weekly cycles

---

## Verdict Trend

The **verdict trend** shows the last 5 verdicts for a given (agent, model) pair, computed from **weekly runs only** (`run_kind = 'weekly'`). Intraweek runs are excluded from the trend to avoid distorting it with mid-cycle snapshots.

- The trend is displayed as a sequence of verdict symbols (e.g., `K K K S K` for KEEP/SWITCH).
- `CHANGED` appears in the trend when two consecutive active runs for the same agent used **different models**. This signals that the agent switched models between cycles.
- Only runs where `run_status = 'active'` contribute to the trend; superseded runs are excluded.

---

## Time Display

All response time values in the TUI are displayed in **humanized format** rather than raw milliseconds or seconds. The format adapts to the magnitude of the duration:

| Duration range | Format example |
|----------------|----------------|
| ≥ 1 hour | `5h 11m` |
| ≥ 1 minute | `24m 15s` |
| ≥ 1 second | `42.3s` |
| < 1 second | `850ms` |

This applies to all columns showing average or percentile turn times (e.g., `Avg`, `P95`) in both the Benchmark History Summary and Benchmark Detailed tabs.

---

## Raw Model Field

Each `benchmark_runs` row stores a `raw_model` field that preserves the full provider-prefixed model name as emitted by the plugin (e.g., `opencode/claude-sonnet-4-6`).

- The `raw_model` value is shown verbatim in the **Decision Rationale** panel (the detail panel in Benchmark Detailed tab) to give exact model identification.
- Table columns in both benchmark tabs show the **normalized** model name (prefix stripped, e.g., `claude-sonnet-4-6`) for readability.
- Normalization is performed by `NormalizeModelName()`, which strips the provider prefix (everything up to and including the last `/`) from the model string.

---

## Best alternative model selection

When a SWITCH or URGENT_SWITCH verdict is issued, the runner looks for a better model **within the same agent's current window data** before falling back to config-based recommendations.

Selection criteria (priority order):

1. **Accuracy first**: candidate must have `accuracy > current - 0.001`
2. **ROI second**: among equal accuracy, prefer higher `ROIScore`
3. **Speed third**: among equal accuracy and ROI, prefer lower `AvgTurnMs`

The candidate must also have `SampleSize >= 50` and must not itself be `URGENT_SWITCH`.

```go
// internal/runner/runner.go
func bestAlternativeModel(currentModel string, current benchmark.WindowMetrics, perModel map[string]modelMetrics) string
```

---

## Deprecated fields

The following fields exist in `WindowMetrics` and `BenchmarkRun` for backward compatibility but carry no new information:

| Field | Status | Note |
|-------|--------|------|
| `ToolSuccessRate` | Deprecated | Always 1.0 in practice; excluded from SWITCH triggers |
| `AvgQuality` | Deprecated | `quality_score` is rarely emitted; has no influence on verdicts |
| `AvgLatencyMs` | Deprecated alias | Populated from `AvgTurnMs` for old runs |
| `P50LatencyMs` / `P95LatencyMs` / `P99LatencyMs` | Deprecated aliases | Populated from turn percentiles |
| `completedSegmentsCost` | Dead (plugin) | Never written; kept for struct compatibility |
| `lastStepCost` | Dead (plugin) | Never written; kept for struct compatibility |
| `MaxLatencyP95Ms` | Inactive threshold | Present in `DefaultThresholds` but not used as a SWITCH trigger |
| `MinToolSuccessRate` | Inactive threshold | Present in `DefaultThresholds` but not used as a SWITCH trigger |
