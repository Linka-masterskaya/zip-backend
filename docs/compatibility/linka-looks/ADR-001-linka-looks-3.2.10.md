# ADR-001: `.linka` compatibility with Linka Looks 3.2.10

- **Status:** Accepted — incompatible as-is; versioned conversion implemented and verified against the pinned client (see «Verification of the looks-3 converter»)
- **Date:** 2026-08-13
- **Backend snapshot:** `zip-backend` `444c4c8339ed0319820733b62764a79b4483fcc2`
- **Linka Looks:** release/tag `v3.2.10`, app version `3.2.10`, commit `b8e65af5825a5a3389e416253393c39d4d5353bd`
- **Looks set format:** `3.0`
- **Authoritative client paths:**
  - `src/electron/services/card-storage-service.ts` — `.linka` ZIP open/save path (`getConfigFile`, `saveSet`, `cleanFile`)
  - `src/common/interfaces/ConfigFile.ts` — `ConfigFile`, `normalizeConfigFile`, legacy migration
  - `src/frontend/utils/setGameLogic.ts` — quiz/match semantics

## Closure checklist

- [x] Exact supported Linka Looks version/build fixed: `3.2.10` / `b8e65af...`.
- [x] Reproducible fixture contains Cyrillic text, real PNG, real WAV, `single_choice`, and `matching`.
- [x] Current backend GET export path has a checked-in regression test and was executed in an exact-core isolated run; golden regenerated from that run.
- [x] Exact official Linka Looks `ConfigFile.ts` parser/model source was hash-verified and executed.
- [x] Open/order/media/Cyrillic and save round-trip loss are recorded.
- [x] ADR + field conversion/loss matrix are recorded.
- [ ] Separate implementation issue exists in GitHub — **blocked by integration HTTP 403. Run `docs/compatibility/linka-looks/publish-follow-up-and-close-n5.sh` from an authenticated developer environment; it creates/reuses the follow-up issue, links it from #110 and closes #110.**

## Decision

The backend Linka Config 2.0 format (`metadata`, `settings`, `blocks[].elements[]`) is **not compatible as-is** with Linka Looks 3.2.10.

A backend archive does not fail fast in Linka Looks. The client successfully reads `config.json`, but because it has neither Looks `pages[]` nor legacy Looks `cards[]`, `normalizeConfigFile` migrates it to a single empty `standard` page. This is silent data loss.

Therefore export to Linka Looks must be explicit and versioned, for example a dedicated `looks-3` export mode/converter. Do not label the current backend `metadata.version = "2.0"` as compatible with the Linka Looks legacy "2.0" format: they are different schemas that happen to use the same version string.

## Reproducible fixture

`testdata/source-config.json` contains two backend task modes:

1. `single_choice` with Cyrillic text, one PNG element and one WAV element;
2. `matching` with two Cyrillic/Latin pairs.

Media are real files:

- `testdata/pixel.png` — 1×1 PNG;
- `testdata/tone.wav` — PCM WAV, mono, 8 kHz.

`testdata/backend-v2-export.linka` is the checked-in golden for the current backend export shape: `config.json` plus both media entries under `media/<uuid>.<ext>`. `internal/pack/linka_looks_compat_test.go` now routes the real GET pattern through `http.ServeMux` into `ContentHandler.Export` + `ContentService.Export`, verifies HTTP headers, ZIP entries, media bytes, block and element order, Cyrillic text, exact media URLs, and exported `config.json`, and can regenerate the golden with `UPDATE_LINKA_LOOKS_FIXTURE=1`.

```bash
go test ./internal/pack -run '^TestLinkaLooksCompatibilityFixture$' -count=1
```

The checked-in regression test was executed successfully in an isolated Go module because the spike container cannot download the full repository's Go 1.25.7 toolchain/modules. The isolated run used byte-identical copies of current `content.go`, `zip.go`, `models.go`, and the exact checked-in `linka_looks_compat_test.go`; only infrastructure/dependency packages were stubbed. It passed twice (normal + `UPDATE_LINKA_LOOKS_FIXTURE=1`) and regenerated `backend-v2-export.linka` through `ContentHandler.Export -> ContentService.Export -> buildArchive`. Evidence and source SHA-256 values are captured in `testdata/backend-http-export-run.json`. The normal full-repository command above must still run in CI/project Go 1.25.7 before merge; `go.mod` remains unchanged.

