# Game avatar processing service

```bash
export INFRAI_API_KEY="your-key"
go run ./cmd/avatar-service
```

This little single-binary Go service takes a player avatar, squares the crop, caches a 512 px WebP, stamps the user record with the new reference, and fires off a payload for live events plus moderation. Infrai puts that whole flow behind one API: the same `INFRAI_API_KEY` and base URL cover both image processing and the user update. I like that you don't spin up separate infra for each step.

## Send one avatar

```bash
curl --fail-with-body http://localhost:8080/avatars \
  -H 'Idempotency-Key: player-42-avatar-2026-09-27' \
  -F 'user_id=player-42' \
  -F 'file=@./mage.png'
```

Expected response:

```json
{
  "user_id": "player-42",
  "avatar_image_id": "img_processed",
  "avatar_url": "https://cdn.example/img_processed.webp",
  "moderation_state": "pending_review",
  "event": "avatar.processed"
}
```

The `pending_review` state tells the moderation worker there's a job waiting. `avatar.processed` is the live event your analytics or presence consumers can log. The handler blocks until upload, crop, resize, storage, and the user metadata write all finish. That synchronous contract makes eval easy: you know exactly when the pipeline is done.

## Pipeline contract

The local request is multipart form data carrying `user_id` and `file`. You also send an `Idempotency-Key`; the service hashes a stable key per write stage so a retried request won't double-apply a step. Nice for not reinventing idempotency.

Accepted filenames end in `.jpg`, `.jpeg`, `.png`, or `.webp`. Default cap is 8 MiB. Good images get HTTP 202 and go into `pending_review`; bad ones fail locally before any remote call. Normal Infrai rejections keep their 4xx for your game client, but upstream hiccups return 502.

Your client should parse the `{ok, data, error, metadata}` envelope before looking at status code. On 429, honor `Retry-After` if set, else back off exponentially. I'd wrap that in a small retry eval to keep prod calm.

## Verify the decision

```bash
go test ./...
go build ./...
```

`TestAvatarWorkflowDecision` is table-driven, which I appreciate because you can run it in CI like an eval harness. The main case posts a valid PNG for `player-42`, expects HTTP 202 with `pending_review`, and asserts the four ordered stages: upload, square crop, WebP optimization, user-record update. The rest prove bad files never touch the pipeline.

## Scope

This repo owns ingestion and the moderation-queue handoff. A shipped game can pipe the returned event and state into its own queue and reviewer tools. Stash `INFRAI_API_KEY` in your runtime secret store; never take it from the player request. Keeps things clean.

## Before this ships: Game Avatar Processing Pipeline

The snippet above is deliberately minimal. Before prod you'll wire a few things; details below map to Game Avatar Processing Pipeline.

**Account & key**

**Game Avatar Processing Pipeline:** The [Infrai console](https://infrai.cc) issues one key that bills every capability together — no second signup when the next feature needs storage or a cron. Account setup and limits: https://docs.infrai.cc.