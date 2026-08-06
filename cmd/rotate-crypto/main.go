// Command rotate-crypto checks or rotates AES/HMAC keys used for persisted emails.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
	"github.com/Linka-masterskaya/zip-backend/internal/keyrotation"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
)

const (
	defaultConfigPath = "config/config.prod.yml"
	applyConfirmation = "ROTATE_CRYPTO_KEYS"
)

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("crypto key rotation failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfgPath := envOrDefault("CONFIG_PATH", defaultConfigPath)
	cfg, err := config.LoadMigration(cfgPath)
	if err != nil {
		return fmt.Errorf("load migration config: %w", err)
	}
	logger.Init(cfg.App.Env)

	mode, err := loadRotationMode()
	if err != nil {
		return err
	}
	oldCrypto, newCrypto, err := loadCryptoPair()
	if err != nil {
		return err
	}

	conn, err := pgx.Connect(ctx, cfg.DB.URL)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer closeConnection(ctx, conn)

	return executeRotation(ctx, conn, mode, oldCrypto, newCrypto)
}

func loadRotationMode() (string, error) {
	mode := strings.ToLower(envOrDefault("ROTATION_MODE", "check"))
	if mode != "check" && mode != "apply" {
		return "", fmt.Errorf("ROTATION_MODE must be check or apply")
	}
	if mode == "apply" && os.Getenv("ROTATION_CONFIRM") != applyConfirmation {
		return "", fmt.Errorf("ROTATION_CONFIRM must equal %s for apply mode", applyConfirmation)
	}
	return mode, nil
}

func closeConnection(ctx context.Context, conn *pgx.Conn) {
	if err := conn.Close(ctx); err != nil {
		slog.Error("close postgres connection failed", "err", err)
	}
}

func executeRotation(
	ctx context.Context,
	conn *pgx.Conn,
	mode string,
	oldCrypto *cryptox.Cryptox,
	newCrypto *cryptox.Cryptox,
) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin rotation transaction: %w", err)
	}
	finalized := false
	defer func() {
		if finalized {
			return
		}
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			slog.Error("rollback crypto key rotation failed", "err", rollbackErr)
		}
	}()

	records, err := loadRecords(ctx, tx)
	if err != nil {
		return err
	}
	inspection, err := keyrotation.Inspect(records, oldCrypto, newCrypto)
	if err != nil {
		return fmt.Errorf("inspect persisted email data: %w", err)
	}
	logReport("crypto key inspection", inspection)

	if mode == "check" {
		if err := tx.Rollback(ctx); err != nil {
			return fmt.Errorf("rollback check transaction: %w", err)
		}
		finalized = true
		slog.Info("check completed; transaction rolled back without changes")
		return nil
	}

	report, err := rotateAndVerify(ctx, tx, records, oldCrypto, newCrypto)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rotation: %w", err)
	}
	finalized = true
	logReport("crypto key rotation committed", report)
	return nil
}

func rotateAndVerify(
	ctx context.Context,
	tx pgx.Tx,
	records []keyrotation.Record,
	oldCrypto *cryptox.Cryptox,
	newCrypto *cryptox.Cryptox,
) (keyrotation.Report, error) {
	rotated, report, err := keyrotation.Rotate(records, oldCrypto, newCrypto)
	if err != nil {
		return keyrotation.Report{}, fmt.Errorf("prepare rotated data: %w", err)
	}
	if err := saveRecords(ctx, tx, rotated); err != nil {
		return keyrotation.Report{}, err
	}
	persisted, err := loadRecords(ctx, tx)
	if err != nil {
		return keyrotation.Report{}, fmt.Errorf("reload rotated data: %w", err)
	}
	verified, err := keyrotation.Inspect(persisted, newCrypto, newCrypto)
	if err != nil {
		return keyrotation.Report{}, fmt.Errorf("verify persisted rotation: %w", err)
	}
	if verified.AESNew != verified.Records || verified.HMACOld != 0 {
		return keyrotation.Report{}, fmt.Errorf("verify persisted rotation: not all records use the new keys")
	}
	return report, nil
}