## Linka Looks execution result

The compatibility result is now confirmed by executing the **exact official parser/model source**, not only by a reimplementation. The spike used Linka Looks `v3.2.10` / commit `b8e65af5825a5a3389e416253393c39d4d5353bd`; `src/common/interfaces/ConfigFile.ts` was verified by Git blob SHA `be3443a89839a04829dd036f2cb5bf493a35e6af`, compiled, and its exported `normalizeConfigFile` function executed against `backend-v2-export.linka`.

```bash
node docs/compatibility/linka-looks/verify-official-parser.mjs \
  /path/to/linka.looks-electron/src/common/interfaces/ConfigFile.ts \
  docs/compatibility/linka-looks/testdata/backend-v2-export.linka
```

Captured official-parser result: `testdata/looks-v3.2.10-official-parser-run.json`. It proves that the ZIP/config parses but becomes one `standard` page with nine `NewCard` placeholders; no source element IDs, media paths, or Cyrillic samples survive normalization.

For the save/round-trip observation, `looks-v3.2.10-harness.mjs` reproduces the pinned `saveSet`/`cleanFile` behavior and records which media/config data are dropped. Captured result: `testdata/looks-v3.2.10-run.json`.

Observed behavior:

| Check | Current backend `.linka` in Looks 3.2.10 |
|---|---|
| ZIP opens / `config.json` parses | **Yes** |
| Backend block order survives | **No** — all source element/block identity disappears |
| Image path is attached to a card | **No** |
| Audio path is attached to a card | **No** |
| Cyrillic element text survives open | **No** |
| `single_choice` mode survives | **No** |
| `matching` mode survives | **No** |
| Looks representation after open | one `standard` 3×3 page with 9 `NewCard` placeholders |
| Save round-trip retains media | **No** — both media entries are removed by `cleanFile` |
| Save round-trip retains source IDs/text | **No** |
| Saved placeholders | 9 `EmptyCard` cards |
| Backend can import the Looks 3.0 save as Linka Config 2.0 | **No** — schemas are different |

Conclusion: **opening succeeds but compatibility fails**. A successful open must not be used as the acceptance signal; semantic preservation is required.


## Verification of the looks-3 converter

Дата проверки: 2026-08-20. Конвертер `linka.ToLooks` и режим экспорта
`GET /packs/{id}/export?format=looks-3` прогнаны через **тот же** официальный
парсер, что и в спайке: клиент склонирован на теге `v3.2.10`, коммит
`b8e65af5825a5a3389e416253393c39d4d5353bd`, блоб `ConfigFile.ts` —
`be3443a89839a04829dd036f2cb5bf493a35e6af`, оба хеша совпали.

Фикстура — та же, что доказывала несовместимость: `single_choice` с кириллицей,
PNG и WAV плюс `matching` из двух пар. Архив собран через реальный HTTP-обработчик
(`internal/pack/linka_looks_export_test.go`, генерация по
`UPDATE_LINKA_LOOKS_FIXTURE=1`) и лежит в `testdata/backend-looks3-export.linka`.

| Проверка | `linka-2` (было) | `looks-3` (стало) |
|---|---|---|
| Архив открывается | да | да |
| Версия формата после открытия | 3.0 (навязана клиентом) | 3.0 |
| Режимы страниц | `standard` — задания потеряны | `quiz`, `match` |
| Идентификаторы элементов дожили | 0 | **10 из 10** |
| Пути медиа привязаны к карточкам | 0 | **2 из 2** |
| Кириллица дожила | 0 | **Ёжик, Кошка, Собака** |
| Медиа после сохранения в Looks | **оба удалены** `cleanFile` | **ничего не удалено** |
| Идентификаторы после сохранения | 0 | **10 из 10** |

Захваченные результаты: `testdata/looks-v3.2.10-looks3-export-run.json`
(официальный парсер) и `testdata/looks-v3.2.10-looks3-roundtrip.json`
(open + save round-trip). Строка `linka-2` в таблице — это повторный прогон
старой фикстуры на той же машине, он воспроизвёл выводы спайка без изменений.

