# TUI Controls and Navigation

Metronous runs a five-tab terminal dashboard (TUI):

| # | Tab |
|---|-----|
| 1 | Benchmark History Summary |
| 2 | Benchmark Detailed |
| 3 | Tracking |
| 4 | Charts |
| 5 | Config |

## Global keys (app level)
- `q`: quit
- `1`/`2`/`3`/`4`/`5` or `left`/`right`: switch tabs (note: inside **Charts**, `left`/`right` navigate months instead of switching tabs)
- `ctrl+s`: save config
- `ctrl+r`: reload config

## Tracking tab
- `up`/`down` or `k`/`j`: move the session cursor (sessions are shown collapsed)
- `Enter`: open the session popup (popup content is frozen at open time)
- `Esc`: close the popup
- Popup navigation:
  - `up`/`down` or `k`/`j`: move within the popup viewport
  - `PgUp`/`PgDn`: scroll the popup by blocks of 20 rows

## Benchmark History Summary tab

This tab shows a weighted historical view of all (agent, model) pairs that have been active in the **last 4 weekly cycles**. It is designed for at-a-glance health monitoring across all agents.

### Display layout

- **Cascade sort**: for each agent, the currently-active model appears first (marked with `●`). Historical models that were active in previous weekly cycles are listed below it, sorted by recency.
- **4-cycle filter**: only (agent, model) pairs that appear in at least one of the last 4 weekly benchmark runs are shown. Older models are hidden.
- **Verdict column**: verdict (`KEEP`, `SWITCH`, `URGENT_SWITCH`, `INSUFFICIENT_DATA`) is displayed **only** for the active model row of each agent. Historical (superseded) model rows show a `—` in the verdict column to avoid misleading comparisons.
- **Legend line**: a legend at the bottom of the table explains the weighting scheme.

### Legend explanation

```
Weighted historical averages (weekly + intraweek) — showing models active in the last 4 weekly cycles
```

Metrics shown (accuracy, avg response time, cost, health score) are **weighted averages** across all benchmark runs for that (agent, model) pair. Recent weekly runs receive higher weight than older ones; intraweek runs contribute proportionally to their sample size within the same cycle.

### Keys
- `up`/`down` or `k`/`j`: move the cursor between rows
- `F5`: trigger an intraweek benchmark run immediately (same as the weekly pipeline, but covers only events since the last run)

## Benchmark Detailed tab
- `up`/`down` or `k`/`j`: select a row (an agent run)
- `PgUp`/`PgDn`: change the displayed cycle (Sunday-bounded week; navigation goes newest <-> older)
- `Enter`: freeze the detail panel for the selected row
- `Esc`: unfreeze the detail panel
- `F5`: trigger an intraweek benchmark run immediately (covers events since the last run, using the same pipeline as the weekly run)

## Charts tab
- The Charts tab renders a main monthly cost chart plus two summary cards: Performance Top 3 of the Month and Responsibility Top 3 of the Month
- `left`/`right`: change the selected month
- `k`/`l`: move the day cursor within the cost chart only (updates the tooltip)
- Mouse: hover/click on a day column to show the tooltip (terminal-dependent)
