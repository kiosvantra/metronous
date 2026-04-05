# SDD Proposal: Cost Forecasting & Budget Alerts

**Date**: April 4, 2026  
**Feature**: Cost Forecasting & Budget Alerts for metronous  
**Status**: Proposal (awaiting review)

---

## Intent

Metronous tracks costs across AI models and agents, but provides no visibility into future spending or budget management. Teams lack tools to predict cost trends, set spending limits, or receive alerts before budget overruns occur. This feature addresses three critical gaps:

1. **Cost Prediction**: Forecast spending for the next 7-30 days based on historical trends, enabling proactive budget planning.
2. **Budget Enforcement**: Define hard limits (per-agent, per-model, or global) with clear alerts when approaching thresholds.
3. **Cost Awareness**: Surface cost trends in the Charts tab with visual indicators and TUI alerts, making spending patterns visible at a glance.

This reduces surprise bills, enables cost optimization decisions, and improves team financial visibility across AI infrastructure.

---

## Scope

### Features (What Users Will See)

- **Budget Limits Configuration**: Set budgets via `thresholds.json` for global, per-agent, or per-model costs (daily, weekly, monthly).
- **Cost Forecast Overlay**: New section in Charts tab showing 7/14/30-day forecasts with confidence bands (upper/lower bounds).
- **Budget Alerts**: TUI banners (top of dashboard) when cost is trending toward or has exceeded budget limits; also logged to `metronous.log`.
- **Forecast Accuracy Metrics**: Historical forecast vs actual comparison for users to evaluate forecast quality.
- **Cost Breakdown by Agent**: Forecast chart broken down by agent to identify which agents are driving high costs.

### Affected Modules

| Module | Changes |
|--------|---------|
| `internal/store` | 3 new tables: `daily_cost_snapshots`, `cost_forecasts`, `budget_alerts` |
| `internal/runner` | Weekly forecast generation task (in benchmark pipeline) |
| `internal/tui` | Charts tab extended with overlay panel for forecasts |
| `internal/config` | New fields in `thresholds.json`: `budget_limits`, `forecasting`, `alerts` |
| `internal/cli` | New command: `metronous config budgets [get|set]` to manage limits |
| `internal/decision` | Optional: cost-based model recommendations (lower-cost alternatives when over budget) |

### Out of Scope

- Email/Slack notifications (alerts display in TUI only; logging provided for external systems).
- Multi-tenant budget isolation (all budgets are per local instance).
- Automatic cost reduction (e.g., switching models when budget exceeded).
- Historical forecasting accuracy reports (tracked but not visualized in v1).
- Advanced forecasting methods beyond EMA (Holt-Winters available as upgrade path).

### Dependencies

- **VividCortex/ewma**: Go library for Exponential Moving Average computation.
- **jthomperoo/holtwinters**: Optional upgrade for advanced seasonal forecasting (deferred to v2).
- **Existing QueryDailyCostByModel**: Foundation for daily snapshots; no changes to signature.

---

## Approach

### Schema Design

#### 1. `daily_cost_snapshots` (core data table)
Stores daily cost totals computed from events. Populated by runner at end of day.

