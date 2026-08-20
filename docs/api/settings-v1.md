# AB-48 settings v1 contract

This document is the backend decision record for AB-48. The issue requires persistence, a bounded JSON object, an allow-list of top-level keys, templates, and conditional TTS voice validation. No separate frontend schema for the nested values exists in the repository/organization at the time of implementation, so v1 deliberately keeps those nested values opaque instead of inventing product semantics that would later require a migration.

## Product decisions for AB-48 v1

| Blocker from AB-48 | v1 decision |
| --- | --- |
| Who owns settings? | The authenticated **user** owns site settings and templates. They therefore survive relogin and use from another device. |
| Site voice vs pack/action voice | `settings.voice` is the user's site-level **default**. An explicit voice supplied by a concrete pack/action/TTS request wins at the consumer. AB-48 does not rewrite pack data or the TTS request contract. |
| What is calibration offset? | **Out of scope for AB-48 v1.** No `calibration_offset` key is persisted until coordinate system, unit, range, and user-vs-device ownership are defined. |
| Which keys/ranges are in v1? | Exactly the seven keys below. `voice` is a non-empty TTS voice ID. Other values are opaque valid JSON in v1; there are no nested numeric/enum ranges until a frontend contract is approved. The whole document is still bounded to 64 KiB. |
| Image/audio bank settings | **Out of scope for AB-48 v1.** Track M does not define a settings payload, so media-bank semantics are not guessed inside user settings. |

These are intentional compatibility decisions for v1, not hidden implementation assumptions. A future nested schema can tighten individual keys without opening arbitrary top-level JSONB fields; a new top-level setting requires an explicit backend/OpenAPI change.

## Scope and ownership

- Settings are keyed by authenticated `user_id` in PostgreSQL.
- `PUT /api/v1/settings` is a **full replacement**, not a patch.
- A user without a saved settings row receives `{}`.
- Templates are also user-owned. Listing only returns the current user's templates, and deleting another user's template ID returns `404` rather than exposing ownership.
- Both settings and template rows are removed automatically when the owning user is deleted (`ON DELETE CASCADE`).

## v1 top-level keys

The server accepts only:

- `eye_control`
- `card_activation`
- `interactivity`
- `voice`
- `button_direction`
- `colors`
- `border_width`

`voice`, when present, must be a non-empty string. The remaining six values may contain any valid JSON value in AB-48 v1. This is deliberate: their nested field names, enums, units, and ranges have not been approved, while the acceptance criterion only requires basic document validation and an allow-list of top-level keys.

`templates` is not stored inside the settings JSON because templates have dedicated CRUD endpoints.

## Voice validation and precedence

`settings.voice` is the user's default voice. It does not override an explicit voice chosen by a pack/action/TTS request.

When `voice` is present, the backend calls the same TTS service used by `GET /api/v1/tts/voices`:

- if the voice catalog is available, the value must equal one returned voice `id`;
- an available but empty catalog rejects every explicit voice;
- if the catalog cannot be obtained (for example, TTS/cache outage), settings remain writable because AB-48 makes validation conditional on the list being available.

Templates use the same validation as the main settings document, including `voice` validation.

## Validation and limits

- Settings document: valid JSON **object**, maximum **64 KiB**.
- Template body: same settings-document rules and maximum **64 KiB**.
- Template name: **1–100 Unicode characters after trimming**.
- Unknown top-level settings keys are rejected with `400`.
- Oversized payloads/documents are rejected with `413`.
- The database repeats the object invariant with `jsonb_typeof(...) = 'object'` checks.
- The HTTP template wrapper rejects unknown fields and trailing second JSON values.

## Explicitly excluded from v1

- `calibration_offset` or any other device-calibration representation;
- image/audio bank preferences whose payload has not been defined by Track M;
- a nested schema/range contract for the six opaque non-voice values.

Adding any of these is a follow-up contract change, not an implicit extension of the JSONB column.
