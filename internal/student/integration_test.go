package student

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
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
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{})

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

	err = service.Delete(studentContext(ownerID), created.ID)
	assertStudentStatus(t, err, apperr.ErrConflict.HTTPStatus)
	_, err = pool.Exec(context.Background(), `DELETE FROM folders WHERE student_id = $1`, created.ID)
	require.NoError(t, err)
	require.NoError(t, service.Delete(studentContext(ownerID), created.ID))

	ownerList, err = service.List(studentContext(ownerID), ListInput{})
	require.NoError(t, err)
	assert.Empty(t, ownerList.Items)
	err = service.Delete(studentContext(foreignID), created.ID)
	assertStudentStatus(t, err, apperr.ErrNotFound.HTTPStatus)
}

func TestStudentCreateRequiresEmail(t *testing.T) {
	pool := studentTestDB(t)
	ownerID := seedStudentUser(t, pool, "owner")
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{})

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
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{})

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
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{err: errors.New("minio is down")})

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
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{})

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
	service := NewService(NewRepository(pool), identityCrypto{}, stubStorage{})

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
