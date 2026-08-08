package main

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Linka-masterskaya/zip-backend/internal/auth"
	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
	"github.com/Linka-masterskaya/zip-backend/internal/keyrotation"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
)

func TestDatabaseRotationAndRollbackPreserveReadableData(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := testutil.NewPostgres(t)
	t.Cleanup(cleanup)
	applyRotationMigrations(t, pool)

	oldCrypto := integrationCrypto(t, 'o', 'h')
	newCrypto := integrationCrypto(t, 'n', 'm')
	userID, studentID := insertRotationFixtures(t, ctx, pool, oldCrypto)

	rotateDatabase(t, ctx, pool, oldCrypto, newCrypto, true)
	assertPersistedEmails(t, ctx, pool, newCrypto, userID, studentID)

	rotateDatabase(t, ctx, pool, newCrypto, oldCrypto, true)
	assertPersistedEmails(t, ctx, pool, oldCrypto, userID, studentID)

	rotateDatabase(t, ctx, pool, oldCrypto, newCrypto, false)
	assertPersistedEmails(t, ctx, pool, oldCrypto, userID, studentID)
}

func applyRotationMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	db, err := sql.Open("pgx", pool.Config().ConnString())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, goose.Up(db, "../../migrations"))
}

func integrationCrypto(t *testing.T, aesByte, hmacByte byte) *cryptox.Cryptox {
	t.Helper()
	client, err := cryptox.New(repeatedByte(aesByte, 32), repeatedByte(hmacByte, 48))
	require.NoError(t, err)
	return client
}

func repeatedByte(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}

func insertRotationFixtures(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	cryptoClient *cryptox.Cryptox,
) (uuid.UUID, uuid.UUID) {
	t.Helper()
	orgID, userID, studentID := uuid.New(), uuid.New(), uuid.New()
	userEmail := []byte("owner@example.com")
	studentEmail := []byte("student@example.com")
	userCiphertext, err := cryptoClient.Encrypt(userEmail)
	require.NoError(t, err)
	studentCiphertext, err := cryptoClient.Encrypt(studentEmail)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO organizations (id, name) VALUES ($1, 'rotation-org')`, orgID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO users (id, org_id, display_name) VALUES ($1, $2, 'Owner')`, userID, orgID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO auth_cred (user_id, email_hash, email_encrypted, role)
		VALUES ($1, $2, $3, 'defectologist')`, userID, cryptoClient.Hash(userEmail), userCiphertext)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO students (id, defectologist_id, email_encrypted, name, age, status)
		VALUES ($1, $2, $3, 'Student', 8, 'active')`, studentID, userID, studentCiphertext)
	require.NoError(t, err)
	return userID, studentID
}

func rotateDatabase(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	oldCrypto, newCrypto *cryptox.Cryptox,
	commit bool,
) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	records, err := loadRecords(ctx, tx)
	require.NoError(t, err)
	rotated, report, err := keyrotation.Rotate(records, oldCrypto, newCrypto)
	require.NoError(t, err)
	require.Equal(t, len(records), report.ChangedAES)
	require.NoError(t, saveRecords(ctx, tx, rotated))

	persisted, err := loadRecords(ctx, tx)
	require.NoError(t, err)
	verified, err := keyrotation.Inspect(persisted, newCrypto, newCrypto)
	require.NoError(t, err)
	require.Equal(t, verified.Records, verified.AESNew)
	require.Zero(t, verified.HMACOld)

	if commit {
		require.NoError(t, tx.Commit(ctx))
	} else {
		require.NoError(t, tx.Rollback(ctx))
	}
}

func assertPersistedEmails(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	cryptoClient *cryptox.Cryptox,
	userID, studentID uuid.UUID,
) {
	t.Helper()
	const userEmail = "owner@example.com"
	const studentEmail = "student@example.com"

	repository := auth.NewAuthRepo(pool)
	user, err := repository.GetUserByEmailHash(ctx, cryptoClient.Hash([]byte(userEmail)))
	require.NoError(t, err)
	assert.Equal(t, userID.String(), user.ID)

	var userCiphertext, studentCiphertext []byte
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT email_encrypted FROM auth_cred WHERE user_id = $1`, userID).Scan(&userCiphertext))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT email_encrypted FROM students WHERE id = $1`, studentID).Scan(&studentCiphertext))

	decryptedUser, err := cryptoClient.Decrypt(userCiphertext)
	require.NoError(t, err)
	assert.Equal(t, userEmail, string(decryptedUser))
	decryptedStudent, err := cryptoClient.Decrypt(studentCiphertext)
	require.NoError(t, err)
	assert.Equal(t, studentEmail, string(decryptedStudent))
}
