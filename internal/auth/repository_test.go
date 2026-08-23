package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"testing"
	time "time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	uuid "github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, cleanup, err := testutil.NewPostgresCtx(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	if err := ApplyMigrations(pool, "../../migrations"); err != nil {
		cleanup()
		log.Fatal(err)
	}

	testPool = pool
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func ApplyMigrations(pool *pgxpool.Pool, migrationsDir string) error {
	db := stdlib.OpenDBFromPool(pool)
	goose.SetDialect("postgres")
	if err := goose.Up(db, migrationsDir); err != nil {
		return fmt.Errorf("testutil.ApplyMigrations: %w", err)
	}
	return nil
}

func truncateAll(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(),
		`TRUNCATE verify_tokens, auth_cred, auth_identities, students, users, organizations CASCADE`)
	require.NoError(t, err)
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func seedUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(),
		`INSERT INTO users (id, email_verified, display_name) VALUES ($1, false, 'Test User')`, id)
	require.NoError(t, err)
	return id
}

func seedStudent(t *testing.T, pool *pgxpool.Pool, defectologistID uuid.UUID) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(),
		`INSERT INTO students (id, defectologist_id, email_encrypted, email_verified, name, status)
			 VALUES ($1, $2, '\x00', false, 'test student', 'active')`,
		id, defectologistID)
	require.NoError(t, err)
	return id
}

func seedVerifyToken(t *testing.T, pool *pgxpool.Pool, userID, studentID *uuid.UUID, expiresAt time.Time, usedAt *time.Time) []byte {
	t.Helper()
	tokenID, err := uuid.NewV7()
	require.NoError(t, err)

	raw := make([]byte, 32)
	_, err = rand.Read(raw)
	require.NoError(t, err)
	hash := sha256.Sum256(raw)

	_, err = pool.Exec(context.Background(),
		`INSERT INTO verify_tokens (id, user_id, student_id, purpose, token_hash, expires_at, used_at)
			 VALUES ($1, $2, $3, 'email_verify', $4, $5, $6)`,
		tokenID, userID, studentID, hash[:], expiresAt, usedAt)
	require.NoError(t, err)

	return hash[:]
}

func TestUseEmailVerifyToken(t *testing.T) {
	repo := NewAuthRepo(testPool)

	t.Run("valid token returns userID", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		tokenHash := seedVerifyToken(t, testPool, &userID, nil, time.Now().Add(time.Hour), nil)

		gotUserID, gotStudentID, err := repo.useEmailVerifyToken(ctx, tokenHash)

		require.NoError(t, err)
		assert.Equal(t, userID, gotUserID)
		assert.Equal(t, uuid.Nil, gotStudentID)

		// проверяем что токен сожжён (used_at != NULL)
		var usedAt *time.Time
		err = testPool.QueryRow(ctx,
			`SELECT used_at FROM verify_tokens WHERE token_hash = $1`, tokenHash).Scan(&usedAt)
		require.NoError(t, err)
		assert.NotNil(t, usedAt)
	})

	t.Run("expired token", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		tokenHash := seedVerifyToken(t, testPool, &userID, nil, time.Now().Add(-time.Hour), nil)

		_, _, err := repo.useEmailVerifyToken(ctx, tokenHash)

		assert.ErrorIs(t, err, apperr.ErrVerifyTokenInvalid)
	})

	t.Run("already used token", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		usedAt := time.Now()
		tokenHash := seedVerifyToken(t, testPool, &userID, nil, time.Now().Add(time.Hour), &usedAt)

		_, _, err := repo.useEmailVerifyToken(ctx, tokenHash)

		assert.ErrorIs(t, err, apperr.ErrVerifyTokenInvalid)
	})

	t.Run("nonexistent token hash", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		fakeHash := sha256.Sum256([]byte("nonexistent"))

		_, _, err := repo.useEmailVerifyToken(ctx, fakeHash[:])

		assert.ErrorIs(t, err, apperr.ErrVerifyTokenInvalid)
	})

	t.Run("wrong purpose ignored", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		tokenID, _ := uuid.NewV7()
		raw := make([]byte, 32)
		rand.Read(raw)
		hash := sha256.Sum256(raw)

		// вставляем с purpose = 'password_reset', не 'email_verify'
		_, err := testPool.Exec(ctx,
			`INSERT INTO verify_tokens (id, user_id, purpose, token_hash, expires_at)
             VALUES ($1, $2, 'password_reset', $3, $4)`,
			tokenID, userID, hash[:], time.Now().Add(time.Hour))
		require.NoError(t, err)

		_, _, err = repo.useEmailVerifyToken(ctx, hash[:])

		assert.ErrorIs(t, err, apperr.ErrVerifyTokenInvalid)
	})

	t.Run("valid student token returns studentID", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		defID := seedUser(t, testPool)
		studentID := seedStudent(t, testPool, defID)
		tokenHash := seedVerifyToken(t, testPool, nil, &studentID, time.Now().Add(time.Hour), nil)

		gotUserID, gotStudentID, err := repo.useEmailVerifyToken(ctx, tokenHash)

		require.NoError(t, err)
		assert.Equal(t, uuid.Nil, gotUserID)
		assert.Equal(t, studentID, gotStudentID)
	})
}