Инструменты спайка при этом уточнены: и верификатор, и harness брали
идентификаторы только из `blocks[]`, поэтому на формате Looks показывали
пустой список. Теперь оба читают и `blocks[].elements[]`, и `pages[].cards[]`.

**Что этим не доказано.** Проверен парсер и модель клиента, а не отрисовка:
логика `setGameLogic.ts` и поведение на экране остаются за рамками. Продуктовые
решения по `multi_choice`, `categories`, `sequence` и пересекающимся графам пар
по-прежнему нужны — сейчас экспорт таких наборов отвечает `409` с указанием
блока вместо частичного результата.

## Compatibility / conversion matrix

| Backend Linka Config 2.0 | Linka Looks 3.0 | Required conversion / loss |
|---|---|---|
| `metadata.version = "2.0"` | `version = "3.0"` | Rewrite version and treat schemas as different namespaces. |
| `metadata.title` | no title field in config | Use archive filename; optionally copy to `description` only if product accepts changed semantics. |
| `settings.columns`, `settings.rows` | per-page `columns`, `rows` | Copy to each generated page. `match` uses two lanes and fixed row semantics. |
| `blocks[]` | `pages[]` | One page per convertible block, preserving block order. |
| `block.id` | `page.id` | Preserve string ID where possible. |
| `block.name` | no equivalent | Not converted; Looks 3.2.10 `normalizePage` builds pages from an explicit field list and drops unknown properties. |
| `grid` | `mode: "standard"` | Direct structural mapping. |
| `single_choice` | `mode: "quiz"` | `answers[].is_correct=true` → card `answer: true`; Looks question has no backend source and remains empty unless a rule is added. |
| `multi_choice` | no lossless equivalent | Looks quiz advances after any card with `answer: true`; it does not require selecting the full set. Must reject or define explicit degradation. |
| `matching` + `pairs[]` | `mode: "match"`, `matchId`, lanes | Possible only when pair graph can be represented by Looks match groups. Arbitrary overlapping pairs may require duplicated cards and are potentially lossy. |
| `categories` | no page mode | Unsupported/lossy; product decision required. |
| `sequence` | no page mode | Card order can be preserved visually, but sequence-task semantics are lost; product decision required. |
| element `id` | card `id` | Preserve where one element maps to one card. |
| `kind: text`, `value` | `cardType: 0`, `title` | Map to an active card with `title=value`. |
| `kind: image`, `media_url` | `cardType: 0`, `imagePath` | Map exported relative ZIP path to `imagePath`. |
| `kind: audio`, `media_url` | `cardType: 0`, `audioPath` | Map exported relative ZIP path to `audioPath`. |
| `media_id` | no equivalent | Lost in Looks config; path is sufficient for playback, but backend identity is not round-trippable without extension metadata. |
| `source_picture_id` | no equivalent | Lost unless carried in versioned extension metadata. |
| `answers[].is_correct=false` | no field needed | Omit `answer`; only true answers are marked. |
| `pairs[].left_id/right_id` | one `matchId` per card | Requires grouping/reordering into top/bottom lanes; not every arbitrary pair graph is representable losslessly. |

## Required follow-up

A separate implementation issue for a versioned Linka Looks 3.0 converter/export mode is required. Its complete body is stored in `FOLLOW-UP-ISSUE.md`; it uses this fixture as the golden compatibility test and explicitly rejects or defines degradation for backend task types that Linka Looks cannot represent losslessly.

Creation was attempted through the connected GitHub integration on 2026-08-13, but GitHub returned HTTP 403 `Resource not accessible by integration`. The integration currently lacks Issues write permission for `Linka-masterskaya/zip-backend`, so **#110 must not be closed until that issue is actually created** by an account/integration with write access.

Suggested estimate:

- export converter + route/content negotiation + fixture tests: **12–16 h**;
- if true Looks-save → backend-import round-trip is required, add a Looks 3.0 import converter: **+8 h**;
- product decisions for `multi_choice`, `categories`, `sequence`, and non-representable matching graphs are prerequisites to claiming lossless support.
