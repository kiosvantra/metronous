# Skill Registry (for Judgment Day / Orchestrator)

This repository is written in Go and uses the internal TUI built with Bubble Tea + Lipgloss.

## Compact Rules (auto-resolved)

### Go (all `.go`)
- Correctness: run `go test ./...` and ensure all packages pass.
- Error handling: do not ignore errors returned from DB/store calls; propagate or handle explicitly.
- Concurrency: avoid data races; do not mutate shared state without synchronization.
- Performance: avoid unnecessary allocations/complexity in hot paths.
- Naming & conventions: follow existing identifiers and file structure.

### TUI (paths under `internal/tui/*.go`)
- Rendering correctness: ensure `View()` output is stable across cursor moves (no leftover artifacts, consistent height where needed).
- Input semantics: key handling must match documented controls; avoid changing behavior in unrelated tabs.
- UX: maintain consistent color palettes and avoid truncated/ambiguous labels.
- Tests: update/extend `internal/tui/*_test.go` so behavior is covered.

### Data / stores (paths under `internal/store/*.go`)
- Window semantics: date/time windows must be treated as `[since, until)` consistently.
- Units: keep USD/time units consistent and document any conversions.
- SQL/schema safety: do not break interfaces; keep migrations/back-compat in mind.

## Skill Selection Notes

When the target includes:
- Go tests or test changes: prefer the `go-testing` skill.
- PR creation/review workflow: prefer the `branch-pr` skill.