```sql
CREATE TABLE daily_cost_snapshots (
  id INTEGER PRIMARY KEY,
  date DATE UNIQUE NOT NULL,                 -- Date [since, until) = [date, date+1day)
  total_cost_usd DECIMAL(12, 4) NOT NULL,    -- Total cost across all models/agents
  model_breakdown TEXT NOT NULL,             -- JSON: {"gpt-4": 12.34, "claude-3": 5.67}
  agent_breakdown TEXT NOT NULL,             -- JSON: {"agent-a": 8.00, "agent-b": 10.01}
  snapshot_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

**Rationale**: Denormalized breakdowns avoid repeated aggregation queries; JSON allows flexible dimensionality.

#### 2. `cost_forecasts` (prediction results)
Stores computed forecasts. Regenerated weekly (every Monday, 00:00 UTC).

```sql
CREATE TABLE cost_forecasts (
  id INTEGER PRIMARY KEY,
  forecast_date DATE NOT NULL,               -- Date this forecast was computed
  forecast_horizon INTEGER NOT NULL,         -- Days ahead: 7, 14, or 30
  forecast_type TEXT NOT NULL,               -- 'ema' or 'holtwinters' (v2)
  target_date DATE NOT NULL,                 -- The date being forecast
  predicted_cost_usd DECIMAL(12, 4) NOT NULL,
  confidence_lower_usd DECIMAL(12, 4),       -- 80% confidence band lower bound
  confidence_upper_usd DECIMAL(12, 4),       -- 80% confidence band upper bound
  model_forecasts TEXT,                      -- JSON: {"gpt-4": {...}, "claude-3": {...}}
  actual_cost_usd DECIMAL(12, 4),            -- Populated after target_date passes
  error_pct DECIMAL(5, 2),                   -- |actual - predicted| / actual * 100
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(forecast_date, forecast_horizon, target_date)
);
```

**Rationale**: Confidence bands enable risk-aware budget planning; error tracking improves forecasting over time; separate model forecasts for breakdown visibility.

#### 3. `budget_alerts` (alert log)
Audit trail of budget limit violations and warnings. Used for alert deduplication.

```sql
CREATE TABLE budget_alerts (
  id INTEGER PRIMARY KEY,
  alert_type TEXT NOT NULL,                  -- 'warning' (80%), 'critical' (95%), 'exceeded'
  scope TEXT NOT NULL,                       -- 'global', 'agent:{name}', 'model:{name}'
  budget_limit_usd DECIMAL(12, 4) NOT NULL,
  current_cost_usd DECIMAL(12, 4) NOT NULL,
  forecast_cost_usd DECIMAL(12, 4),          -- NULL if actual overspend
  alert_date DATE NOT NULL,                  -- Date alert was triggered
  period TEXT NOT NULL,                      -- 'daily', 'weekly', 'monthly'
  dismissed_at DATETIME,                     -- NULL until user acknowledges
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_alert_date_scope (alert_date, scope)
);
```

**Rationale**: Supports alert deduplication (don't spam same alert), dismissal tracking, and audit trails for cost reviews.

### Algorithm Choice: Exponential Moving Average (EMA)

**Selected**: EMA with α (alpha) = 0.3  
**Rationale**:
- **Simplicity**: Single parameter, easy to tune; no seasonal/trend components to model separately.
- **Responsiveness**: α=0.3 gives 70% weight to recent data, 30% to historical; captures trend shifts quickly.
- **Robustness**: Works well with sparse data (e.g., 2-3 weeks of history); doesn't require minimum data.
- **Real-time computation**: O(1) updates; no batch retraining needed.
- **Explainability**: Non-technical users can understand "forecast based on recent trend".

**Why not Holt-Winters?** Requires 4-6 weeks of data (2 seasonal cycles) and is overkill for initial feature; added as v2 upgrade path with `forecast_type` column ready.

**Confidence Bands**: 80% confidence interval computed as:
```
std_dev = sqrt(sum((actual[i] - forecast[i])^2) / n)
lower = forecast - 1.28 * std_dev   # 80th percentile z-score
upper = forecast + 1.28 * std_dev
```

### Integration Flow

```
┌─────────────────────────────────────────────────────────────────┐
│ Runner (Weekly, Monday 00:00 UTC)                               │
│ ───────────────────────────────────────────────────────────────│
│ 1. Fetch daily snapshots for last 30 days via QueryDailyCostByModel
│ 2. Compute EMA forecasts for 7/14/30 day horizons (store in cost_forecasts)
│ 3. Check budget limits against forecasts; log violations to budget_alerts
│ 4. Trigger TUI alert banner for critical budgets
└──────────────┬──────────────────────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────────────────────┐
│ TUI Charts Tab (On-demand refresh, manual or auto-scroll)        │
│ ──────────────────────────────────────────────────────────────── │
│ • Main view: Historical cost trend (last 30 days as bars/lines) │
│ • Overlay panel: 7/14/30-day forecast with confidence bands     │
│ • Agent breakdown: Forecast costs per agent below main chart    │
│ • Alert banner: "Warning: Global budget trending 85% → $5K"    │
└──────────────────────────────────────────────────────────────────┘
```

### Configuration Model

Add to `thresholds.json`:

```json
{
  "budgets": {
    "enabled": true,
    "global": {
      "daily": 500.00,
      "weekly": 3000.00,
      "monthly": 10000.00
    },
    "agents": {
      "researcher-bot": {
        "daily": 100.00,
        "monthly": 2000.00
      },
      "api-tester": {
        "daily": 50.00
      }
    }
  },
  "forecasting": {
    "enabled": true,
    "method": "ema",
    "alpha": 0.3,
    "min_data_points": 10,
    "forecast_horizons": [7, 14, 30],
    "regenerate_schedule": "0 0 * * 1"  // Cron: Monday 00:00 UTC
  },
  "alerts": {
    "enabled": true,
    "warning_threshold_pct": 80,
    "critical_threshold_pct": 95,
    "deduplicate_hours": 24,
    "show_in_tui": true,
    "log_to_file": true
  }
}
```

### Data Lifecycle

- **daily_cost_snapshots**: Retain 90 days; auto-delete older. Refreshed nightly by runner.
- **cost_forecasts**: Retain 365 days; auto-delete older. Regenerated weekly.
- **budget_alerts**: Retain 180 days; auto-delete older. Kept for audit and trend analysis.

---

## Trade-offs

### 1. EMA vs Holt-Winters

| Factor | EMA | Holt-Winters |
|--------|-----|--------------|
| Data requirement | ≥5 points | ≥20-30 points (2 seasons) |
| Accuracy (mature data) | 85-90% MAPE | 92-95% MAPE |
| Complexity | Low (1 param) | High (3 params) |
| Tuning effort | Minimal | Significant |
| Implementation time | 2 hours | 2 days (via library) |
| **Decision** | ✅ **Selected for v1** | Upgrade path for v2 |

**Rationale**: Users deploying metronous may have only 2-3 weeks of cost data. EMA works immediately; Holt-Winters requires patience and tuning.

### 2. UI Placement: Overlay vs New Tab vs Sidebar

| Option | Pros | Cons | **Decision** |
|--------|------|------|----------|
| **New Tab** | Clear separation, dedicated UX | Clutters navigation; adds cognitive load | ❌ No |
| **Sidebar widget** | Always visible; passive awareness | Reduces chart space; may distract | ❌ No |
| **Charts tab overlay** | Integrates with existing cost data; toggle on/off | Slightly cramped at small terminal widths | ✅ **Selected** |

**Rationale**: Charts tab already shows historical costs; forecast overlay is a natural extension. Toggling forecast on/off (via keyboard shortcut) gives users choice without tab explosion.

### 3. Alert Frequency & Deduplication

| Strategy | Frequency | Pros | Cons |
|----------|-----------|------|------|
| **Per-update** | Every forecast run (weekly) | Accurate | Alert fatigue; noise |
| **Per-day** | Once per calendar day | Reasonable | May miss critical spikes |
| **Per-week** | Once per week | Quiet | Delayed notification |
| **Manual only** | User views dashboard | No spam | Easy to miss alerts |

**Decision**: Hybrid approach:
- Compute alerts at forecast time (weekly).
- Display in TUI immediately (banner, dismissible).
- Deduplicate by scope + budget: don't re-alert same budget on same day unless severity changes (e.g., warning → critical).
- Log all to `budget_alerts` table for audit.

**Rationale**: Weekly forecast + dismissible TUI banner + logging = alert visibility without spam.

### 4. Per-Agent vs Global Budgets

| Approach | Pros | Cons | **Decision** |
|----------|------|------|----------|
| **Global only** | Simple; single limit | No control per team/agent | ❌ No |
| **Per-agent only** | Fine-grained control | Complex config; hard to enforce global limits | ❌ No |
| **Hierarchical (global + per-agent)** | Flexible; supports both | More code; config validation needed | ✅ **Selected** |

**Rationale**: Global budget = safety net; per-agent = team accountability. Config schema supports both; runner checks both and alerts on either violation.

### 5. Real-time vs Batch Forecasting

| Approach | Latency | Cost | Accuracy | **Decision** |
|----------|---------|------|----------|----------|
| **Real-time** | Immediate (on-demand) | O(1) per forecast | Stale until next run | ❌ No |
| **Batch weekly** | Up to 7 days stale | O(n) once/week | Fresh after run | ✅ **Selected** |
| **Batch daily** | Up to 1 day stale | O(n) daily | Better freshness | ⚠️ Phase 2 |

**Rationale**: Weekly is sufficient for budget planning (costs don't change minute-to-minute). Daily batch can be added in Phase 2 if users request higher freshness.

---

## Rollback Plan

### Safe Deletion Strategy

The feature is designed to fail gracefully if tables don't exist:

1. **Migration versioning**: Each table creation is a separate migration with version number (e.g., `001_add_cost_snapshots.sql`).
2. **Soft disable**: Config flag `budgets.enabled` and `forecasting.enabled` allow disabling without code changes.
3. **Fallback behavior**:
   - If `cost_forecasts` table missing: Charts tab shows historical data only (no forecast overlay).
   - If `budget_alerts` table missing: No budget violations logged, but runner continues normally.
   - If `daily_cost_snapshots` table missing: Forecasting disabled, but historical QueryDailyCostByModel still works.

### Reverting Implementation

**Step 1: Disable in config** (no downtime):
```json
{
  "budgets": { "enabled": false },
  "forecasting": { "enabled": false },
  "alerts": { "enabled": false }
}
```

**Step 2: Drop tables** (offline):
```bash
sqlite3 ~/.metronous/data/tracking.db << EOF
DROP TABLE budget_alerts;
DROP TABLE cost_forecasts;
DROP TABLE daily_cost_snapshots;
EOF
```

**Step 3: Verify**: Restart metronous; confirm Charts tab shows historical data only.

### Migration Compatibility

- **Up**: Create tables in sequence; runner checks for existence before populating.
- **Down**: Drop tables in reverse order; no schema mutation of existing tables.
- **No breaking changes**: Existing `events`, `benchmarks` tables untouched; `QueryDailyCostByModel` unmodified.

---

## Risks & Mitigation

### Risk 1: Data Sparsity (Forecast Unreliability)

**Problem**: New deployments may have only 2-3 days of cost data; EMA forecasts on sparse data are noise.

**Mitigation**:
- Require minimum 10 data points before generating any forecast (config: `forecasting.min_data_points`).
- Widen confidence bands for sparse data: confidence = 50% for <10 points, 80% for ≥20 points.
- Display warning in Charts: "Forecast requires 10+ days of data; currently X days available."
- In runner: skip forecast generation and log warning if threshold not met.

**Testing**: Table-driven tests with 1/5/10/30 data points; verify forecast is not generated or is marked as low-confidence.

### Risk 2: False Budget Alerts (False Positives)

**Problem**: Cost variations (weekend zero-cost days) could trigger spurious "budget exceeded" alerts.

**Mitigation**:
- Use confidence upper bound (not point estimate) for budget checks: `if actual > budget OR forecast_upper > budget, alert`.
- Implement alert deduplication: don't re-trigger same alert (scope + budget) within 24 hours unless severity increases.
- Distinguish alert types: "warning" (80% confidence exceeded), "critical" (95%), "exceeded" (actual overspend).
- Log all decisions to `budget_alerts` table with `dismissed_at` field; users can review false positives.

**Testing**: Mock cost histories with weekend spikes; verify alert count is <5 per month per budget.

### Risk 3: Alert Fatigue

**Problem**: Too many alerts → users ignore them.

**Mitigation**:
- Batch alerts: one TUI banner per run (not per budget).
- Deduplication window: 24 hours (don't repeat same alert).
- Dismissible: user can close banner; logged but not re-shown until next day.
- Config knob: `alerts.deduplicate_hours` allows site-specific tuning.

**Success metric**: <5 alerts/day/user in normal operation (test with synthetic data).

### Risk 4: Model Price Changes

**Problem**: Mid-month price changes (e.g., GPT-4 pricing update) break historical trend.

**Mitigation**:
- Document in thresholds.json: when updating model costs, note the change date.
- Runner: check for cost data after price change date; if <5 points, don't forecast.
- Option (v2): manually reset forecast state on price change.

**Mitigation is procedural, not technical**: Rely on user awareness (changelog notes price updates).

### Risk 5: Forecast Drift Over Time

**Problem**: EMA α=0.3 may systematically under/over-predict if trend changes (e.g., new high-cost model adopted).

**Mitigation**:
- Track forecast error (actual - predicted) in `cost_forecasts.error_pct`.
- Dashboard metric: "Forecast MAPE last 30 days: 12%" (visible to user).
- If MAPE >25% for >10 consecutive forecasts, suggest alpha tuning or disabling forecasting.
- v2: Auto-tune alpha or switch to Holt-Winters if detection triggers.

**Testing**: Simulate trend changes (introduce new model at mid-stream); verify alert still works and error grows gracefully.

### Risk 6: Performance Impact (Forecast Computation)

**Problem**: EMA computation + DB writes on large historical datasets could slow runner or TUI refresh.

**Mitigation**:
- EMA computation: O(n) single pass over 30 days of data = <100ms on typical machines.
- DB inserts: batch 3 forecasts (7/14/30 days) + 5 alerts per run = minimal I/O.
- Caching: cache last 30 days of snapshots in memory during forecast run; avoid repeated queries.
- Benchmark: `go test -bench=BenchmarkForecastGeneration` ensures <500ms even with 1 year of data.

**Testing**: Performance test with synthetic 1-year cost history; target <1s end-to-end.

---

## Effort Estimate

### Phase Breakdown (4-6 weeks total, 1 developer)

| Phase | Tasks | Hours | Dependencies |
|-------|-------|-------|--------------|
| **Phase 1: Schema & Migrations** | Create 3 tables, write migrations, test rollback. | 8 | None |
| **Phase 2: EMA & Store Ops** | Implement EMA, add store methods (InsertSnapshot, QueryForecasts), unit tests. | 16 | Phase 1 |
| **Phase 3: Runner Integration** | Weekly forecast task, alert generation, logging, end-to-end test. | 12 | Phase 2 |
| **Phase 4: TUI Charts Overlay** | Extend Charts tab, add forecast panel, key binding for toggle, UX refinement. | 16 | Phase 3 |
| **Phase 5: Config & CLI** | Add thresholds.json fields, config validation, CLI commands (get/set budgets), docs. | 12 | Phase 3 |
| **Phase 6: Testing & Docs** | Integration tests, performance benchmarks, user guide, edge case coverage. | 12 | All above |

**Total**: 76 hours ≈ 2.2 weeks @ 40h/week, or ≈ 4 weeks @ 20h/week (accounting for review/revisions/integration).

**Critical Path**:
```
Phase 1 → Phase 2 → Phase 3 ──┬→ Phase 4 ─┐
                   └──→ Phase 5 ─┴→ Phase 6
