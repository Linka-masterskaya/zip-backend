# TEMP — Packs, Cards, Media, Archives: архитектура и API

Статус: draft. Ничего не опубликовано в GitHub.

Источники сверки:
- `docs/Linka  командное пространство (1).xlsx`, лист `Фич лист`.
- `docs/Linka_Editor_2_0_TZ.docx` — Linka Editor 2.0.
- `docs/Документация Linka Editor V1-2.docx` — базовый `.linka`/инфра контракт.
- Текущий код: `internal/pack`, `internal/media`, `pkg/linka`, `migrations/*`.

## 1. Что подтверждено перепроверкой

1. Backend нужен не только для папок/наборов. По фичлисту и ТЗ 2.0 также нужны ученики, настройки сайта, медиа, TTS, `.linka` экспорт/импорт и версии для адаптации под ребёнка.
2. `4.7.4 Дублировать` — это дублирование настроенных карточек/страниц внутри набора, то есть операция над `config`. Это не копия всего набора. Копия всего набора нужна из `4.6.2 Поделиться` при выборе папки.
3. Linka Editor 2.0 меняет модель: формат должен поддерживать `pack -> blocks -> elements`, несколько типов заданий внутри одного набора и режим адаптации под ученика.
4. Формат "открытый ответ" не поддерживается. Поддерживаемые типы заданий: один ответ, несколько ответов, сопоставление, распределение по категориям, последовательность.
5. Старый `.linka` контракт остаётся обязательным: ZIP с `config.json` и медиа, JSON Schema 2020-12, лимит архива 50 MiB, лимит хранилища 10 GiB на организацию.

## 2. Архитектурное решение

Backend хранит набор как объект и его содержимое как валидируемый JSON:

```
section/folder -> pack -> config blocks/elements -> media
                         -> adaptations for students
                         -> technical snapshots in pack_versions
```

Канон содержимого набора — `packs.config` JSONB. В этот JSON входят блоки, элементы, режимы, правильные ответы, пары, категории, порядок, настройки отображения и ссылки на медиа. Тот же JSON кладётся в `.linka/config.json`.

В SQL выносим только то, по чему нужен быстрый список/поиск/фильтр/доступ:
- папка и раздел;
- название;
- возраст;
- сложность;
- цели занятия;
- заметки;
- избранное;
- привязка адаптированной версии к ученику;
- метаданные медиа.

Почему не нормализуем карточки в таблицы: иначе появится две правды — JSON для `.linka` и SQL для редактора. Для редактора проще и безопаснее сохранять полный `config` целиком, а backend валидирует схему и делает снапшот.

## 3. Текущая база и пробелы

Уже есть:
- `packs(id, org_id, owner_id, title, status, config JSONB, created_at, updated_at)`.
- `pack_versions(pack_id, version, config, created_by, created_at)` — техническая история сохранений.
- `media_files(org_id, uploader_id, sha256, mime_type, size_bytes, minio_key)` — база для дедупликации.
- `students(defectologist_id, email_encrypted, name, age, status, ...)`.

Не хватает:
- дерева папок и корневых разделов;
- `folder_id`, `age_min`, `age_max`, `difficulty`, `goals`, `notes` у `packs`;
- избранного;
- бизнес-версий/адаптаций набора под ученика;
- учёта использования медиа в наборах;
- `students.last_lesson_at`;
- настроек пользователя и шаблонов цвета;
- полноценного Postgres repository для `pack`;
- JSON Schema 2.0 в `pkg/linka`;
- CRUD для media/upload/export/import.

## 4. Предлагаемые миграции

### folders

```sql
CREATE TABLE folders (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id),
    owner_id    UUID NOT NULL REFERENCES users(id),
    parent_id   UUID REFERENCES folders(id) ON DELETE RESTRICT,
    section     TEXT NOT NULL CHECK (section IN ('library', 'my', 'students')),
    kind        TEXT NOT NULL CHECK (kind IN ('folder', 'student')),
    student_id  UUID REFERENCES students(id) ON DELETE RESTRICT,
    name        TEXT NOT NULL,
    depth       INT NOT NULL CHECK (depth BETWEEN 0 AND 4),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT folders_kind_student_chk CHECK (
        (kind = 'student' AND student_id IS NOT NULL AND section = 'students') OR
        (kind = 'folder' AND student_id IS NULL)
    )
);
```

