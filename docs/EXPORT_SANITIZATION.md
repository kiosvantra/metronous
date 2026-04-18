# Export Sanitization Rules (Issue #18)

This document defines the v1 export contract safety rules used by:

metronous report export --allow-export --out <path>

Safety defaults:
- Export is disabled by default.
- No implicit data egress is performed.
- Export writes only to a local file path provided by the user.

Sanitization pipeline (v1):
1) Agent identity pseudonymization
   - `agent_id` is transformed to `anon-<sha256-prefix>`.
   - Raw agent IDs are never emitted in export payloads.

2) Free-text redaction by omission
   - `decision_reason` is intentionally omitted from exported benchmark runs.
   - `artifact_path` is intentionally omitted from exported benchmark runs.

3) Semantic phase normalization
   - Allowed phase labels: propose, spec, design, implement, verify, untagged.
   - Unknown/non-standard labels are exported as `custom`.

4) Export shape restrictions
   - Export includes only aggregate benchmark rows and semantic phase summaries.
   - Raw session IDs and raw metadata maps are not included in the contract.

Contract:
- `schema_version` is required and currently set to `metronous.export.v1`.
- Future versions should evolve via new schema versions without breaking v1 readers.
