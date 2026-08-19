# ADR-001: AI assistant / LLM worker architecture

- Status: Proposed by N9 discovery; requires Product confirmation of v1 commands and confirmation UX before N9 can be marked done
- Date: 2026-08-19
- Scope: discovery/architecture only; AI worker implementation is out of scope

## Context

Linka needs an AI assistant for natural-language commands, generation/editing of cards through JSON Patch, and TTS orchestration.

The repository already provides part of the boundary this discovery must reuse:

- NATS JetStream stream `AI_JOBS` with draft LLM subjects `ai.llm.req` and `ai.llm.resp.*`;
- draft broker payloads `broker.LLMRequest` and `broker.LLMChunk`;
- `pkg/linka/schema.json`, `pkg/linka.ValidateConfig`, and `pkg/linka.MaxConfigSize` as the canonical server-side config validation boundary;
- persisted packs expose `updated_at`, which can be used as the optimistic-concurrency token without adding a pack revision column;
- OpenAI configuration (`openai.api_key`, `openai.base_url`, `openai.org_id`) already exists;
- the TTS boundary already has `ai.tts.jobs` / `broker.TTSJob`; N9 must not redefine the TTS contract owned by #81.

The existing LLM broker structs are scaffolding, not a frozen v1 contract. N9 freezes the intended behavior and leaves implementation changes to follow-up issues.

## Decision summary

1. The frontend talks only to the Linka HTTP API. NATS remains an internal async boundary.
2. The LLM never writes a pack or media record directly. It returns a proposal; server/domain code owns authorization, validation and persistence.
3. V1 mutation output is a restricted RFC 6902 JSON Patch over `config.blocks` only. Pack metadata, settings, ownership and media references are outside the model-owned patch boundary.
4. The server validates every proposal before it can be shown as applicable:
   - decode and size/operation limits;
   - operation and path allowlist;
   - apply to an in-memory copy of the captured config;
   - `pkg/linka.ValidateConfig` on the resulting document.
5. A request captures the pack `updated_at` value. Apply reloads/locks the pack and succeeds only if `updated_at` is unchanged; otherwise it returns a conflict. No new pack revision column is required for v1.
6. The user-facing request lifecycle is asynchronous. HTTP creates/cancels/applies requests; SSE provides progress/result events.
7. Existing NATS subject names stay unchanged for v1 (`ai.llm.req`, `ai.llm.resp.<request_id>`). The payload contains `version: 1`, so the contract is versioned without forcing a stream-subject migration.
8. Cancellation is durable at the API/request-state layer and best-effort for provider compute. A Core NATS cancellation notification may stop an active worker early; late responses are ignored after cancellation.
9. OpenAI Responses API is the initial provider boundary because this repository already contains OpenAI configuration and the API supports streaming and strict structured output. Provider access remains behind an internal interface.
10. The concrete model is selected by the golden-fixture evaluation before implementation is enabled; model identity is configuration, not an API contract.
11. TTS is orchestration, not an LLM-generated media patch. The assistant delegates to the final #81 TTS job/result contract and reuses `media_files` through server-owned code.

## High-level architecture

```text
Frontend
   |
   | HTTP: create / status / events / cancel / apply
   v
Linka API
   | auth + rate/quota + idempotency
   | load pack config + capture packs.updated_at
   | persist assistant request state
   v
NATS JetStream: ai.llm.req
   |
   v
LLM worker
   | provider call with strict structured output
   | progress/result events
   v
NATS: ai.llm.resp.<request_id>
   |
   v
Linka API
   | patch policy -> apply to copy -> pkg/linka.ValidateConfig
   | preview / confirmation
   v
Apply: lock/reload pack -> compare updated_at -> save transactionally or reject as stale
```

## Provider and credential boundary

The worker depends on a small provider interface; domain code does not depend on an OpenAI SDK:

```text
Generate(ctx, request) -> proposal/events
```

Provider credentials are read only by backend/worker configuration. They are never returned to the frontend, written to prompts, or included in logs/traces.

### Initial provider

Use the OpenAI Responses API for the first implementation/evaluation. The repository already has `OpenAIConfig`, so this choice does not introduce a second credential model.

Provider requirements:

- streaming support for progress UX;
- strict JSON-schema structured output for the proposal envelope;
- configurable request storage/retention controls;
- a model snapshot that passes the golden quality/latency/cost gates.

