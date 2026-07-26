# TEMP — Карточки задач: наборы, карточки, медиа, архивы

Статус: draft. Номера AB-40..AB-55 предложены для публикации после подтверждения.

База: `TEMP-packs-spec.md`.

## Результат перепроверки

План `D/M/E + students/settings` в целом правильный, но с тремя обязательными поправками:

1. Модель `config.json` должна быть Linka 2.0: `pack -> blocks -> elements`, а не плоский `cards[]`.
2. Нужна отдельная задача на адаптированные версии набора под ученика.
3. `4.7.4 Дублировать` не является копией набора. Копия набора относится к `4.6.2 Поделиться`/выбор папки.
4. Архивы и медиа связаны: нужен учёт media usages из `config`, иначе экспорт и удаление файлов будут ненадёжными.

## Карта зависимостей

```
AB-40 migrations ─┬─ AB-42 folders
                  ├─ AB-43 packs ───┬─ AB-45 search
                  │                 ├─ AB-46a favorites
                  │                 └─ AB-53/54/55 archives/share
                  └─ AB-48 settings

existing students schema ── AB-47 students ── AB-46b adaptations

AB-41 linka schema ─┬─ AB-44 config/versioning
                    ├─ AB-46b adaptations
                    ├─ AB-53 export
                    └─ AB-54 import

AB-49 media ─┬─ AB-51 TTS
             ├─ AB-52 pictures bank integration
             └─ AB-53/54 export/import
```

## Трек D — домен: папки, наборы, карточки

### AB-40 — Миграции: folders, pack metadata, publication, favorites, adaptations

Связь: `2.1`, `2.2`, `2.3`, `4.1.2`, `4.1.3`, `4.1.5`, `4.2`, `4.4`, `4.5`, `4.6.2`, `4.9.2`, `4.9.4`, ТЗ 2.0 adaptation.

Описание:
Добавить SQL-фундамент для файловой системы продукта: папки, привязка набора к папке, фильтры, избранное и адаптированные версии под ученика.

Сделать:
- `folders` с `org_id`, `section=library|my|students`, `kind=folder|student`, `parent_id`, `student_id`, `name`, `depth`.
- `parent_id` и `student_id` используют `ON DELETE RESTRICT`, чтобы удаление непустой папки или ученика с папкой завершалось конфликтом, а не каскадом.
- Расширить `packs`: `folder_id`, `library_folder_id`, `published_at`, `age_min`, `age_max`, `difficulty`, `goals`, `notes`.
- `favorite_packs`.
- `pack_adaptations`.
- `media_usages` для ссылок `config.elements[].imageId/audioId`.
- Индексы по `org_id`, `owner_id`, `folder_id`, `title`, `difficulty`, возрасту.

Критерии готовности:
- Goose up/down работают.
- Миграции накатываются поверх схемы текущего `main`, а не только на чистую базу.
- Нельзя создать некорректный `difficulty`.
- Удаление набора чистит favorites/versions каскадом.
- Миграции не ломают существующие auth/profile тесты.

### AB-41 — `pkg/linka`: JSON Schema 2.0 + Go-валидатор

Связь: `4.7`, `4.8`, `4.9`, `5.1`..`5.5`, ТЗ 2.0, V1 §5.4/§5.6.

Описание:
Описать и валидировать новый `config.json`: `metadata`, `settings`, `blocks[]`, `elements[]`, режимы заданий, медиа-ссылки, ответы, пары, категории, последовательности.

Сделать:
- JSON Schema 2020-12 в `pkg/linka`.
- Go API `ValidateConfig(ctx, json.RawMessage) error`.
- Типы/константы для `block.type` и `element.kind`.
- Тестовые fixtures: валидный grid, single_choice, multi_choice, matching, categories, sequence; невалидные cases.

Критерии готовности:
- `single_choice` требует ровно один правильный ответ.
- `multi_choice` требует минимум один правильный ответ.
- `sequence` требует уникальный `order`.
- `rows/columns` в диапазоне 1..100.
- Формат "open answer" не принимается.

