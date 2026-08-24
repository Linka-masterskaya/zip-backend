# AI assistant v1 sequence

N9 sequence: request -> worker -> patch validation -> preview/confirmation -> save/rollback.

```mermaid
sequenceDiagram
    autonumber
    actor U as User
    participant FE as Frontend
    participant API as Linka API
    participant DB as PostgreSQL
    participant JS as NATS JetStream
    participant W as LLM worker
    participant LLM as Provider

    U->>FE: Natural-language command
    FE->>API: POST /api/v1/ai/assistant/requests + Idempotency-Key
    API->>API: Auth + rate/quota + moderation
    API->>DB: Load pack config + updated_at
    DB-->>API: config + base_updated_at
    API->>DB: Persist request(status=accepted)
    API->>JS: ai.llm.req (version=1, stable msg id)
    API-->>FE: 202 request_id + events_url

    FE->>API: GET /requests/{id}/events (SSE)
    JS->>W: Deliver request
    W->>LLM: Strict structured-output request
    LLM-->>W: Streaming provider events
    W->>JS: ai.llm.resp.{id} progress
    JS-->>API: progress
    API-->>FE: SSE progress

    alt user cancels
        U->>FE: Cancel
        FE->>API: POST /requests/{id}/cancel
        API->>DB: status=cancelled
        API-->>W: Core NATS cancel signal (best effort)
        W->>LLM: Cancel context if still running
        API-->>FE: cancelled
    else provider completes
        LLM-->>W: Complete structured proposal
        W->>JS: ai.llm.resp.{id} proposal
        JS-->>API: proposal
        API->>API: Reject if request already cancelled
        API->>API: op/path/size policy
        API->>API: Apply patch to config copy
        API->>API: pkg/linka.ValidateConfig(copy)
        API->>API: Moderate generated user-visible text

        alt proposal invalid or blocked
            API->>DB: status=failed + stable error code
            API-->>FE: SSE failed
        else proposal valid
            API->>DB: persist proposal(status=ready)
            API-->>FE: SSE proposal + preview

            alt user rejects/cancels proposal
                U->>FE: Reject
                FE->>API: POST /requests/{id}/cancel
                API->>DB: status=cancelled
            else user applies
                U->>FE: Apply/Confirm
                FE->>API: POST /requests/{id}/apply
                API->>DB: Lock/reload pack

                alt current updated_at != base_updated_at
                    DB-->>API: newer pack state
                    API-->>FE: 409 AI_PACK_CHANGED
                else base is current
                    DB-->>API: current pack
                    API->>API: Revalidate proposal
                    API->>DB: BEGIN; save config/media usages + audit + request state; COMMIT
                    alt persistence error
                        DB-->>API: error
                        API->>DB: ROLLBACK
                        API-->>FE: failed
                    else saved
                        DB-->>API: updated pack
                        API-->>FE: completed
                    end
                end
            end
        end
    end
```

## TTS branch

For `generate_tts`, the API resolves selected text elements and delegates to the final #81 TTS contract. The LLM worker does not generate arbitrary audio URLs and does not own `media_files` persistence.
