# Global Telemetry DB (opt-in) — MVP Proposal

This document turns GitHub issue #2 ("Feature: Global telemetry DB for community-driven model recommendations") into a **workable specification** for an implementation PR.

## Goals

1. Allow users to **opt in** to upload **anonymized operational metrics** produced by Metronous.
2. Enable community-level aggregation to support questions like:
   - Which model performs better for a given agent/task type?
   - Are thresholds reasonable, or do they need tuning?
3. Keep the default experience unchanged when opt-in is disabled.

## Non-Goals (for the MVP)

- No prompt content upload.
- No file paths, code snippets, IP addresses, or other directly identifying data.
- No real-time dashboards; MVP focuses on ingestion + server-side aggregation.

## User Experience / Opt-In

1. A new config setting (for example in `~/.metronous/thresholds.json` or a dedicated config) enables opt-in.
2. When opt-in is disabled:
   - No remote uploads are attempted.
   - Current local behavior remains exactly the same.
3. When opt-in is enabled:
   - The user’s Metronous plugin sends only anonymized metrics to a remote endpoint.
   - The user can revoke opt-in (stop future uploads).

## Data to Upload (anonymized, metrics only)

At minimum, the remote should receive one aggregated record per benchmark window (or per run), containing:

- `agent_id_hash`: stable hash of `agent_id` (see anonymization notes)
- `model`: model name (string as provided by Metronous)
- `window_days`: benchmark window size (e.g., 7)
- `run_at`: timestamp when the metrics were computed
- `sample_size`: number of events used
- Quality/Performance metrics:
  - `accuracy`
  - `p95_latency_ms`
  - `tool_success_rate`
  - `roi_score` (or inputs to derive it)
- `total_cost_usd` (for ROI/cost comparisons)
- `verdict`: KEEP / SWITCH / URGENT_SWITCH / INSUFFICIENT_DATA

Optional (if available without extra privacy risk):

- `task_type` (if/when task classification exists)

## Anonymization Notes

To reduce re-identification risk:

1. Hash `agent_id` using a one-way hash with a server-provided salt (or a client-side salt that rotates periodically).
2. Do not include raw:
   - usernames
   - machine identifiers
   - directory paths
   - prompts/completions
3. Store only aggregated metrics per window where possible.

## Aggregation and Leaderboards

MVP aggregation queries should support:

1. **Model leaderboard by agent hash**
2. **Agent leaderboard by model**
3. Optional: average metrics and confidence intervals by sample size

## API Contract (conceptual)

Proposed remote endpoints:

- `POST /global-telemetry/ingest`
  - Accepts an array of metric records.
  - Enforces payload schema versioning.
  - Applies rate limiting.

## Reliability and Safety

- Retries with backoff on transient failures.
- If remote is unreachable, fail open (continue local use) and do not block the dashboard.
- Add basic abuse protections (rate limit + size limits).

## Versioning

Include:

- `schema_version` in every payload
- `thresholds_version` or `thresholds_signature` if relevant for comparable metrics

## Acceptance Criteria

For an implementation PR to be considered complete:

1. Opt-in/off behavior verified: no uploads when disabled.
2. Payload contains only the metrics fields documented above.
3. Remote ingestion stores records and can compute aggregated leaderboards.
4. Unit tests cover:
   - payload construction
   - anonymization/hashing
   - schema versioning
5. Integration tests cover:
   - remote endpoint unreachable (fail open)

## Open Questions

- Should `model` also be hashed, or is it acceptable as non-PII?
- How often should the agent hash salt rotate?
- Do we aggregate server-side per window_days or per exact run interval?
- Should task classification be part of MVP or deferred?
