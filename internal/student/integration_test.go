package student

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	"github.com/Linka-masterskaya/zip-backend/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStudentCRUDScopeAndFolderDeleteConflict(t *testing.T) {
	pool := studentTestDB(t)
	ownerID := seedStudentUser(t, pool, "owner")
	foreignID := seedStudentUser(t, pool, "foreign")
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{}, &stubUploader{pool: pool})

	created, err := service.Create(studentContext(ownerID), CreateInput{
		Email: " Student@Example.com ", Name: " Анна ", Age: intPtr(7),
	})
	require.NoError(t, err)
	assert.Equal(t, "student@example.com", created.Email)
	assert.Equal(t, "Анна", created.Name)
	assert.Equal(t, "active", created.Status)

	ownerList, err := service.List(studentContext(ownerID), ListInput{})
	require.NoError(t, err)
	require.Len(t, ownerList.Items, 1)
	foreignList, err := service.List(studentContext(foreignID), ListInput{})
	require.NoError(t, err)
	assert.Empty(t, foreignList.Items)

	newName := "Анна П."
	updated, err := service.Update(
		studentContext(ownerID), created.ID, UpdateInput{Name: &newName},
	)
	require.NoError(t, err)
	assert.Equal(t, newName, updated.Name)

	_, err = pool.Exec(context.Background(), `
		INSERT INTO folders (
			org_id, owner_id, section, kind, student_id, name, depth
		)
		SELECT org_id, id, 'students', 'student', $2, 'Анна', 0
		FROM users WHERE id = $1`, ownerID, created.ID)
	require.NoError(t, err)

	err = service.Delete(studentContext(ownerID), created.ID, false)
	assertStudentStatus(t, err, apperr.ErrConflict.HTTPStatus)
	_, err = pool.Exec(context.Background(), `DELETE FROM folders WHERE student_id = $1`, created.ID)
	require.NoError(t, err)
	require.NoError(t, service.Delete(studentContext(ownerID), created.ID, false))

	ownerList, err = service.List(studentContext(ownerID), ListInput{})
	require.NoError(t, err)
	assert.Empty(t, ownerList.Items)
	err = service.Delete(studentContext(foreignID), created.ID, false)
	assertStudentStatus(t, err, apperr.ErrNotFound.HTTPStatus)
}

func TestStudentCreateRequiresEmail(t *testing.T) {
	pool := studentTestDB(t)
	ownerID := seedStudentUser(t, pool, "owner")
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{}, &stubUploader{pool: pool})

	_, err := service.Create(studentContext(ownerID), CreateInput{Name: "No email"})
	assertStudentStatus(t, err, apperr.ErrBadRequest.HTTPStatus)
}

type identityCrypto struct{}

func (identityCrypto) Encrypt(value []byte) ([]byte, error) {
	return append([]byte(nil), value...), nil
}

func (identityCrypto) Decrypt(value []byte) ([]byte, error) {
	return append([]byte(nil), value...), nil
}

// stubStorage подменяет MinIO: presigned-ссылка считается локально, сеть
// для этого не нужна.
type stubStorage struct{ err error }

func (s stubStorage) PresignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return "https://minio.test/" + key + "?signature=stub", nil
}

// stubUploader подменяет банк медиа: запись кладётся прямо в таблицу,
// MinIO для этого не нужен.
type stubUploader struct{ pool *pgxpool.Pool }

func (u *stubUploader) Upload(ctx context.Context, _ []byte, name string) (*media.Response, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	mediaID := uuid.New()
	key := "avatars/" + mediaID.String() + "-" + name
	if _, err = u.pool.Exec(ctx, `
		INSERT INTO media_files (
			id, org_id, uploader_id, sha256, mime_type, size_bytes, minio_key,
			name, media_type
		)
		SELECT $1, u.org_id, u.id, $3, 'image/png', 10, $4, $5, 'image'
		FROM users u WHERE u.id = $2`,
		mediaID, userID, mediaID.String(), key, name); err != nil {
		return nil, err
	}
	return &media.Response{
		File: media.File{ID: mediaID, Name: name, MinIOKey: key},
		URL:  "https://minio.test/" + key,
	}, nil
}