### AB-42 — Folders CRUD

Связь: `4.1.2`, `4.1.3`, `4.2.2`, `4.4.2`, `4.4.4`.

Описание:
Реализовать backend папок и разделов.

API:
- `POST /api/v1/folders`
- `GET /api/v1/folders?section=&parent_id=`
- `GET /api/v1/folders/{id}/contents`
- `PATCH /api/v1/folders/{id}`
- `DELETE /api/v1/folders/{id}`
- `POST /api/v1/folders/{id}/move`

Критерии готовности:
- Пользователь видит только папки своей org/owner scope.
- Нельзя переместить папку в своего потомка.
- Parent и child имеют одинаковые `section`/`org_id`; в private-разделах совпадает `owner_id`.
- В library автор создаёт и перемещает папки только внутри своего дерева; admin может работать со всем деревом.
- Move проверяет максимальную глубину всего поддерева и выполняет пересчёт в одной транзакции.
- Для `kind=student` проверяется существование ученика.
- Для `kind=student` проверяется, что ученик принадлежит владельцу папки.
- `contents` возвращает один дискриминированный и стабильно отсортированный список `folders + packs` с pagination.
- Поведение удаления зафиксировано: v1 запрещает удалять непустую папку.

### AB-43 — Pack CRUD на Postgres вместо Redis stub

Связь: `4.1.4`, `4.4.3`, `4.6.3`, `4.6.4`, `4.7.1`.

Описание:
Заменить текущий `internal/pack` stub на полноценный Postgres CRUD.

API:
- `POST /api/v1/packs`
- `GET /api/v1/packs/{id}`
- `GET /api/v1/packs?folder_id=`
- `PATCH /api/v1/packs/{id}`
- `DELETE /api/v1/packs/{id}`
- `POST /api/v1/packs/{id}/move`
- `POST /api/v1/packs/{id}/publication`
- `DELETE /api/v1/packs/{id}/publication`

Критерии готовности:
- `pack.Repository` работает через `pgxpool`, не через Redis.
- Создание набора кладёт пустой валидный `config` Linka 2.0.
- `PATCH` меняет title/folder/filter metadata/notes без изменения `config`.
- Move проверяет org/owner/section целевой папки.
- Публикация сохраняет ссылку на library folder без копирования набора; снятие публикации сохраняет исходный набор.
- Автор публикует только в свою library-папку; admin может публиковать в любую.
- Повторная публикация в ту же папку идемпотентна, в другую возвращает 409 до снятия текущей.
- Library contents возвращает только опубликованные наборы.
- Удаление опубликованного набора возвращает 409.
- Все операции проверяют org/user доступ.
- Покрытие handler/service/repository тестами по паттернам auth/profile.

### AB-44 — Сохранение config + техническая история версий

Связь: `4.7.9`, `4.9.6`, V1 "История версий и откат", ТЗ 2.0 "автосохранение".

Описание:
Фронт присылает весь `config`, backend валидирует через AB-41, сохраняет в `packs.config` и пишет снапшот в `pack_versions`.

API:
- `PUT /api/v1/packs/{id}/config`
- `GET /api/v1/packs/{id}/versions`
- `POST /api/v1/packs/{id}/versions/{version}/restore`

Критерии готовности:
- Невалидный config возвращает 400 с понятным кодом ошибки.
- Каждое сохранение увеличивает `pack_versions.version`.
- При сохранении пересобирается `media_usages` для pack/adaptation.
- Restore пишет новый снапшот, а не переписывает историю.
- Конкурентные сохранения не создают одинаковый version.

### AB-45 — Поиск и фильтры наборов

Связь: `2.1`, `2.2`, `4.9.2`.

Описание:
Список наборов по всем папкам пользователя/организации с поиском по названию и фильтрами.

API:
- `GET /api/v1/packs?query=&age=&difficulty=&section=`