The implementation must send Responses requests with storage disabled where supported. OpenAI API business inputs/outputs are not used for model training by default, but default endpoint retention and abuse-monitoring behavior still applies unless the organization is approved/configured for stronger controls. EU data residency/ZDR is therefore an ops/legal deployment decision, not something application code may assume.

## Proposed v1 commands

These values are an engineering proposal and require Product confirmation before N9 is complete.

| Command | Purpose | Main limit | Mutation behavior |
|---|---|---|---|
| `generate_cards` | Add new card blocks from a natural-language instruction | max 20 new blocks/request | proposal -> validate -> preview/apply |
| `edit_cards` | Rewrite or structurally edit selected existing blocks | max 20 selected blocks/request | proposal -> validate -> preview/apply |
| `generate_tts` | Generate speech for selected text elements | max 50 text elements/request | delegates to #81; no LLM-owned media patch |

Not in v1: arbitrary tool execution, web browsing, model-selected database queries, pack deletion, ownership/permission changes, direct media upload, or patching arbitrary config paths.

## Confirmation policy

**Product confirmation required.** Recommended v1 policy is deliberately simple: every config mutation is previewed and requires an explicit Apply action. This avoids encoding fragile heuristics such as “20% of text changed” into the backend before the product UX is settled.

`generate_tts` is also user initiated, but its exact confirmation behavior is owned by the #81/product flow rather than by the LLM patch policy.

The HTTP/stream/storage boundary does not depend on whether Product later relaxes confirmation for some non-destructive command; that is a policy flag on an already validated proposal.

## JSON Patch safety model

Detailed contract: `docs/ai/llm-contract-v1.md`.

V1 rules:

- allowed operations: `add`, `replace`, `remove`;
- patch boundary: `/blocks/...` only;
- existing block/element IDs are immutable;
- model may not set/change `media_id`, `media_url`, or `source_picture_id`;
- max 100 operations and 64 KiB serialized patch;
- prompt max 16 KiB UTF-8;
- patch is applied only to a copy until the user applies it;
- final config must pass `pkg/linka.ValidateConfig`, including the existing 5 MiB size limit and cross-reference checks.

`metadata`, `settings`, pack title/status/ownership and all DB fields are intentionally outside the v1 model mutation boundary. This prevents an AI card-edit command from silently changing unrelated pack state.

## Optimistic concurrency

The current pack model already exposes `UpdatedAt` and every config-changing repository query updates `updated_at`.

V1 therefore uses:

```text
base_updated_at = pack.updated_at captured when request is created
```

Apply performs a row lock or conditional update and verifies the current `updated_at` still equals `base_updated_at`. If not, return `409 AI_PACK_CHANGED` and require the user to regenerate/rebase the proposal.

This keeps stale-write protection inside the existing persistence model. A dedicated revision column can be considered later only if timestamp-based concurrency proves insufficient.

## Streaming and cancellation

### User-facing stream

`POST /api/v1/ai/assistant/requests` returns `202 Accepted` with a request UUID. The frontend reads `GET .../{id}/events` as SSE.

Event types:

- `accepted`
- `started`
- `progress`
- `proposal`
- `failed`
- `cancelled`
- `completed`

Only `proposal` contains a complete patch. Provider token fragments are never executable patch operations.

### Internal responses

Worker events use the existing response subject shape:

```text
ai.llm.resp.<request_id>
```

with a `version: 1` envelope and monotonically increasing `seq`.

### Cancellation

`POST .../{id}/cancel` atomically marks the persisted request cancelled. The API may publish an ephemeral Core NATS notification `ai.llm.cancel.<request_id>` to stop an active provider context early. Correctness does not depend on delivery of that notification: the API rejects/ignores any proposal received after persisted state became cancelled.

## Idempotency

Create accepts `Idempotency-Key` for mutation commands. Persist a uniqueness key scoped to `(organization_id, user_id, idempotency_key)` with the request payload hash.

- same key + same payload -> return the existing request;
- same key + different payload -> `409 AI_REQUEST_CONFLICT`.

The JetStream publish should also use a stable message ID derived from `request_id` so broker duplicate detection complements, but does not replace, persisted idempotency.

Apply is idempotent by request state: after a proposal has been applied successfully, a retry returns the completed state rather than applying the patch twice.

## Retry and dead-letter behavior

Provider call:

