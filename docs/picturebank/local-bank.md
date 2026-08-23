# Local Pictures Bank operations

The local adapter implements the same `picturebank.Source` contract as the external Pictures Bank:

- `GET /api/v1/pictures/categories`
- `GET /api/v1/pictures/search?query=...`
- `GET /api/v1/pictures/{id}/content`
- `POST /api/v1/pictures/{id}/import`

All public routes remain authenticated and use the existing Pictures Bank rate-limit policy. Image bytes are always proxied by the backend. Neither search results nor import responses expose MinIO keys or URLs.

`PictureCategory.id` is an opaque adapter-specific string. Frontend code must not parse or validate it as a UUID. In local mode the stable category ID is the category name itself; search results and the categories endpoint use the same ID.

## Enable local mode

`feature_flags.local_bank` is intentionally file-owned and cannot be overridden by environment variables. Set it in the selected YAML config:

```yaml
pictures_bank:
  # url is not required in local mode
  url: ""
  max_image_bytes: 10485760

feature_flags:
  local_bank: true
```

With `local_bank: true`, startup does not construct the external Pictures Bank client and does not require `pictures_bank.url` or external credentials. PostgreSQL and MinIO remain required because they back the local adapter.

Run database migrations before seeding content. The normal migration command uses `CONFIG_PATH`:

```bash
CONFIG_PATH=config/config.dev.yml go run ./cmd/migrate
```

## Add system content

Supported image types are PNG, JPEG, WebP and GIF. The same `pictures_bank.max_image_bytes` limit used for serving images is applied during ingestion.

```bash
go run ./cmd/picturebank-seed add \
  -config config/config.dev.yml \
  -file ./seed/cat.png \
  -category "Животные" \
  -title "Кот"
```

The command prints the generated picture UUID. To preserve an existing `source_picture_id` during an adapter/content migration, provide a stable UUID explicitly:

```bash
go run ./cmd/picturebank-seed add \
  -config config/config.dev.yml \
  -id 11111111-2222-3333-4444-555555555555 \
  -file ./seed/cat.png \
  -category "Животные" \
  -title "Кот"
```

Objects are written only as `system/pictures-bank/<uuid>`. They do not create `media_files` rows and do not change `organizations.storage_used_bytes`.

If MinIO reports an ambiguous upload failure, the command performs a bounded best-effort removal of the reserved object key before removing PostgreSQL metadata. If object cleanup itself fails, metadata is deliberately kept and the error contains the picture UUID so an operator can retry `delete` without leaving an invisible orphan.

## Delete system content

```bash
go run ./cmd/picturebank-seed delete \
  -config config/config.dev.yml \
  -id 11111111-2222-3333-4444-555555555555
```

After deletion, the existing content proxy returns the same HTTP 200 deleted-picture placeholder behavior used for a picture removed from the external bank. Existing pack configs therefore do not need migration.

The delete command refuses metadata that points outside `system/pictures-bank/`.

## PostgreSQL schema

`picture_bank_images` stores:

- `id`
- `category`
- `title`
- `mime_type`
- `size_bytes`
- `minio_key`
- `created_at`

The table has a B-tree index on `category` and a GIN `pg_trgm` index on `title` for case-insensitive substring search.