```

Phases 4 and 5 can run in parallel after Phase 3; Phase 6 (testing/docs) is critical path tail.

---

## Success Criteria

### Quantitative Metrics

| Metric | Target | How to Measure |
|--------|--------|---|
| **Forecast Accuracy (MAPE)** | <15% over 30-day horizon | Run against 90 days of actual data; compare `error_pct` in `cost_forecasts` |
| **False Positive Rate** | <5% (≤1 spurious alert per 20 real) | Manual audit of `budget_alerts` table over 2-week period |
| **Computation Time** | <500ms per forecast run | Benchmark test with 1-year history; time runner weekly task |
| **Query Latency** (Charts overlay) | <200ms refresh | Time `SELECT * FROM cost_forecasts` queries; cache if needed |
| **User Adoption** | ≥50% enable budgeting | Survey/poll users after 2 weeks; check `budgets.enabled` in configs |
| **Alert Dismissal Rate** | ≥80% dismissed in <24h | Log dismissal events; measure time from alert to dismissal |

### Qualitative Metrics

- **Usability**: Users can set budgets and understand forecasts in <5 minutes (validate in user interview).
- **Trustworthiness**: Forecast is frequently accurate enough users reference it in budget planning meetings.
- **Documentation**: All new config fields, CLI commands, and algorithms documented in README and inline code comments.

### Success Stories (Post-Launch)

1. **User avoids budget overrun**: Sets global budget of $10K/month; forecast alerts them at $8K; they optimize agents and stay under budget.
2. **Cost breakdown adoption**: Per-agent budgets drive accountability; engineering team notices high-cost agents and migrates to cheaper models.
3. **Forecast-driven decisions**: Team uses 30-day forecast to plan sprint scope: "We have $500 budget left, we can run 10 more benchmark rounds."

---

## Implementation Checklist (For Next Phase: Spec Writing)

- [ ] Define exact SQL schemas with indexes and constraints.
- [ ] Specify EMA algorithm with worked examples and edge case handling.
- [ ] Define alert generation logic (which budgets trigger, how severity is determined).
- [ ] Specify TUI Charts overlay UX (key bindings, rendering).
- [ ] Define CLI commands and config validation rules.
- [ ] List all test scenarios (sparse data, price changes, alert dedup, performance).
- [ ] Document rollback procedure and migration safety properties.

---

## References

- **EMA reference**: VividCortex/ewma Go library documentation.
- **Confidence intervals**: https://en.wikipedia.org/wiki/Prediction_interval#Univariate_case
- **Cost forecasting best practices**: AWS Cost Anomaly Detection (inspiration for alert dedup strategy).
- **metronous existing code**: `internal/store/sqlite/event_store.go` (QueryDailyCostByModel), `internal/tui/charts_view.go` (Charts tab).

---

**Next Steps**: Await review feedback. Once approved, proceed to Spec writing phase for detailed requirement specifications and test scenarios.
