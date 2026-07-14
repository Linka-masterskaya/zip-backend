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

	// apply migrations here
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
		`TRUNCATE verify_tokens, auth_cred, auth_identities, students, users CASCADE`)
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
		`INSERT INTO users (id, email_verified) VALUES ($1, false)`, id)
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

func TestGetUserContactForResend(t *testing.T) {
	repo := NewAuthRepo(testPool)

	t.Run("happy path", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		seedAuthCred(t, testPool, userID, []byte("encrypted-email"), "defectologist")

		email, verified, err := repo.getUserContactForResend(ctx, userID)

		require.NoError(t, err)
		assert.Equal(t, []byte("encrypted-email"), email)
		assert.False(t, verified)
	})

	t.Run("verified user", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		_, err := testPool.Exec(ctx,
			`UPDATE users SET email_verified = true WHERE id = $1`, userID)
		require.NoError(t, err)
		seedAuthCred(t, testPool, userID, []byte("encrypted-email"), "defectologist")

		email, verified, err := repo.getUserContactForResend(ctx, userID)

		require.NoError(t, err)
		assert.Equal(t, []byte("encrypted-email"), email)
		assert.True(t, verified)
	})

	t.Run("user not found", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		fakeID, _ := uuid.NewV7()

		_, _, err := repo.getUserContactForResend(ctx, fakeID)

		assert.ErrorIs(t, err, apperr.ErrUserNotFound)
	})

	t.Run("soft deleted user", func(t *testing.T) {
		truncateAll(t)
		ctx := testCtx(t)

		userID := seedUser(t, testPool)
		seedAuthCred(t, testPool, userID, []byte("encrypted-email"), "defectologist")
		_, err := testPool.Exec(ctx,
			`UPDATE users SET deleted_at = now() WHERE id = $1`, userID)
		require.NoError(t, err)

		_, _, err = repo.getUserContactForResend(ctx, userID)

		assert.ErrorIs(t, err, apperr.ErrUserNotFound)
	})
}
