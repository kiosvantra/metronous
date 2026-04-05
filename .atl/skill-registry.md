# Skill Registry

This registry summarizes available user skills and project conventions so agents can quickly resolve which skills to apply.

## User Skills

### branch-pr
- Trigger: Creating or updating a pull request, preparing changes for review, or working with GitHub issues linked to a PR.
- Context: Any repository hosted on GitHub using the Agent Teams Lite workflow.

### go-testing
- Trigger: Writing or updating Go tests, especially Bubbletea TUI tests or adding coverage around CLI and daemon flows.
- Context: Go modules in this project (for example, packages under `internal/`).

### issue-creation
- Trigger: Creating GitHub issues, reporting bugs, or proposing new features.
- Context: When work should start from an issue-first workflow.

### judgment-day
- Trigger: When the orchestrator requests an adversarial double review (for example, "judgment day" / "dual review").
- Context: Any non-trivial change that needs two independent judges before merging.

### sdd-init / sdd-explore / sdd-propose / sdd-spec / sdd-design / sdd-tasks / sdd-apply / sdd-verify / sdd-archive
- Trigger: Spec-Driven Development lifecycle for significant changes.
- Context: Use these skills to plan, implement, verify, and archive changes using SDD.

### skill-creator
- Trigger: When new reusable patterns or behaviors should be captured as skills.
- Context: Any repeated workflow that would benefit from automation or standardization.

### skill-registry
- Trigger: When updating or regenerating this registry after installing, removing, or changing skills.
- Context: Keeps the registry in sync with the actual skill set.

## Project Standards (Compact Rules)

### Go and TUI code
- Prefer idiomatic Go and keep functions small and focused.
- For Bubbletea and Lipgloss TUI code, centralize layout decisions and avoid duplicating width/height logic across views.
- Use table-driven tests in `_test.go` files and keep test helpers under `internal/...` when they are project-specific.

### Testing
- Default test command: `go test ./...`.
- For changes affecting the TUI or CLI behavior, add or update Go tests under `internal/tui/` or `internal/cli/` when possible.
- Aim to keep Strict TDD Mode enabled by writing or updating tests alongside implementation changes.

### SDD workflow
- Use SDD for multi-file or architectural changes; simple one-off edits can be done directly.
- Each SDD phase should read only its required inputs and write a single artifact (explore, proposal, spec, design, tasks, apply-progress, verify-report, archive-report).
- Artifacts are persisted primarily in Engram for this project; openspec may be added later if file-based specs are needed.

### Pull requests and reviews
- Prefer small, focused PRs that are easy to review.
- When in doubt, run Judgment Day (dual review) for risky refactors or behavior changes.
