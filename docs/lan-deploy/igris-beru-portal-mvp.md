# IGRIS ↔ BERU Portal MVP on LAN

Metronous now ships a minimal portal backed by `timeline.db`.

## Endpoints

- `GET /timeline` — embedded web UI
- `GET /api/timeline/conversations`
- `GET /api/timeline/items?conversation_id=...`
- `GET /api/timeline/messages?conversation_id=...`
- `GET /api/timeline/stream?conversation_id=...` — SSE
- `POST /api/timeline/ingest` — same `X-Metronous-Auth` model as `/ingest`

## Safe default

`config.yaml` defaults to loopback-only:

```yaml
server:
  listen_address: "127.0.0.1:0"
  public_base_url: ""
  enable_timeline_lan: false
```

## Explicit LAN opt-in

To expose Metronous on your LAN, set both:

```yaml
server:
  listen_address: "0.0.0.0:8080"
  enable_timeline_lan: true
```

Without `enable_timeline_lan: true`, Metronous falls back to loopback binding even if `listen_address` is broader.

## Seed a conversation

```bash
curl -X POST http://127.0.0.1:8080/api/timeline/ingest \
  -H 'Content-Type: application/json' \
  -H "X-Metronous-Auth: $METRONOUS_INGEST_TOKEN" \
  -d '{
    "kind":"handoff",
    "conversation_id":"conv_igris_beru_001",
    "from_agent_id":"IGRIS",
    "to_agent_id":"BERU",
    "task_key":"portal-mvp",
    "title":"Ship timeline MVP",
    "body":"Implement schema, API, SSE, and UI.",
    "priority":"high"
  }'
```

Then open `http://<host>:8080/timeline`.