Правила:
- `parent_id IS NULL` означает элемент в корневом разделе.
- `kind='student'` допускается только в `section='students'`.
- Нельзя переместить папку внутрь самой себя или своего потомка.
- Parent и child должны иметь одинаковые `section` и `org_id`; в `my`/`students` должен совпадать `owner_id`.
- В `library` автор создаёт и перемещает папки только внутри своего дерева; `admin` может работать со всем деревом. Чтение библиотеки глобальное и не фильтруется по `org_id`.
- Ученик должен принадлежать владельцу student folder.
- При move проверяется максимальная глубина всего поддерева, пересчёт выполняется транзакционно.
- Удалять можно только пустую папку; FK не удаляют дочерние папки или student folder каскадом.

### packs extension

```sql
ALTER TABLE packs
    ADD COLUMN folder_id UUID REFERENCES folders(id),
    ADD COLUMN library_folder_id UUID REFERENCES folders(id),
    ADD COLUMN published_at TIMESTAMPTZ,
    ADD COLUMN age_min INT CHECK (age_min IS NULL OR age_min BETWEEN 3 AND 18),
    ADD COLUMN age_max INT CHECK (age_max IS NULL OR age_max BETWEEN 3 AND 18),
    ADD COLUMN difficulty TEXT CHECK (difficulty IN ('easy', 'medium', 'hard')),
    ADD COLUMN goals TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN notes TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT packs_age_range_chk CHECK (
        age_min IS NULL OR age_max IS NULL OR age_min <= age_max
    ),
    ADD CONSTRAINT packs_published_chk CHECK (
        (library_folder_id IS NULL) = (published_at IS NULL)
    );
```

`folder_id` указывает только на `my`/`students` того же owner/org, `library_folder_id` — только на собственную папку автора в `library` (для `admin` допустима любая). Эти межтабличные инварианты проверяет service. Публикация добавляет ссылку и не создаёт копию набора.

### favorites

```sql
CREATE TABLE favorite_packs (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    pack_id    UUID NOT NULL REFERENCES packs(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, pack_id)
);
```

Избранные карточки в фичлисте упомянуты, но карточка живёт внутри JSON. Для v1 фиксируем только избранные наборы. Избранные элементы можно добавить позже как `(user_id, pack_id, element_id)`, когда фронт подтвердит UX.

### pack_adaptations

