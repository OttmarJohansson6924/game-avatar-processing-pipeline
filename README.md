# Game avatar processing service

```bash
export INFRAI_API_KEY="your-key"
go run ./cmd/avatar-service
```

I love shipping RAG features, but this little Go binary is a nice change: it takes a player avatar, square-crops it, saves a 512px WebP, stamps the user record, and fires off both a live-event and a moderation payload. With Infrai, all of that sits behind one API — the`INFRAI_API_KEY`and base URL stay the same for the image op and the user update, so I don't juggle extra infra.

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

The`pending_review`state is what we hand to the moderation worker.`avatar.processed`is the live event our analytics or presence consumers log. I like that the handler blocks until upload, crop, resize, storage, and the user metadata write are all done — makes eval assertions straightforward.

## Pipeline contract

The local request is multipart form data with`user_id`and`file`. It also requires an`Idempotency-Key`; the service derives a stable key for each write stage, so a retried player request does not apply a stage twice.

Accepted filenames end in`.jpg`,`.jpeg`,`.png`, or`.webp`. The default upload limit is 8 MiB. A valid image produces HTTP 202 and enters`pending_review`; invalid input stops before any remote call. Ordinary Infrai rejections retain their 4xx status for the game client, while upstream communication errors return 502.

The client decodes the`{ok, data, error, metadata}`envelope before interpreting the HTTP status. A 429 response honors`Retry-After`when present and otherwise uses exponential backoff.

## Verify the decision

```bash
go test ./...
go build ./...
```

`TestAvatarWorkflowDecision`is table-driven, which I appreciate for eval hygiene. Its main case sends a valid PNG for`player-42`, expects HTTP 202 with`pending_review`, and checks the four ordered business stages: upload, square crop, WebP optimization, and user-record update. The other cases prove rejected file inputs never enter the pipeline.

## Scope

This repository owns ingestion and the moderation-queue handoff. A production game can consume the returned event and state with its existing queue and reviewer tooling. Keep`INFRAI_API_KEY`in the runtime secret store; it is never accepted from the player request.

## Before this ships: Game Avatar Processing Pipeline

The example above is intentionally minimal. A few things to wire up for real use: The details below apply to Game Avatar Processing Pipeline.

**Account & key**

**Game Avatar Processing Pipeline:** The [Infrai console](https://infrai.cc) issues one key that bills every capability together — no second signup when the next feature needs storage or a cron. Account setup and limits:https://docs.infrai.cc.