- retry at most 2 times for 429, transient network failures and provider 5xx;
- exponential backoff with jitter inside the overall request deadline;
- no automatic retry for auth/config errors or ordinary provider 4xx;
- at most one regeneration attempt for syntactically well-formed output that fails Linka-specific patch validation.

LLM consumer:

- explicit ACK on success;
- NAK/re-delivery up to the configured LLM `MaxDeliver` value;
- on the final failed delivery publish a redacted record to `ai.llm.dlq` and ACK/terminate the original work item;
- the implementation issue must add the LLM consumer settings and DLQ subject/retention to broker configuration.

DLQ contains request IDs, command, provider/model, attempt count, prompt version, timestamps and stable error code; never raw prompt, credentials or full pack config.

## Prompt/version management and injection defense

System/developer prompt templates are version-controlled and have a stable `prompt_version`. Every request/audit event records the version used.

User prompt and pack content are untrusted data. They are placed in clearly separated input fields, never promoted into privileged instructions. Prompt injection is mitigated primarily by capability restriction: the provider receives no DB credentials and cannot directly persist, call arbitrary tools, or mutate fields outside the server-side patch allowlist.

Prompt secrecy is not an authorization mechanism; prompts contain no credentials or other secrets.

## Moderation and safety policy

V1 performs safety checks at two points:

1. user natural-language prompt before the provider generation request;
2. generated user-visible textual values before the proposal is exposed/applicable.

For the OpenAI baseline, use the Moderations API (`omni-moderation-latest` or its then-current supported successor). A flagged request/proposal becomes `AI_CONTENT_BLOCKED`; no patch is applied. Store only category/result metadata needed for audit, not the full moderated text.

Provider refusal is represented as a stable assistant failure state, not retried as a transient failure.

Authorization, patch policy and `pkg/linka.ValidateConfig` remain mandatory even when moderation succeeds; moderation is not a substitute for structural/security validation.

## Rate limits, quotas and budgets

Implementation defaults are configuration-driven:

- max prompt: 16 KiB;
- max proposal: 100 operations / 64 KiB patch;
- max 2 active LLM requests per user;
- max 10 active LLM requests per organization;
- organization request rate limit: 60 created requests/minute by default;
- provider input/output token caps configured per selected model;
- configurable organization daily cost budget; new requests fail with `AI_BUDGET_EXCEEDED` once exhausted;
- provider timeout 30 s for the normal v1 card-edit path.

The limits are guardrails, not product SLA. Values are tuned from production metrics without changing the API/NATS contract.

## Persistence and audit boundary

A follow-up implementation issue adds assistant request persistence separate from `packs.config`.

Minimum request/audit fields:

- request ID, organization/user/pack IDs;
- command and state;
- idempotency key hash + payload hash;
- `base_updated_at`;
- prompt version, provider and model;
- validated proposal patch (JSONB) and confirmation/apply state;
- token/cost/latency counters;
- stable error code and timestamps.

Do not persist raw natural-language prompts by default. If Product wants conversation history, that is a separate privacy/storage feature with its own retention policy.

## Observability and partial/error UX

Metrics (names illustrative):

- `ai_llm_requests_total{command,status}`
- `ai_llm_request_duration_seconds{command}`
- `ai_llm_first_event_seconds{command}`
- `ai_llm_tokens_total{direction,model}`
- `ai_llm_cost_usd_total{model}`
- `ai_llm_validation_failures_total{reason}`
- `ai_llm_provider_errors_total{provider,code}`
- `ai_llm_cancelled_total`
- `ai_llm_dlq_total`

Logs/traces carry IDs, command, prompt version, provider/model and stable error code, but not raw prompts or full config.

UX rules:

- partial progress may be shown, but partial patch data is never applied;
- disconnecting SSE does not cancel work;
- provider/policy failures end in a stable `failed` state that can be fetched later;
- cancellation is visible as `cancelled` even if the provider computation finishes late;
- stale apply returns a conflict with a clear “pack changed; regenerate proposal” action.

## Golden/eval acceptance thresholds

The fixtures in `docs/ai/testdata/golden-prompts.jsonl` form the initial deterministic regression set. Before implementation is enabled, run them repeatedly against candidate prompt/model snapshots and record the report.

Quality gates:

- 100%: no accepted proposal touches a forbidden path or media field;
- 100%: every accepted proposal passes `pkg/linka.ValidateConfig`;
- >= 95%: command intent and requested scope are correct on reviewer-scored fixtures;
- 100%: prompt-injection fixtures cannot bypass patch policy;
- 0 duplicate persistence writes under idempotency replay tests.

