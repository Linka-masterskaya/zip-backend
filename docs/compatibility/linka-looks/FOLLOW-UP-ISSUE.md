# [N5 follow-up] Versioned Linka Looks 3.0 export converter

## Контекст

Spike #110 подтвердил несовместимость текущего backend Linka Config 2.0 (`metadata/settings/blocks[].elements[]`) с реально поддерживаемым Linka Looks 3.2.10 (set format `3.0`, `pages[].cards[]`). Текущий `.linka` формально открывается клиентом, но `blocks` игнорируются: набор нормализуется в пустую `standard`-страницу, а при save round-trip исходные media удаляются.

Это отдельная задача от #83 (streaming).

## Цель

Добавить **versioned converter/export mode** для Linka Looks 3.0, не меняя внутренний canonical Linka Config 2.0.

Предлагаемый API-контракт (финальное имя параметра можно согласовать отдельно):

- текущий `GET /api/v1/packs/{id}/export` сохраняет canonical backend export;
- явный режим, например `GET /api/v1/packs/{id}/export?format=looks-3`, отдаёт Linka Looks 3.0.

Не делать silent auto-conversion без версии формата.

## Mapping

| Backend 2.0 | Looks 3.0 | Правило |
|---|---|---|
| `metadata.version="2.0"` | `version="3.0"` | Обязательная смена schema/version namespace. |
| `metadata.title` | нет поля title | Имя архива; `description` только при явном продуктовом решении. |
| `settings.columns/rows` | page `columns/rows` | Копировать на каждую сгенерированную страницу. |
| `blocks[]` | `pages[]` | Один convertible block → одна page, порядок сохранять. |
| `block.id` | `page.id` | Сохранять строковый ID. |
| `grid` | `mode=standard` | Прямая структурная конвертация. |
| `single_choice` | `mode=quiz` | `is_correct=true` → card `answer=true`; `question` сейчас неоткуда заполнить. |
| `multi_choice` | нет lossless equivalent | Looks quiz завершает вопрос по одной выбранной `answer=true`; reject или явно задокументированный degraded mode. |
| `matching` + `pairs[]` | `mode=match`, `matchId`, lanes | Работает только для графов, представимых Looks match groups; произвольные overlapping pairs потенциально требуют duplicate cards/дают лишние связи. |
| `categories` | нет page mode | Reject либо отдельное продуктовое правило деградации. |
| `sequence` | нет page mode | Визуальный порядок можно сохранить, task semantics — нет; reject либо отдельное правило. |
| element `id` | card `id` | Сохранять при 1:1 mapping. |
| text `value` | `cardType=0`, `title` | Текстовая карточка. |
| image `media_url` | `cardType=0`, `imagePath` | Relative ZIP path. |
| audio `media_url` | `cardType=0`, `audioPath` | Relative ZIP path. |
| `media_id` | нет поля | Потеря backend identity, если не добавить versioned extension metadata. |
| `source_picture_id` | нет поля | Потеря, если не добавить extension metadata. |
| `answers[].is_correct=false` | поле не требуется | `answer` не ставить. |

## Fixtures / tests

Использовать fixture из spike #110:

- Cyrillic text: `Ёжик`, `Кошка`, `Собака`;
- real PNG;
- real WAV;
- `single_choice` + `matching`;
- deterministic media UUIDs;
- проверка card/page order, `imagePath`, `audioPath`, UTF-8, mode semantics и round-trip.

Добавить golden test, который:

1. экспортирует через реальный `GET /api/v1/packs/{id}/export?format=looks-3`;
2. открывает результат parser semantics Linka Looks 3.2.10;
3. проверяет отсутствие silent loss для поддержанных mappings;
4. проверяет явный 4xx для неподдержанных lossless mappings (`multi_choice/categories/sequence` и непредставимый matching), пока продукт не утвердит деградацию.

## Round-trip

Если требование означает `backend -> Looks save -> backend import` без потерь, одного export converter недостаточно: текущий backend import ожидает canonical Linka Config 2.0 и не принимает Looks 3.0. Тогда в этой же версии нужен Looks 3.0 import converter либо отдельная следующая issue.

## Acceptance criteria

- [ ] canonical backend 2.0 остаётся внутренним форматом;
- [ ] Looks export выбирается явно по версии;
- [ ] `grid`, согласованный `single_choice` и представимый `matching` конвертируются детерминированно;
- [ ] media paths существуют в ZIP и совпадают с `imagePath`/`audioPath`;
- [ ] кириллица и порядок сохраняются;
- [ ] неподдержанные lossless случаи не деградируют молча;
- [ ] fixture #110 проходит автоматизированную compatibility проверку;
- [ ] OpenAPI документирует export format/version и ошибки conversion.

## Оценка

- export converter + API mode + tests/OpenAPI: **12–16 ч**;
- Looks 3.0 import converter для настоящего save/import round-trip: **+8 ч**;
- продуктовые решения по `multi_choice`, `categories`, `sequence` и сложным matching-графам — prerequisite для заявления полной lossless-совместимости.

Связано: #110, #83.
