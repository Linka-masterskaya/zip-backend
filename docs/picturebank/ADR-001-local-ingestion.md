# ADR-001: Local Pictures Bank ingestion

- Status: Accepted
- Date: 2026-08-23
- Scope: N11 local Pictures Bank

## Context

N11 adds a PostgreSQL + MinIO implementation of `picturebank.Source`. System Pictures Bank content must be managed without exposing MinIO URLs and without mixing system objects with organization-owned media or organization storage quota.

The task allows either an admin-only ingestion HTTP API or a supported seed command. The choice must be explicit before the implementation surface is frozen.

## Decision

Use a **supported operator CLI** instead of adding an admin HTTP ingestion API.

The command is `cmd/picturebank-seed` and supports:

- `add` — validate an image, upload it under the reserved MinIO namespace and create PostgreSQL metadata;
- `delete` — remove the system object and metadata by UUID.

No public/admin HTTP mutation routes are added for system Pictures Bank content.

## Rationale

1. Public Pictures Bank routes remain read/reference-only and keep exactly the external-adapter contract used by the frontend.
2. Ingestion does not require introducing a new application role or another privileged HTTP authentication policy.
3. System content administration stays an operator/deployment action and cannot consume a user's media quota.
4. The delete path can fail closed on the reserved object prefix, preventing accidental deletion of organization media if metadata is corrupted.
5. The command is usable in local development and production with the same application configuration and credentials already required for PostgreSQL and MinIO.
6. Upload rollback removes the reserved MinIO key before deleting metadata. If that cleanup cannot be confirmed, metadata remains as an operator-visible recovery handle instead of creating an invisible orphan.

## Storage boundary

Metadata lives in `picture_bank_images`.

Binary objects live in the existing private MinIO bucket under the reserved prefix:

```text
system/pictures-bank/<picture-uuid>
```

User media continues to use its own namespace (`media/<org-id>/...`). The CLI refuses to delete any key outside `system/pictures-bank/`.

## Consequences

- Operations that need to add or remove system content require shell/job access to the backend runtime environment.
- No direct MinIO URL becomes part of the API contract.
- Switching `feature_flags.local_bank` changes only the backend adapter; pack configs continue to store `source_picture_id` and the frontend continues to use `/api/v1/pictures/...`.