```sql
CREATE TABLE pack_adaptations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_pack_id UUID NOT NULL REFERENCES packs(id) ON DELETE CASCADE,
    student_id  UUID NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    config      JSONB NOT NULL,
    difficulty  TEXT CHECK (difficulty IN ('easy', 'medium', 'hard')),
    notes       TEXT NOT NULL DEFAULT '',
    created_by  UUID NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Это бизнес-версия для ребёнка из ТЗ 2.0. Не путать с `pack_versions`, где лежат технические снапшоты сохранений одного набора.

### media_usages

```sql
CREATE TABLE media_usages (
    media_id      UUID NOT NULL REFERENCES media_files(id) ON DELETE CASCADE,
    pack_id       UUID REFERENCES packs(id) ON DELETE CASCADE,
    adaptation_id UUID REFERENCES pack_adaptations(id) ON DELETE CASCADE,
    element_id    TEXT NOT NULL,
    usage_type    TEXT NOT NULL CHECK (usage_type IN ('image', 'audio')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((pack_id IS NOT NULL) <> (adaptation_id IS NOT NULL)),
    PRIMARY KEY (media_id, element_id, COALESCE(pack_id, adaptation_id))
);
```

Таблица пересобирается при сохранении `config`: backend извлекает все `imageId`/`audioId` из блоков и элементов. Она нужна для экспорта `.linka`, запрета удаления используемых файлов и диагностики битых ссылок.

### students/settings

```sql
ALTER TABLE students ADD COLUMN last_lesson_at DATE;

CREATE TABLE user_settings (
    user_id    UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    settings   JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE user_setting_templates (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    template   JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## 5. `config.json` 2.0 skeleton

```json
{
  "version": "2.0",
  "metadata": {
    "title": "Животные",
    "ageMin": 3,
    "ageMax": 6,
    "difficulty": "easy",
    "goals": ["лексика"],
    "description": ""
  },
  "settings": {
    "cardAlign": "center",
    "withoutSpace": false,
    "directSet": false,
    "quiz": false,
    "quizAutoNext": true,
    "quizReadQuestion": false,
    "printTextMode": false,
    "hideInputAndReadOnSelect": false
  },
  "blocks": [
    {
      "id": "uuid",
      "type": "single_choice",
      "title": "",
      "layout": { "rows": 2, "columns": 3 },
      "elements": [
        {
          "id": "uuid",
          "kind": "normal",
          "text": "кот",
          "imageId": "uuid",
          "audioId": "uuid",
          "correct": true,
          "order": 1,
          "pairId": null,
          "categoryId": null
        }
      ]
    }
  ]
}
```

Ограничения схемы:
- `layout.rows` и `layout.columns`: 1..100.
- `block.type`: `grid`, `sequence`, `single_choice`, `multi_choice`, `matching`, `categories`.
- `element.kind`: `normal`, `empty`, `text`, `space`.
- Для `single_choice` ровно один `correct=true`.
- Для `multi_choice` один или больше `correct=true`.
- Для `sequence` у элементов должен быть уникальный `order`.
- Для `matching` нужны пары/связи.
- Для `categories` нужны категории и привязка элементов к категориям.

## 6. API outline

### Folders

- `POST /api/v1/folders`
- `GET /api/v1/folders?section=my&parent_id=...`
- `GET /api/v1/folders/{id}/contents` returns `folders + packs`
- `PATCH /api/v1/folders/{id}`
- `DELETE /api/v1/folders/{id}`
- `POST /api/v1/folders/{id}/move`

### Packs

- `POST /api/v1/packs`
- `GET /api/v1/packs/{id}`
- `GET /api/v1/packs?folder_id=...&query=...&age=...&difficulty=...`
- `PATCH /api/v1/packs/{id}`
- `DELETE /api/v1/packs/{id}`
- `POST /api/v1/packs/{id}/move`
- `POST /api/v1/packs/{id}/publication`
- `DELETE /api/v1/packs/{id}/publication`
- `POST /api/v1/packs/{id}/copy`
- `PUT /api/v1/packs/{id}/config`
- `GET /api/v1/packs/{id}/versions`
- `POST /api/v1/packs/{id}/versions/{version}/restore`
- `PUT /api/v1/packs/{id}/favorite`
- `DELETE /api/v1/packs/{id}/favorite`
- `GET /api/v1/favorites/packs`

### Adaptations

- `POST /api/v1/packs/{id}/adaptations`
- `GET /api/v1/packs/{id}/adaptations`
- `GET /api/v1/adaptations/{id}`
- `PUT /api/v1/adaptations/{id}/config`
- `DELETE /api/v1/adaptations/{id}`

### Students

- `POST /api/v1/students`
- `GET /api/v1/students`
- `PATCH /api/v1/students/{id}`
- `DELETE /api/v1/students/{id}`

### Settings

- `GET /api/v1/settings`
- `PUT /api/v1/settings`
- `GET /api/v1/settings/templates`
- `POST /api/v1/settings/templates`
- `DELETE /api/v1/settings/templates/{id}`

### Media

- `POST /api/v1/media` multipart upload
- `GET /api/v1/media?type=image|audio&query=...`
- `GET /api/v1/media/{id}/url`
- `DELETE /api/v1/media/{id}`
- `POST /api/v1/tts`
- `GET /api/v1/tts/voices`
- `GET /api/v1/pictures/categories`
- `GET /api/v1/pictures/search?query=...`

### Archives/share

- `GET /api/v1/packs/{id}/export`
- `POST /api/v1/packs/import`
- `POST /api/v1/packs/{id}/share`

## 7. Что считаем frontend-only

Backend не должен иметь отдельные endpoints для каждой кнопки редактора:
- добавить карточку;
- дублировать карточку/страницу;
- копировать/вырезать/вставить страницу;
- переключить режим блока;
- изменить текст карточки;
- поставить галочку правильного ответа.

Фронт меняет `config`, backend валидирует весь документ и сохраняет новую версию.

## 8. Открытые продуктовые вопросы

1. Избранные карточки: делаем только избранные наборы в v1 или нужен backend для отдельных `element_id`?
2. PDF в "поделиться": в v1 отправляем только `.linka`, PDF откладываем?
3. Публичная библиотека: это read-only seed-контент или пользователь может публиковать туда наборы?
4. Импорт `.linka`: нужен в первом релизе? Технически логично брать вместе с экспортом, но в фичлисте явно не указан.