Критерии готовности:
- Поиск по title case-insensitive.
- Возраст `age=5` матчится по `age_min <= 5 <= age_max`.
- Фильтр сложности: `easy|medium|hard`.
- Ответ содержит `is_favorite`, `folder_id`, `section`.

### AB-46a — Избранные наборы

Связь: `2.3`.

API:
- `PUT /api/v1/packs/{id}/favorite`
- `DELETE /api/v1/packs/{id}/favorite`
- `GET /api/v1/favorites/packs`

Критерии готовности:
- Повторный favorite idempotent.
- Пользователь может добавлять в избранное только доступный ему набор.
- Удаление набора чистит favorite каскадом.

### AB-46b — Адаптированные версии под ученика

Связь: `4.5.4`, `4.6.2`, ТЗ 2.0 "Создать версию".

Описание:
Добавить бизнес-версии набора для конкретного ученика.

API:
- `POST /api/v1/packs/{id}/adaptations`
- `GET /api/v1/packs/{id}/adaptations`
- `GET /api/v1/adaptations/{id}`
- `PUT /api/v1/adaptations/{id}/config`
- `DELETE /api/v1/adaptations/{id}`

Критерии готовности:
- Создание adaptation копирует текущий `packs.config`.
- Adaptation привязана к `student_id`.
- Сохранение adaptation валидируется той же схемой AB-41.

## Трек S — ученики и настройки

### AB-47 — Students CRUD

Связь: `4.5.1`..`4.5.7`.

Описание:
Сделать backend картотеки учеников поверх существующей таблицы `students`.

Сделать:
- Миграция `students.last_lesson_at DATE`.
- `POST /api/v1/students`
- `GET /api/v1/students`
- `PATCH /api/v1/students/{id}`
- `DELETE /api/v1/students/{id}`

Критерии готовности:
- Статусы: `active`, `one_time`, `paused`, `archived`.
- Delete soft-delete через `deleted_at`.
- Если у ученика есть папка, delete возвращает 409; FK не удаляет папку каскадом.
- Email хранится шифрованно по существующему `cryptox`.
- Сортировка email на SQL не делается из-за encryption; API допускает сортировку по name/age/status/last_lesson_at.

### AB-48 — User settings + color templates

Связь: `3.1.1`..`3.1.7`, `3.2.1`..`3.2.6`, `3.3`.

Описание:
Настройки сайта должны переживать релогин и другое устройство: управление глазами, активация карточки, интерактивность, голос, направление кнопок, цвета, толщина границ, шаблоны.

API:
- `GET /api/v1/settings`
- `PUT /api/v1/settings`
- `GET /api/v1/settings/templates`
- `POST /api/v1/settings/templates`
- `DELETE /api/v1/settings/templates/{id}`

Критерии готовности:
- `settings` хранится как JSONB.
- Backend валидирует базовую форму: объект, лимит размера, допустимые ключи верхнего уровня.
- Templates имеют name и JSON body.
- `voice` должен соответствовать голосу из TTS/voices, если список доступен.

## Трек M — медиа и банк аудио/изображений

### AB-49 — Media upload/list/delete: MinIO, dedup, quota

Связь: `4.8.4`, `4.8.6`, V1 Media-Service.

Описание:
Реализовать загрузку пользовательских изображений и аудио. Таблица `media_files` уже есть, MinIO client уже есть, но `internal/media` пустой.

API:
- `POST /api/v1/media` multipart
- `GET /api/v1/media?type=image|audio&query=`
- `GET /api/v1/media/{id}/url`
- `DELETE /api/v1/media/{id}`

Критерии готовности:
- SHA-256 дедуп по `(org_id, sha256)`.
- Квота 10 GiB/org.
- MIME allowlist: images и audio.
- Presigned URL для чтения.
- NATS job на ClamAV scan или explicit TODO, если ClamAV ещё не поднят.

### AB-50 — Audio bank operations

Связь: `4.8.4`, `4.8.7`.

Описание:
Поверх AB-49 оформить сценарии банка аудио: загрузить, выбрать из ранее загруженных, заменить, удалить, проиграть через presigned URL.

