# codex frame fixtures — provenance & validation

These fixtures are transcribed from the codex v2 frames exercised in
`internal/codexappgateway/broker/*_test.go`. They are correct for the shapes
codex-app-gateway already round-trips against codex 0.137.0.

## Validate / refresh against a live codex tag

1. Get a workspace codex token (console: `POST /api/codex/tokens`).
2. Connect a ws client to `<codex-app-gateway>/codex-app/ws` with
   `Authorization: Bearer <token>`, do the initialize/initialized handshake,
   send `thread/start`, then `turn/start` with
   `{"threadId":"<id>","input":[{"type":"text","text":"say hi then run ls"}]}`.
3. Log every server->client frame. Confirm the `item/completed` /
   `turn/completed` shapes match these files; if codex changed field names,
   update the fixtures AND the struct json tags in `mapper/mapper.go`.

## Not yet pinned (deferred to P1.5 / P2)
- `item/agentMessage/delta` — incremental text deltas (finer-grained streaming
  than per-`item/completed`). Record its exact params to add
  `TEXT_MESSAGE_CONTENT` streaming.
- `commandExecution` / `fileChange` item schemas — needed for P2 tool events.
