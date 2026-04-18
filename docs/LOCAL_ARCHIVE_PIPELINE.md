# Local archive pipeline (bronze/silver/gold)

Metronous now includes an optional local archive pipeline with medallion stages:

- bronze: raw captured interactions
- silver: user-filtered/accepted interactions
- gold: manually curated benchmark cases

All archive behavior is local-only. There is no network export/sync in this pipeline.

## Safety defaults

Archive is disabled by default.

`~/.metronous/config.yaml`:

```yaml
archive:
  enabled: false
  capture_full_payload: false
  block_on_sensitive: false
  redact_patterns:
    - "(?i)api[_-]?key"
    - "(?i)authorization"
    - "(?i)password"
    - "(?i)secret"
    - "(?i)token"
  max_files_per_stage: 500
  max_bytes_per_stage: 104857600
  max_age_days: 30
```

Default-off behavior:
- no archive writes unless `archive.enabled=true`
- no full payload capture unless `capture_full_payload=true`

## Stage transitions

- ingest -> bronze is automatic (when enabled)
- bronze -> silver/gold is manual via promotion APIs/hooks
- optional sanitizer hook can modify payload before promotion
- known sensitive keys are redacted by configured patterns
- when `block_on_sensitive=true`, promotion is denied if sensitive keys remain

## Retention and pruning

Each stage can enforce deterministic retention caps:
- max files per stage (`max_files_per_stage`)
- max bytes per stage (`max_bytes_per_stage`)
- max age in days (`max_age_days`)

Pruning strategy is deterministic: oldest files first, tie-broken by filename.

## Visibility

Use:

```bash
metronous report archive-usage --data-dir ~/.metronous/data
```

or JSON:

```bash
metronous report archive-usage --data-dir ~/.metronous/data --format json
```

This command is local-only and does not perform any network egress.