Критерии готовности:
- `GET /media?type=audio` возвращает метаданные и URL/URL endpoint.
- Удаление не ломает наборы: если аудио используется в config, возвращать 409 или soft-delete policy.
- В спецификации явно описано, что привязка к карточке хранится в `config.elements[].audioId`.

### AB-51 — TTS из текста

Связь: `4.8.5`, `4.8.8`, `4.9.3`, V1 AI-Service.

Описание:
Создание mp3 из текста: endpoint публикует задачу в NATS, ai-worker генерит файл, media module сохраняет результат в MinIO и `media_files`.

API:
- `POST /api/v1/tts`
- `GET /api/v1/tts/{job_id}`
- `GET /api/v1/tts/voices`

Критерии готовности:
- Request: text, voice, optional pack_id/element_id.
- Response: job_id.
- Завершённая задача возвращает `media_id`.
- Голоса используются в settings и pack settings.

### AB-52 — Pictures Bank integration

Связь: `4.8.6`, V1 §5.3/§5.7.

Описание:
Прокси/клиент к Pictures Bank: категории, поиск, получение картинки, сохранение `pictureId`/media reference в config.

API:
- `GET /api/v1/pictures/categories`
- `GET /api/v1/pictures/search?query=`
- `GET /api/v1/pictures/{id}/buffer` или proxy URL

Критерии готовности:
- Bearer auth к внешнему Pictures Bank из config.
- Таймауты и нормализация ошибок.
- Кэширование категорий допустимо, но не обязательно.

## Трек E — архивы и отправка

### AB-53 — Export `.linka`

Связь: `4.6.2`, V1 §5.4/§5.6.

Описание:
Собрать ZIP с расширением `.linka`: `config.json` + медиафайлы, на которые ссылается config.

API:
- `GET /api/v1/packs/{id}/export`
- возможно `GET /api/v1/adaptations/{id}/export`

Критерии готовности:
- Перед экспортом config валидируется AB-41.
- ZIP стримится, не грузится целиком в память.
- Лимит архива 50 MiB.
- Нет недостающих media references; при отсутствии файла 409.
- Content-Type/filename корректны.

### AB-54 — Import `.linka`

Связь: обратный сценарий для `.linka`; в фичлисте явно не указан, но нужен для полноценного обмена файлами.

Описание:
Загрузка `.linka` архива, валидация `config.json`, импорт медиа в MinIO с дедупом, создание нового pack.

API:
- `POST /api/v1/packs/import`

Критерии готовности:
- Принимается только ZIP/.linka до 50 MiB.
- Path traversal в ZIP запрещён.
- `config.json` обязателен и валиден.
- Медиа дедуплицируются через `media_files`.
- Новый pack создаётся в выбранной folder_id.

### AB-55 — Share pack

Связь: `4.5.4`, `4.6.2`, figma "быстрая отправка набора".

Описание:
Модальное окно выбора ученика/папки: при выборе папки копируем набор, при выборе ученика отправляем `.linka` на email и/или создаём adaptation.

API:
- `POST /api/v1/packs/{id}/share`

Критерии готовности:
- `target_type=folder` создаёт copy pack в folder.
- `target_type=student` проверяет student, собирает `.linka`, отправляет email через существующий mailer.
- Поиск ученика/папки покрыт существующими list endpoints.
- PDF вложение не входит в v1, если продукт отдельно не подтвердит.

## Что не ставим отдельными backend задачами

- Кнопка "Добавить" карточки (`4.7.3`) — изменение `config` на фронте.
- Дублировать карточки/страницы (`4.7.4`, `4.7.6`) — изменение `config` на фронте.
- Переключение между страницами (`4.7.7`) — frontend state.
- Кнопки типа карточки (`4.7.8`, `4.8.1`, `4.8.2`) — изменение `config` на фронте.
- Режимы `5.x` как отдельные endpoints — валидируются схемой AB-41 и сохраняются через AB-44.