Performance/cost gates for a normal request (<= 10 generated/edited cards, fixture context):

- p95 first progress event <= 3 s;
- p95 complete proposal <= 15 s;
- hard provider deadline <= 30 s;
- p95 provider cost <= USD 0.05/request during the evaluation dataset.

If no candidate model meets the gates, record the benchmark and reopen model/threshold selection rather than silently weakening the controls.

## TTS boundary (#81)

N9 does not invent a second TTS result model. Current main already exposes `broker.TTSJob` / `ai.tts.jobs`; `broker.TTSResult` and `ai.tts.done.*` are currently reserved/unused in the broker documentation. The implementation must consume the final contract delivered by #81 and reuse `media_files` exactly as #81 defines it.

The LLM proposal may identify which text elements the user selected, but only server/domain code dispatches TTS jobs and writes server-owned media references back into config.

## Alternatives considered

### Synchronous provider call inside an HTTP handler

Rejected: couples HTTP lifetime to provider latency and makes cancellation/retry/idempotency harder while the repository already has a JetStream direction for AI work.

### Model returns the entire Linka config

Rejected: larger output, higher cost, stale-overwrite risk and poor auditability. A bounded patch can be inspected and validated against the existing config.

### Worker writes directly to PostgreSQL

Rejected: duplicates authorization/domain rules and gives the model execution path excessive authority. Worker produces proposals; Linka server/domain code persists.

### Version NATS subjects by renaming them to `.v1`

Rejected for v1: the current stream already registers `ai.llm.req` / `ai.llm.resp.*`. Versioning the payload envelope preserves compatibility and avoids a stream-subject migration. A future incompatible transport may introduce new subjects deliberately.

### Execute streamed JSON Patch chunks as they arrive

Rejected: a stream may stop in the middle of an object/operation. Progress can stream, but mutation becomes executable only after the complete structured proposal passes server validation.

## Implementation backlog produced by this discovery

1. **Assistant API** — routes, auth/rate/quota, SSE, cancellation, idempotency and request state. Estimate: 1 small PR.
2. **Patch policy** — RFC 6902 parser/apply, `/blocks` allowlist, immutable/media checks, `pkg/linka.ValidateConfig`, `updated_at` concurrency. Estimate: 1 small PR.
3. **LLM worker/provider** — LLM consumer settings, provider interface, OpenAI Responses adapter, strict output, retry/cancel, response publisher. Estimate: 1-2 PRs.
4. **Prompt/evals** — versioned prompts, golden runner, model comparison and quality/latency/cost report. Estimate: 1 small PR.
5. **Persistence/audit** — assistant request table/repository, idempotency and retention. Estimate: 1 small PR.
6. **Observability/DLQ** — metrics/tracing/alerts, final-delivery handling, DLQ retention/tooling. Estimate: 1 small PR.
7. **TTS orchestration** — integrate the final #81 TTS contract and map results to server-owned media references. Estimate: 1 small PR after #81 contract is frozen.

The issues are intended to be independently reviewable once the contract fields are frozen.

## Open confirmations before N9 can be marked done

- **Product**: approve/adjust the v1 command set and confirmation behavior.
- **Legal/ops**: decide whether production requires EU regional processing/data residency and ZDR/MAM controls.
- **Engineering eval**: choose/pin the concrete model snapshot from candidates that pass the golden gates.

These confirmations do not change the chosen storage/API *shape*: assistant request state remains separate from pack config; HTTP remains the user boundary; NATS remains the worker boundary; model output remains a validated proposal. Product may change allowed command enum values/policy without redesigning that boundary.

## References

- OpenAI Responses API / streaming: https://platform.openai.com/docs/api-reference/responses-streaming
- OpenAI structured outputs: https://platform.openai.com/docs/api-reference/responses
- OpenAI API data controls / retention / data residency: https://platform.openai.com/docs/models/default-usage-policies-by-endpoint
- OpenAI Moderations API: https://platform.openai.com/docs/api-reference/moderations
- OWASP GenAI Top 10 — Prompt Injection: https://genai.owasp.org/llmrisk/llm01-prompt-injection/
- OWASP GenAI Top 10 — Excessive Agency: https://genai.owasp.org/llmrisk/llm062025-excessive-agency/