func seedStudentMedia(t *testing.T, pool *pgxpool.Pool, uploaderID uuid.UUID, key string) uuid.UUID {
	t.Helper()
	mediaID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO media_files (
			id, org_id, uploader_id, sha256, mime_type, size_bytes, minio_key,
			name, media_type
		)
		SELECT $1, u.org_id, u.id, $3, 'image/png', 10, $4, 'avatar.png', 'image'
		FROM users u WHERE u.id = $2`,
		mediaID, uploaderID, key, key)
	require.NoError(t, err)
	return mediaID
}

func studentTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, cleanup := testutil.NewPostgres(t)
	t.Cleanup(cleanup)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, applyStudentMigrations(db))
	return pool
}

func applyStudentMigrations(db *sql.DB) error {
	return migrations.Run(db)
}

func seedStudentUser(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	orgID, userID := uuid.New(), uuid.New()
	_, err := pool.Exec(
		context.Background(),
		`INSERT INTO organizations (id, name) VALUES ($1, $2)`,
		orgID, name,
	)
	require.NoError(t, err)
	_, err = pool.Exec(
		context.Background(),
		`INSERT INTO users (id, org_id, display_name) VALUES ($1, $2, 'Test User')`,
		userID, orgID,
	)
	require.NoError(t, err)
	return userID
}

func studentContext(userID uuid.UUID) context.Context {
	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	return authctx.SetRoleToCtx(ctx, "defectologist")
}

func intPtr(value int) *int {
	return &value
}

func assertStudentStatus(t *testing.T, err error, status int) {
	t.Helper()
	var appErr *apperr.AppError
	require.Error(t, err)
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, status, appErr.HTTPStatus)
}

// TestStudentAvatarLifecycle проверяет весь путь аватара: постановку по
// media_id, выдачу свежей presigned-ссылки, снятие через null и отказ
// для файла чужой организации.
func TestStudentAvatarLifecycle(t *testing.T) {
	pool := studentTestDB(t)
	ownerID := seedStudentUser(t, pool, "owner")
	foreignID := seedStudentUser(t, pool, "foreign")
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{}, &stubUploader{pool: pool})

	mediaID := seedStudentMedia(t, pool, ownerID, "avatars/own.png")
	foreignMediaID := seedStudentMedia(t, pool, foreignID, "avatars/foreign.png")

	created, err := service.Create(studentContext(ownerID), CreateInput{
		Email: "avatar@example.com", Name: "Аня", AvatarMediaID: &mediaID,
	})
	require.NoError(t, err)
	require.NotNil(t, created.AvatarMediaID)
	assert.Equal(t, mediaID, *created.AvatarMediaID)
	require.NotNil(t, created.AvatarURL)
	assert.Contains(t, *created.AvatarURL, "avatars/own.png")

	// Ссылка выписывается на каждое чтение, поэтому приходит и в списке.
	list, err := service.List(studentContext(ownerID), ListInput{})
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	require.NotNil(t, list.Items[0].AvatarURL)
	assert.Contains(t, *list.Items[0].AvatarURL, "avatars/own.png")

	// Чужой файл ставить нельзя, даже зная его идентификатор.
	_, err = service.Update(studentContext(ownerID), created.ID, UpdateInput{
		AvatarMediaID: nullableField[uuid.UUID]{Set: true, Value: &foreignMediaID},
	})
	assertStudentStatus(t, err, 400)

	// null снимает аватар.
	cleared, err := service.Update(studentContext(ownerID), created.ID, UpdateInput{
		AvatarMediaID: nullableField[uuid.UUID]{Set: true},
	})
	require.NoError(t, err)
	assert.Nil(t, cleared.AvatarMediaID)
	assert.Nil(t, cleared.AvatarURL)

	// Отсутствие поля аватар не трогает.
	restored, err := service.Update(studentContext(ownerID), created.ID, UpdateInput{
		AvatarMediaID: nullableField[uuid.UUID]{Set: true, Value: &mediaID},
	})
	require.NoError(t, err)
	require.NotNil(t, restored.AvatarMediaID)
	newName := "Аня П."
	untouched, err := service.Update(studentContext(ownerID), created.ID, UpdateInput{Name: &newName})
	require.NoError(t, err)
	require.NotNil(t, untouched.AvatarMediaID)
	assert.Equal(t, mediaID, *untouched.AvatarMediaID)

	// Несуществующий media_id — понятная ошибка, а не нарушение FK.
	unknown := uuid.New()
	_, err = service.Update(studentContext(ownerID), created.ID, UpdateInput{
		AvatarMediaID: nullableField[uuid.UUID]{Set: true, Value: &unknown},
	})
	assertStudentStatus(t, err, 400)
}

// TestStudentAvatarSurvivesPresignFailure: список учеников важнее
// картинки, поэтому сбой подписи не должен ронять чтение.
func TestStudentAvatarSurvivesPresignFailure(t *testing.T) {
	pool := studentTestDB(t)
	ownerID := seedStudentUser(t, pool, "owner")
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{err: errors.New("minio is down")}, &stubUploader{pool: pool})

	mediaID := seedStudentMedia(t, pool, ownerID, "avatars/broken.png")
	created, err := service.Create(studentContext(ownerID), CreateInput{
		Email: "broken@example.com", Name: "Петя", AvatarMediaID: &mediaID,
	})
	require.NoError(t, err)
	require.NotNil(t, created.AvatarMediaID)
	assert.Nil(t, created.AvatarURL, "ссылки нет, но запись отдаётся")
}

// TestStudentAvatarClearedWhenMediaDeleted: файл удалили из банка —
// карточка ученика остаётся без битой ссылки.
func TestStudentAvatarClearedWhenMediaDeleted(t *testing.T) {
	pool := studentTestDB(t)
	ownerID := seedStudentUser(t, pool, "owner")
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{}, &stubUploader{pool: pool})

	mediaID := seedStudentMedia(t, pool, ownerID, "avatars/doomed.png")
	created, err := service.Create(studentContext(ownerID), CreateInput{
		Email: "doomed@example.com", Name: "Вася", AvatarMediaID: &mediaID,
	})
	require.NoError(t, err)
	require.NotNil(t, created.AvatarMediaID)

	_, err = pool.Exec(context.Background(), `DELETE FROM media_files WHERE id = $1`, mediaID)
	require.NoError(t, err)

	list, err := service.List(studentContext(ownerID), ListInput{})
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	assert.Nil(t, list.Items[0].AvatarMediaID)
	assert.Nil(t, list.Items[0].AvatarURL)
}

// TestStudentCardsShiftLifecycle: значение по умолчанию, смена, сброс через
// null и отказ для значения вне списка.
func TestStudentCardsShiftLifecycle(t *testing.T) {
	pool := studentTestDB(t)
	ownerID := seedStudentUser(t, pool, "owner")
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{}, &stubUploader{pool: pool})

	created, err := service.Create(studentContext(ownerID), CreateInput{
		Email: "shift@example.com", Name: "Аня",
	})
	require.NoError(t, err)
	require.NotNil(t, created.CardsShift)
	assert.Equal(t, "full", *created.CardsShift, "по умолчанию — full")

	left := "left"
	updated, err := service.Update(studentContext(ownerID), created.ID, UpdateInput{
		CardsShift: nullableField[string]{Set: true, Value: &left},
	})
	require.NoError(t, err)
	require.NotNil(t, updated.CardsShift)
	assert.Equal(t, "left", *updated.CardsShift)

	// Отсутствие поля раскладку не трогает.
	newName := "Аня П."
	untouched, err := service.Update(studentContext(ownerID), created.ID, UpdateInput{Name: &newName})
	require.NoError(t, err)
	require.NotNil(t, untouched.CardsShift)
	assert.Equal(t, "left", *untouched.CardsShift)

	// null возвращает значение по умолчанию.
	reset, err := service.Update(studentContext(ownerID), created.ID, UpdateInput{
		CardsShift: nullableField[string]{Set: true},
	})
	require.NoError(t, err)
	require.NotNil(t, reset.CardsShift)
	assert.Equal(t, "full", *reset.CardsShift)

	// Список отдаёт раскладку по каждому ученику.
	list, err := service.List(studentContext(ownerID), ListInput{})
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	require.NotNil(t, list.Items[0].CardsShift)
	assert.Equal(t, "full", *list.Items[0].CardsShift)

	center := "center"
	_, err = service.Update(studentContext(ownerID), created.ID, UpdateInput{
		CardsShift: nullableField[string]{Set: true, Value: &center},
	})
	assertStudentStatus(t, err, 400)

	_, err = service.Create(studentContext(ownerID), CreateInput{
		Email: "bad-shift@example.com", Name: "Петя", CardsShift: &center,
	})
	assertStudentStatus(t, err, 400)
}

// pngBytes — минимальная картинка: важны только сигнатура PNG, по ней
// определяется тип, и непустое тело.
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

// TestStudentAvatarUpload проверяет ручку PUT /students/{id}/avatar:
// картинка уезжает в банк медиа, ученик получает ссылку, чужой ученик и
// не-картинка отбиваются до загрузки.
func TestStudentAvatarUpload(t *testing.T) {
	pool := studentTestDB(t)
	ownerID := seedStudentUser(t, pool, "owner")
	foreignID := seedStudentUser(t, pool, "foreign")
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{}, &stubUploader{pool: pool})

	created, err := service.Create(studentContext(ownerID), CreateInput{
		Email: "upload@example.com", Name: "Аня",
	})
	require.NoError(t, err)
	require.Nil(t, created.AvatarMediaID)

	updated, err := service.ReplaceAvatar(studentContext(ownerID), created.ID, pngBytes, "photo.png")
	require.NoError(t, err)
	require.NotNil(t, updated.AvatarMediaID)
	require.NotNil(t, updated.AvatarURL)
	assert.Contains(t, *updated.AvatarURL, "photo.png")

	// Замена аватара ставит новый файл.
	replaced, err := service.ReplaceAvatar(studentContext(ownerID), created.ID, pngBytes, "second.png")
	require.NoError(t, err)
	require.NotNil(t, replaced.AvatarMediaID)
	assert.NotEqual(t, *updated.AvatarMediaID, *replaced.AvatarMediaID)

	// Не картинка — 400.
	_, err = service.ReplaceAvatar(studentContext(ownerID), created.ID, []byte("not an image"), "note.txt")
	assertStudentStatus(t, err, 400)

	// Чужой ученик — 404, файл в банк не попадает.
	var before int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM media_files`).Scan(&before))
	_, err = service.ReplaceAvatar(studentContext(foreignID), created.ID, pngBytes, "stolen.png")
	assertStudentStatus(t, err, apperr.ErrNotFound.HTTPStatus)
	var after int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM media_files`).Scan(&after))
	assert.Equal(t, before, after, "битый id не должен оставлять файл в банке")
}

// seedStudentFolder заводит папку ученика с вложенной папкой и возвращает
// их идентификаторы: корень и вложенную.
func seedStudentFolder(t *testing.T, pool *pgxpool.Pool, ownerID, studentID uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	var rootID uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO folders (org_id, owner_id, section, kind, student_id, name, depth)
		SELECT org_id, id, 'students', 'student', $2, 'Аня', 0
		FROM users WHERE id = $1
		RETURNING id`, ownerID, studentID).Scan(&rootID))

	var childID uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO folders (org_id, owner_id, parent_id, section, kind, name, depth)
		SELECT org_id, id, $2, 'students', 'folder', 'Занятия', 1
		FROM users WHERE id = $1
		RETURNING id`, ownerID, rootID).Scan(&childID))
	return rootID, childID
}

func seedStudentPack(t *testing.T, pool *pgxpool.Pool, ownerID, folderID uuid.UUID) uuid.UUID {
	t.Helper()
	var packID uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO packs (org_id, owner_id, folder_id, title)
		SELECT org_id, id, $2, 'Набор'
		FROM users WHERE id = $1
		RETURNING id`, ownerID, folderID).Scan(&packID))
	return packID
}