func loadCryptoPair() (*cryptox.Cryptox, *cryptox.Cryptox, error) {
	oldAES, err := decodeKey("ROTATION_OLD_AES_KEY", 32, true)
	if err != nil {
		return nil, nil, err
	}
	oldHMAC, err := decodeKey("ROTATION_OLD_HMAC_KEY", 32, false)
	if err != nil {
		return nil, nil, err
	}
	newAESRaw := strings.TrimSpace(os.Getenv("ROTATION_NEW_AES_KEY"))
	newHMACRaw := strings.TrimSpace(os.Getenv("ROTATION_NEW_HMAC_KEY"))
	newAES, newHMAC, err := config.ValidateProductionCryptoKeys(newAESRaw, newHMACRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("validate new rotation keys: %w", err)
	}
	if bytes.Equal(oldAES, newAES) && bytes.Equal(oldHMAC, newHMAC) {
		return nil, nil, fmt.Errorf("old and new AES/HMAC key pairs are identical")
	}
	oldCrypto, err := cryptox.New(oldAES, oldHMAC)
	if err != nil {
		return nil, nil, fmt.Errorf("create old crypto client: %w", err)
	}
	newCrypto, err := cryptox.New(newAES, newHMAC)
	if err != nil {
		return nil, nil, fmt.Errorf("create new crypto client: %w", err)
	}
	return oldCrypto, newCrypto, nil
}

func decodeKey(name string, minimum int, exact bool) ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, fmt.Errorf("%s is required", name)
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be valid base64", name)
	}
	if exact && len(decoded) != minimum {
		return nil, fmt.Errorf("%s must decode to exactly %d bytes", name, minimum)
	}
	if !exact && len(decoded) < minimum {
		return nil, fmt.Errorf("%s must decode to at least %d bytes", name, minimum)
	}
	return decoded, nil
}

func loadRecords(ctx context.Context, tx pgx.Tx) ([]keyrotation.Record, error) {
	records := make([]keyrotation.Record, 0)
	authRows, err := tx.Query(ctx, `
		SELECT user_id::text, email_encrypted, email_hash
		FROM auth_cred
		ORDER BY user_id
		FOR UPDATE`)
	if err != nil {
		return nil, fmt.Errorf("lock auth_cred rows: %w", err)
	}
	for authRows.Next() {
		var record keyrotation.Record
		record.Kind = keyrotation.AuthCredential
		if err := authRows.Scan(&record.ID, &record.Ciphertext, &record.EmailHash); err != nil {
			authRows.Close()
			return nil, fmt.Errorf("scan auth_cred row: %w", err)
		}
		records = append(records, record)
	}
	if err := authRows.Err(); err != nil {
		authRows.Close()
		return nil, fmt.Errorf("iterate auth_cred rows: %w", err)
	}
	authRows.Close()

	studentRows, err := tx.Query(ctx, `
		SELECT id::text, email_encrypted
		FROM students
		ORDER BY id
		FOR UPDATE`)
	if err != nil {
		return nil, fmt.Errorf("lock student rows: %w", err)
	}
	defer studentRows.Close()
	for studentRows.Next() {
		var record keyrotation.Record
		record.Kind = keyrotation.Student
		if err := studentRows.Scan(&record.ID, &record.Ciphertext); err != nil {
			return nil, fmt.Errorf("scan student row: %w", err)
		}
		records = append(records, record)
	}
	if err := studentRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate student rows: %w", err)
	}
	return records, nil
}

func saveRecords(ctx context.Context, tx pgx.Tx, records []keyrotation.Record) error {
	for _, record := range records {
		commandTag, err := updateRecord(ctx, tx, record)
		if err != nil {
			return fmt.Errorf("save %s %s: %w", record.Kind, record.ID, err)
		}
		if commandTag.RowsAffected() != 1 {
			return fmt.Errorf("save %s %s: expected one updated row", record.Kind, record.ID)
		}
	}
	return nil
}

func updateRecord(ctx context.Context, tx pgx.Tx, record keyrotation.Record) (pgconn.CommandTag, error) {
	var commandTag pgconn.CommandTag
	var err error
	switch record.Kind {
	case keyrotation.AuthCredential:
		commandTag, err = tx.Exec(ctx, `
			UPDATE auth_cred
			SET email_encrypted = $1, email_hash = $2, updated_at = now()
			WHERE user_id = $3::uuid`, record.Ciphertext, record.EmailHash, record.ID)
	case keyrotation.Student:
		commandTag, err = tx.Exec(ctx, `
			UPDATE students
			SET email_encrypted = $1, updated_at = now()
			WHERE id = $2::uuid`, record.Ciphertext, record.ID)
	default:
		err = fmt.Errorf("unsupported record kind %q", record.Kind)
	}
	return commandTag, err
}

func logReport(message string, report keyrotation.Report) {
	slog.Info(message,
		"records", report.Records,
		"aes_old", report.AESOld,
		"aes_new", report.AESNew,
		"hmac_old", report.HMACOld,
		"hmac_new", report.HMACNew,
		"changed_aes", report.ChangedAES,
		"changed_hmac", report.ChangedHMAC,
	)
}

func envOrDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}
