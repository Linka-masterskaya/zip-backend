# AI assistant contract v1 (discovery)

Status: proposed versioned HTTP/NATS contract for N9. Implementation is split into follow-up issues.

## Contract principles

- HTTP `/api/v1` is the user-facing boundary.
- NATS is internal; existing LLM subject names are preserved for v1.
- NATS payloads carry `version: 1` for contract versioning.
- LLM output is a proposal, never a direct persistence command.
- V1 model-owned JSON Patch is restricted to `/blocks/...`.
- TTS reuses the final #81 contract rather than inventing a parallel audio path.

## HTTP API

All routes require authentication and enforce organization/pack access in the implementation.

### `POST /api/v1/ai/assistant/requests`

Creates an asynchronous assistant request.

Header for mutation commands:

```http
Idempotency-Key: <opaque client-generated key>
```

Request example:

```json
{
  "pack_id": "5b5a9a57-2e1e-4c94-a811-90eeef3e3398",
  "command": "edit_cards",
  "prompt": "Сделай формулировки короче и понятнее для ученика 10 лет",
  "selection": {
    "block_ids": ["card-1", "card-2"]
  }
}
```

Proposed command values pending Product confirmation:

- `generate_cards`
- `edit_cards`
- `generate_tts`

Response `202`:

```json
{
  "request_id": "4d4a8fbb-62e2-44c1-83a8-a356956fbbe0",
  "status": "accepted",
  "events_url": "/api/v1/ai/assistant/requests/4d4a8fbb-62e2-44c1-83a8-a356956fbbe0/events"
}
```

Idempotency:

- same key + same payload -> return the existing request;
- same key + different payload -> `409 AI_REQUEST_CONFLICT`.

### `GET /api/v1/ai/assistant/requests/{request_id}`

Returns durable request state and proposal metadata when available.

### `GET /api/v1/ai/assistant/requests/{request_id}/events`

SSE stream for progress/result UX. Browser disconnect does not cancel the request.

Event envelope:

```json
{
  "version": 1,
  "request_id": "4d4a8fbb-62e2-44c1-83a8-a356956fbbe0",
  "seq": 3,
  "type": "progress",
  "timestamp": "2026-08-19T13:00:00Z",
  "data": {}
}
```

Event types:

- `accepted`
- `started`
- `progress`
- `proposal`
- `failed`
- `cancelled`
- `completed`

Only `proposal` carries a complete patch.

Proposal example:

```json
{
  "version": 1,
  "request_id": "4d4a8fbb-62e2-44c1-83a8-a356956fbbe0",
  "seq": 4,
  "type": "proposal",
  "data": {
    "base_updated_at": "2026-08-19T12:58:04.123456Z",
    "patch": [
      {
        "op": "replace",
        "path": "/blocks/0/elements/0/value",
        "value": "Короткая формулировка"
      }
    ],
    "requires_confirmation": true,
    "summary": "Сокращён текст двух карточек"
  }
}
```

### `POST /api/v1/ai/assistant/requests/{request_id}/cancel`

Atomically marks the request cancelled. Idempotent: cancelling an already cancelled/completed request returns its current state according to normal state-transition rules.

The API may also publish a best-effort Core NATS cancellation signal to the active worker. Persisted request state is authoritative; late proposals for cancelled requests are ignored.

### `POST /api/v1/ai/assistant/requests/{request_id}/apply`

Applies an already server-validated proposal after user confirmation.

Request v1:

```json
{}
```

Server steps:

1. authorize against the pack;
2. load/lock current pack;
3. verify `packs.updated_at == proposal.base_updated_at`;
4. re-run patch policy and `pkg/linka.ValidateConfig` defensively;
5. save config/media usages using the existing pack domain transaction;
6. mark assistant request completed and return the updated pack/config state.

If the pack changed after proposal generation, return `409 AI_PACK_CHANGED`. No silent auto-merge.

## Internal NATS contract

### Versioning rule

V1 keeps the subjects already present in `internal/broker`:

```text
ai.llm.req
ai.llm.resp.<request_id>
```

The message envelope field `version: 1` is the NATS contract version. An incompatible future protocol can move to new subjects deliberately; N9 does not require renaming the existing subjects or migrating `AI_JOBS` just to add `.v1`.

The current `broker.LLMRequest` / `broker.LLMChunk` are draft scaffolding and will be replaced/extended by implementation PRs to match the envelope below.

### Request subject — `ai.llm.req`

```json
{
  "version": 1,
  "request_id": "4d4a8fbb-62e2-44c1-83a8-a356956fbbe0",
  "organization_id": "a9ecf20a-cd58-4c04-b3d9-fb9eca4e1caf",
  "user_id": "e693851a-5ac2-460b-905d-ae8529742729",
  "pack_id": "5b5a9a57-2e1e-4c94-a811-90eeef3e3398",
  "command": "edit_cards",
  "prompt": "...",
  "selection": {"block_ids": ["card-1"]},
  "base_updated_at": "2026-08-19T12:58:04.123456Z",
  "prompt_version": "assistant-v1.0.0",
  "locale": "ru-RU",
  "deadline_at": "2026-08-19T13:00:30Z"
}
```

Publish with a stable JetStream message ID derived from `request_id`.

### Response subject — `ai.llm.resp.<request_id>`

```json
{
  "version": 1,
  "request_id": "4d4a8fbb-62e2-44c1-83a8-a356956fbbe0",
  "seq": 4,
  "type": "proposal",
  "provider": "openai",
  "model": "<configured-evaluated-snapshot>",
  "prompt_version": "assistant-v1.0.0",
  "data": {
    "patch": [],
    "summary": "..."
  },
  "usage": {
    "input_tokens": 1200,
    "output_tokens": 220
  },
  "duration_ms": 4100
}
```