// TestStudentForceDeleteRemovesFolderTree: force сносит ученика вместе с
// папкой, вложенными папками, наборами и следами использования медиа.
func TestStudentForceDeleteRemovesFolderTree(t *testing.T) {
	pool := studentTestDB(t)
	ownerID := seedStudentUser(t, pool, "owner")
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{}, &stubUploader{pool: pool})

	created, err := service.Create(studentContext(ownerID), CreateInput{
		Email: "purge@example.com", Name: "Аня",
	})
	require.NoError(t, err)

	_, childID := seedStudentFolder(t, pool, ownerID, created.ID)
	packID := seedStudentPack(t, pool, ownerID, childID)
	mediaID := seedStudentMedia(t, pool, ownerID, "packs/picture.png")
	_, err = pool.Exec(context.Background(), `
		INSERT INTO media_usages (media_id, source_type, source_id)
		VALUES ($1, 'pack', $2)`, mediaID, packID)
	require.NoError(t, err)

	// Без force ученик с папкой не удаляется.
	assertStudentStatus(t, service.Delete(studentContext(ownerID), created.ID, false),
		apperr.ErrConflict.HTTPStatus)

	require.NoError(t, service.Delete(studentContext(ownerID), created.ID, true))

	assertCount(t, pool, `SELECT count(*) FROM students WHERE id = $1`, created.ID, 0)
	assertCount(t, pool, `SELECT count(*) FROM folders WHERE student_id = $1`, created.ID, 0)
	assertCount(t, pool, `SELECT count(*) FROM folders WHERE id = $1`, childID, 0)
	assertCount(t, pool, `SELECT count(*) FROM packs WHERE id = $1`, packID, 0)
	assertCount(t, pool, `SELECT count(*) FROM media_usages WHERE source_id = $1`, packID, 0)
	// Сам файл остаётся в банке: его удаляют отдельно, зато теперь он
	// больше ничем не занят.
	assertCount(t, pool, `SELECT count(*) FROM media_files WHERE id = $1`, mediaID, 1)

	// Повторный вызов — 404.
	assertStudentStatus(t, service.Delete(studentContext(ownerID), created.ID, true),
		apperr.ErrNotFound.HTTPStatus)
}