func seedAuthCred(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, emailEncrypted []byte, role string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO auth_cred (user_id, email_hash, email_encrypted, role)
			 VALUES ($1, '\x00', $2, $3)`,
		userID, emailEncrypted, role)
	require.NoError(t, err)
}

func TestVerifyUser(t *testing.T) {
	repo := NewAuthRepo(testPool)

	t.Run("happy path", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)

		err := repo.verifyUser(ctx, userID)

		require.NoError(t, err)

		// проверяем что email_verified = true
		var verified bool
		err = testPool.QueryRow(ctx,
			`SELECT email_verified FROM users WHERE id = $1`, userID).Scan(&verified)
		require.NoError(t, err)
		assert.True(t, verified)
	})

	t.Run("already verified", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		// вручную верифицируем
		_, err := testPool.Exec(ctx,
			`UPDATE users SET email_verified = true WHERE id = $1`, userID)
		require.NoError(t, err)

		err = repo.verifyUser(ctx, userID)

		assert.ErrorIs(t, err, apperr.ErrVerifyTokenInvalid)
	})

	t.Run("soft deleted user", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		_, err := testPool.Exec(ctx,
			`UPDATE users SET deleted_at = now() WHERE id = $1`, userID)
		require.NoError(t, err)

		err = repo.verifyUser(ctx, userID)

		assert.ErrorIs(t, err, apperr.ErrVerifyTokenInvalid)
	})

	t.Run("nonexistent user", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		fakeID, _ := uuid.NewV7()

		err := repo.verifyUser(ctx, fakeID)

		assert.ErrorIs(t, err, apperr.ErrVerifyTokenInvalid)
	})
}

func TestVerifyStudent(t *testing.T) {
	repo := NewAuthRepo(testPool)

	t.Run("happy path", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		defID := seedUser(t, testPool)
		studentID := seedStudent(t, testPool, defID)

		err := repo.verifyStudent(ctx, studentID)

		require.NoError(t, err)

		var verified bool
		err = testPool.QueryRow(ctx,
			`SELECT email_verified FROM students WHERE id = $1`, studentID).Scan(&verified)
		require.NoError(t, err)
		assert.True(t, verified)
	})

	t.Run("already verified", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		defID := seedUser(t, testPool)
		studentID := seedStudent(t, testPool, defID)
		_, err := testPool.Exec(ctx,
			`UPDATE students SET email_verified = true WHERE id = $1`, studentID)
		require.NoError(t, err)

		err = repo.verifyStudent(ctx, studentID)

		assert.ErrorIs(t, err, apperr.ErrVerifyTokenInvalid)
	})

	t.Run("soft deleted student", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		defID := seedUser(t, testPool)
		studentID := seedStudent(t, testPool, defID)
		_, err := testPool.Exec(ctx,
			`UPDATE students SET deleted_at = now() WHERE id = $1`, studentID)
		require.NoError(t, err)

		err = repo.verifyStudent(ctx, studentID)

		assert.ErrorIs(t, err, apperr.ErrVerifyTokenInvalid)
	})

	t.Run("nonexistent student", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		fakeID, _ := uuid.NewV7()

		err := repo.verifyStudent(ctx, fakeID)

		assert.ErrorIs(t, err, apperr.ErrVerifyTokenInvalid)
	})
}

func TestRotateEmailTokens(t *testing.T) {
	repo := NewAuthRepo(testPool)

	t.Run("inserts new token and invalidates old", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)

		// создаём первый токен
		oldHash := seedVerifyToken(t, testPool, &userID, nil, time.Now().Add(time.Hour), nil)

		// rotate — должен инвалидировать старый и вставить новый
		newTokenID, _ := uuid.NewV7()
		newRaw := make([]byte, 32)
		rand.Read(newRaw)
		newHash := sha256.Sum256(newRaw)

		err := repo.rotateEmailTokens(ctx, newTokenID, userID, newHash[:], time.Now().Add(time.Hour))
		require.NoError(t, err)

		// старый токен — used_at != NULL
		var oldUsedAt *time.Time
		err = testPool.QueryRow(ctx,
			`SELECT used_at FROM verify_tokens WHERE token_hash = $1`, oldHash).Scan(&oldUsedAt)
		require.NoError(t, err)
		assert.NotNil(t, oldUsedAt)

		// новый токен — used_at IS NULL
		var newUsedAt *time.Time
		err = testPool.QueryRow(ctx,
			`SELECT used_at FROM verify_tokens WHERE token_hash = $1`, newHash[:]).Scan(&newUsedAt)
		require.NoError(t, err)
		assert.Nil(t, newUsedAt)
	})

	t.Run("works when no prior tokens exist", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		tokenID, _ := uuid.NewV7()
		raw := make([]byte, 32)
		rand.Read(raw)
		hash := sha256.Sum256(raw)

		err := repo.rotateEmailTokens(ctx, tokenID, userID, hash[:], time.Now().Add(time.Hour))

		require.NoError(t, err)

		// токен вставился
		var count int
		err = testPool.QueryRow(ctx,
			`SELECT count(*) FROM verify_tokens WHERE user_id = $1`, userID).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})
}

func TestCreatePasswordResetToken(t *testing.T) {
	repo := NewAuthRepo(testPool)

	t.Run("inserts token hash with password reset purpose", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		token, err := repo.CreatePasswordResetToken(ctx, userID.String(), time.Hour)
		require.NoError(t, err)
		require.NotEmpty(t, token)

		rawToken, err := decodePasswordResetToken(token)
		require.NoError(t, err)
		expectedHash := hashPasswordResetToken(rawToken)

		var (
			purpose   string
			tokenHash []byte
			usedAt    *time.Time
			expiresAt time.Time
		)
		err = testPool.QueryRow(ctx, `
			SELECT purpose, token_hash, used_at, expires_at
			FROM verify_tokens
			WHERE user_id = $1
		`, userID).Scan(&purpose, &tokenHash, &usedAt, &expiresAt)
		require.NoError(t, err)

		assert.Equal(t, passwordResetTokenPurpose, purpose)
		assert.Equal(t, expectedHash, tokenHash)
		assert.Nil(t, usedAt)
		assert.True(t, expiresAt.After(time.Now()))
	})
}

func TestResetPasswordByToken(t *testing.T) {
	repo := NewAuthRepo(testPool)

	t.Run("valid token updates password and is single use", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		seedAuthCred(t, testPool, userID, []byte("encrypted-email"), "defectologist")
		token, err := repo.CreatePasswordResetToken(ctx, userID.String(), time.Hour)
		require.NoError(t, err)

		gotUserID, err := repo.ResetPasswordByToken(ctx, token, "new-password-hash")
		require.NoError(t, err)
		assert.Equal(t, userID, gotUserID)

		var (
			passwordHash string
			usedAt       *time.Time
		)
		err = testPool.QueryRow(ctx, `
			SELECT ac.password_hash, vt.used_at
			FROM auth_cred ac
			JOIN verify_tokens vt ON vt.user_id = ac.user_id
			WHERE ac.user_id = $1 AND vt.purpose = $2
		`, userID, passwordResetTokenPurpose).Scan(&passwordHash, &usedAt)
		require.NoError(t, err)
		assert.Equal(t, "new-password-hash", passwordHash)
		assert.NotNil(t, usedAt)

		_, err = repo.ResetPasswordByToken(ctx, token, "another-password-hash")
		assert.ErrorIs(t, err, apperr.ErrInvalidResetToken)
	})

	t.Run("expired token is invalid and password is not changed", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		seedAuthCred(t, testPool, userID, []byte("encrypted-email"), "defectologist")
		tokenID, err := uuid.NewV7()
		require.NoError(t, err)
		token, rawToken, err := newPasswordResetToken()
		require.NoError(t, err)

		_, err = testPool.Exec(ctx, `
			INSERT INTO verify_tokens (id, user_id, purpose, token_hash, expires_at)
			VALUES ($1, $2, $3, $4, $5)
		`, tokenID, userID, passwordResetTokenPurpose, hashPasswordResetToken(rawToken), time.Now().Add(-time.Hour))
		require.NoError(t, err)

		_, err = repo.ResetPasswordByToken(ctx, token, "new-password-hash")
		assert.ErrorIs(t, err, apperr.ErrInvalidResetToken)

		var passwordHash *string
		err = testPool.QueryRow(ctx, `SELECT password_hash FROM auth_cred WHERE user_id = $1`, userID).Scan(&passwordHash)
		require.NoError(t, err)
		assert.Nil(t, passwordHash)
	})

	t.Run("token with another purpose is invalid", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		seedAuthCred(t, testPool, userID, []byte("encrypted-email"), "defectologist")
		tokenID, err := uuid.NewV7()
		require.NoError(t, err)
		token, rawToken, err := newPasswordResetToken()
		require.NoError(t, err)

		_, err = testPool.Exec(ctx, `
			INSERT INTO verify_tokens (id, user_id, purpose, token_hash, expires_at)
			VALUES ($1, $2, 'email_verify', $3, $4)
		`, tokenID, userID, hashPasswordResetToken(rawToken), time.Now().Add(time.Hour))
		require.NoError(t, err)

		_, err = repo.ResetPasswordByToken(ctx, token, "new-password-hash")
		assert.ErrorIs(t, err, apperr.ErrInvalidResetToken)
	})

	t.Run("missing auth credentials rolls back consumed token", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		// НЕ СОЗДАЁМ auth_cred - это приведёт к ошибке

		token, err := repo.CreatePasswordResetToken(ctx, userID.String(), time.Hour)
		require.NoError(t, err)

		// Попытка сбросить пароль должна вернуть ошибку, так как auth_cred отсутствует
		_, err = repo.ResetPasswordByToken(ctx, token, "new-password-hash")
		require.Error(t, err)

		// Проверяем, что токен НЕ помечен как использованный (used_at = NULL)
		// Это значит, что транзакция была откачена
		var usedAt *time.Time
		err = testPool.QueryRow(ctx, `
			SELECT used_at
			FROM verify_tokens
			WHERE user_id = $1 AND purpose = $2
		`, userID, passwordResetTokenPurpose).Scan(&usedAt)
		require.NoError(t, err)
		assert.Nil(t, usedAt, "token should not be marked as used when transaction rolls back")

		// Создаём auth_cred для этого пользователя
		seedAuthCred(t, testPool, userID, []byte("encrypted-email"), "defectologist")

		// Теперь тот же токен должен работать
		gotUserID, err := repo.ResetPasswordByToken(ctx, token, "new-password-hash")
		require.NoError(t, err)
		assert.Equal(t, userID, gotUserID)

		// Проверяем, что токен теперь использован
		err = testPool.QueryRow(ctx, `
			SELECT used_at
			FROM verify_tokens
			WHERE user_id = $1 AND purpose = $2
		`, userID, passwordResetTokenPurpose).Scan(&usedAt)
		require.NoError(t, err)
		assert.NotNil(t, usedAt, "token should be marked as used after successful reset")
	})
}

func TestDeleteStaleUnverifiedUsersKeepsVerifiedRecentAndOwners(t *testing.T) {
	truncateAll(t)
	ctx := testCtx(t)
	repo := &authRepo{db: testPool, pool: testPool}

	newUser := func(name string, verified bool, age time.Duration) uuid.UUID {
		t.Helper()
		orgID := uuid.New()
		_, err := testPool.Exec(ctx,
			`INSERT INTO organizations (id, name) VALUES ($1, $2)`, orgID, name)
		require.NoError(t, err)
		userID := uuid.New()
		_, err = testPool.Exec(ctx, `
			INSERT INTO users (id, org_id, email_verified, display_name, created_at)
			VALUES ($1, $2, $3, 'Test User', now() - $4::interval)`,
			userID, orgID, verified, age.String())
		require.NoError(t, err)
		return userID
	}

	stale := newUser("stale org", false, 8*24*time.Hour)
	verified := newUser("verified org", true, 8*24*time.Hour)
	recent := newUser("recent org", false, time.Hour)
	owner := newUser("owner org", false, 8*24*time.Hour)

	// У владельца есть папка: folders.owner_id объявлен ON DELETE RESTRICT,
	// и фоновая задача не должна ни падать, ни удалять данные.
	_, err := testPool.Exec(ctx, `
		INSERT INTO folders (org_id, owner_id, section, kind, name, depth)
		SELECT org_id, id, 'my', 'folder', 'Моя папка', 0 FROM users WHERE id = $1`, owner)
	require.NoError(t, err)

	deleted, err := repo.DeleteStaleUnverifiedUsers(ctx, time.Now().Add(-7*24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	remaining := map[uuid.UUID]bool{}
	rows, err := testPool.Query(ctx, `SELECT id FROM users`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		require.NoError(t, rows.Scan(&id))
		remaining[id] = true
	}
	require.NoError(t, rows.Err())

	assert.False(t, remaining[stale], "просроченная неподтверждённая регистрация удаляется")
	assert.True(t, remaining[verified], "подтверждённый аккаунт не трогаем")
	assert.True(t, remaining[recent], "свежая регистрация ещё имеет право на жизнь")
	assert.True(t, remaining[owner], "владельца данных не удаляем")

	// Личная организация удалённого пользователя не должна остаться сиротой.
	var orgs int
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT count(*) FROM organizations WHERE name = 'stale org'`).Scan(&orgs))
	assert.Zero(t, orgs)

	var keptOrgs int
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT count(*) FROM organizations`).Scan(&keptOrgs))
	assert.Equal(t, 3, keptOrgs)
}

func TestCreateOAuthUserAndAuthCred(t *testing.T) {
	truncateAll(t)
	ctx := testCtx(t)

	repo := NewAuthRepo(testPool)

	userID := uuid.New()

	tx, err := repo.beginTx(ctx)
	require.NoError(t, err)

	txRepo := repo.withTx(tx)

	err = txRepo.CreateOAuthUser(ctx, CreateUserParams{
		ID:             userID,
		OrganizationID: nil,
		Name:           "test@example.com",
		EmailVerified:  false,
	})
	require.NoError(t, err)

	err = txRepo.CreateAuthCred(ctx, CreateAuthCredParams{
		UserID:         userID,
		EmailHash:      []byte("hash"),
		EmailEncrypted: []byte("encrypted"),
		PasswordHash:   "password",
		Role:           "defectologist",
	})
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))

	var count int
	err = testPool.QueryRow(
		ctx,
		`SELECT count(*) FROM users WHERE id = $1`,
		userID,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	var credCount int
	err = testPool.QueryRow(
		ctx,
		`SELECT count(*) FROM auth_cred WHERE user_id = $1`,
		userID,
	).Scan(&credCount)
	require.NoError(t, err)
	assert.Equal(t, 1, credCount)
}
