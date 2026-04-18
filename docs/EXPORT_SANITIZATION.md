# Sharing/Leaderboard Sanitization Contract (Issues #18 + #20)

This document defines the v1 opt-in sharing contract used by:

metronous report export --allow-export --out <path>

Optional preview path (no write, no send):

metronous report export --allow-export --dry-run --out <path>

Safety defaults:
- Sharing/export is disabled by default.
- No implicit data egress is performed.
- No network transmission is performed by this command.
- Contract output is local-file-only when explicitly requested.

Sanitization pipeline (v1):
1) Agent identity pseudonymization
   - `agent_id` is transformed to `anon-<sha256-prefix>`.
   - Raw agent IDs are never emitted in sharing payloads.

2) Free-text redaction by omission
   - `decision_reason` is intentionally omitted from exported benchmark runs.
   - `artifact_path` is intentionally omitted from exported benchmark runs.

3) Semantic phase normalization
   - Allowed phase labels: propose, spec, design, implement, verify, custom, untagged.
   - Unknown/non-standard labels are exported as `custom`.

4) Export shape restrictions
   - Export includes only aggregate benchmark rows and semantic phase summaries.
   - Raw session IDs and raw metadata maps are not included in the contract.

Contract and provenance:
- `schema_version` is required and currently set to `metronous.export.v1`.
- `generated_at` must be RFC3339 UTC timestamp.
- `provenance` is required and currently fixed to:
  - `contract`: `sharing-leaderboard`
  - `consent_mode`: `explicit-opt-in`
  - `egress`: `local-file-only`
  - `sanitization_profile`: `sanitized-v1`

Validation + auditability:
- Submission/write path validates schema + sanitization rules before output.
- Invalid payloads are rejected.
- Export actions are audit logged to local `sharing_audit.log` in `--data-dir`.