Allowed worker event types: `started`, `progress`, `proposal`, `failed`, `cancelled`.

### Cancellation signal — `ai.llm.cancel.<request_id>`

Best-effort Core NATS signal, not the durable source of truth:

```json
{
  "version": 1,
  "request_id": "4d4a8fbb-62e2-44c1-83a8-a356956fbbe0",
  "reason": "user_cancelled"
}
```

If the signal is missed, the worker may finish provider computation; the API still drops the late result because request state is already cancelled.

### DLQ — `ai.llm.dlq`

Follow-up broker work adds retention/consumer handling for this subject. DLQ contains redacted operational metadata only, not raw prompt/full config.

## JSON Patch subset

The provider returns RFC 6902-shaped operations, but v1 allows only a strict subset.

### Operations

Allowed:

- `add`
- `replace`
- `remove`

Rejected:

- `move`
- `copy`
- model-generated `test`

Concurrency is server-owned through `packs.updated_at`, not a model-supplied JSON Patch test operation.

### Patch boundary

V1 permits mutations only below `/blocks`.

Typical allowed path families:

```text
/blocks/-                                  # append a generated block
/blocks/<index>                            # replace/remove one block
/blocks/<index>/type
/blocks/<index>/elements
/blocks/<index>/elements/-
/blocks/<index>/elements/<index>
/blocks/<index>/elements/<index>/value
/blocks/<index>/answers
/blocks/<index>/answers/-
/blocks/<index>/answers/<index>
/blocks/<index>/pairs
/blocks/<index>/pairs/-
/blocks/<index>/pairs/<index>
/blocks/<index>/categories
/blocks/<index>/categories/-
/blocks/<index>/categories/<index>
/blocks/<index>/sequence
/blocks/<index>/sequence/-
/blocks/<index>/sequence/<index>
```

`pkg/linka.ValidateConfig` remains authoritative after the whole patch is applied, so cross-reference mistakes (answers/pairs/categories/sequence pointing to missing elements) are rejected.

### Forbidden fields

Regardless of path shape, reject changes to an existing object's:

```text
.../id
.../media_id
.../media_url
.../source_picture_id
```

New blocks/elements may include new non-empty IDs, but they must be unique and pass `pkg/linka.ValidateConfig`. Existing IDs are immutable.

The following are outside the model patch boundary entirely:

```text
/metadata
/settings
```

Pack DB fields (title/status/ownership/folder/organization/version history) are not part of Linka config patch authority either.

### Limits

- max 100 patch operations;
- max serialized patch 64 KiB;
- max prompt 16 KiB UTF-8;
- `generate_cards`: max 20 new blocks;
- `edit_cards`: max 20 selected blocks;
- final config must satisfy `pkg/linka.MaxConfigSize` (5 MiB) and `pkg/linka.ValidateConfig`.

### Validation pipeline

```text
provider structured output
  -> JSON decode
  -> operation count / byte limits
  -> op allowlist
  -> /blocks path allowlist + immutable/media checks
  -> apply to copy of captured base config
  -> pkg/linka.ValidateConfig
  -> persist validated proposal
  -> user preview/confirmation
  -> reload+lock pack
  -> compare packs.updated_at with base_updated_at
  -> revalidate
  -> transactional save or 409 conflict
```

No partial result is persisted into the pack config.

## Structured provider output

Provider output is constrained to an object conceptually equivalent to:

```json
{
  "summary": "string",
  "patch": [
    {
      "op": "add|replace|remove",
      "path": "string",
      "value": "optional JSON value"
    }
  ]
}
```

Strict provider schema adherence is defense in depth. Server-side patch policy and Linka validation are authoritative.

## TTS command

`generate_tts` does not allow the LLM to invent audio URLs or media IDs.

N9 only defines the assistant orchestration boundary:

1. server resolves the user's selected text elements;
2. server delegates synthesis to the final #81 TTS job/result contract;
3. #81/domain code creates/reuses `media_files`;
4. server-owned code updates permitted media references after TTS succeeds.

Current main's `broker.TTSJob`/`ai.tts.jobs` is therefore reused, while the final result mechanics follow #81 rather than this discovery document.

## Error model

Stable machine codes proposed for implementation:

- `AI_INVALID_COMMAND`
- `AI_REQUEST_CONFLICT`
- `AI_PACK_CHANGED`
- `AI_PROVIDER_UNAVAILABLE`
- `AI_PROVIDER_RATE_LIMITED`
- `AI_OUTPUT_INVALID`
- `AI_PATCH_FORBIDDEN`
- `AI_CONTENT_BLOCKED`
- `AI_CANCELLED`
- `AI_TIMEOUT`
- `AI_BUDGET_EXCEEDED`

User-facing localization is outside N9.

## Request state machine

```text
accepted -> running -> ready -> completed
                |        |
                |        +-> cancelled
                +-> failed
                +-> cancelled
```

Invalid transitions are rejected. Apply after `completed` is idempotent and does not write a second time.

## Product confirmation required

Before N9 is marked complete, Product must approve/adjust:

- the v1 command values;
- whether v1 requires explicit Apply for every mutation (recommended) or allows automatic apply for any safe subset;
- UX wording/behavior for preview, failure, stale conflict and cancellation.

Changing these policy values does not require changing the route layout, request-state storage shape, NATS subject names or patch-validation boundary.