// TestStudentForceDeleteRefusesPublishedPack: опубликованный набор виден
// всей организации, поэтому вместе с учеником он не сносится.
func TestStudentForceDeleteRefusesPublishedPack(t *testing.T) {
	pool := studentTestDB(t)
	ownerID := seedStudentUser(t, pool, "owner")
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{}, &stubUploader{pool: pool})

	created, err := service.Create(studentContext(ownerID), CreateInput{
		Email: "published@example.com", Name: "Аня",
	})
	require.NoError(t, err)

	_, childID := seedStudentFolder(t, pool, ownerID, created.ID)
	packID := seedStudentPack(t, pool, ownerID, childID)

	var libraryID uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO folders (org_id, owner_id, section, kind, name, depth)
		SELECT org_id, id, 'library', 'folder', 'Библиотека', 0
		FROM users WHERE id = $1
		RETURNING id`, ownerID).Scan(&libraryID))
	_, err = pool.Exec(context.Background(), `
		UPDATE packs SET library_folder_id = $2, published_at = now()
		WHERE id = $1`, packID, libraryID)
	require.NoError(t, err)

	assertStudentStatus(t, service.Delete(studentContext(ownerID), created.ID, true),
		apperr.ErrConflict.HTTPStatus)
	// Транзакция откатилась целиком: ученик и набор на месте.
	assertCount(t, pool, `SELECT count(*) FROM students WHERE id = $1`, created.ID, 1)
	assertCount(t, pool, `SELECT count(*) FROM packs WHERE id = $1`, packID, 1)
}

// TestStudentForceDeleteWithoutFolder: ученика без папки force тоже сносит
// насовсем, а не архивирует.
func TestStudentForceDeleteWithoutFolder(t *testing.T) {
	pool := studentTestDB(t)
	ownerID := seedStudentUser(t, pool, "owner")
	foreignID := seedStudentUser(t, pool, "foreign")
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{}, &stubUploader{pool: pool})

	created, err := service.Create(studentContext(ownerID), CreateInput{
		Email: "plain@example.com", Name: "Петя",
	})
	require.NoError(t, err)

	// Чужого ученика снести нельзя.
	assertStudentStatus(t, service.Delete(studentContext(foreignID), created.ID, true),
		apperr.ErrNotFound.HTTPStatus)

	require.NoError(t, service.Delete(studentContext(ownerID), created.ID, true))
	assertCount(t, pool, `SELECT count(*) FROM students WHERE id = $1`, created.ID, 0)
}

func assertCount(t *testing.T, pool *pgxpool.Pool, query string, arg uuid.UUID, want int) {
	t.Helper()
	var got int
	require.NoError(t, pool.QueryRow(context.Background(), query, arg).Scan(&got))
	assert.Equal(t, want, got, query)
}
