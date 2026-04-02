# TUI Controls and Navigation

Metronous runs a five-tab terminal dashboard (TUI): Tracking, Benchmark Summary, Benchmark Detailed, Charts, and Config.

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

## Benchmark Summary tab
- `up`/`down` or `k`/`j`: move the cursor between rows (agents)
- (This tab is aggregated; it does not provide per-cycle navigation.)

## Benchmark Detailed tab
- `up`/`down` or `k`/`j`: select a row (an agent run)
- `PgUp`/`PgDn`: change the displayed cycle (Sunday-bounded week; navigation goes newest <-> older)
- `Enter`: freeze the detail panel for the selected row
- `Esc`: unfreeze the detail panel

## Charts tab
- `m`: toggle between Performance and Responsibility visualization modes
- `left`/`right`: change the selected month
- `k`/`l`: move the day cursor within the month (updates the tooltip)
- Mouse: hover/click on a day column to show the tooltip (terminal-dependent)
