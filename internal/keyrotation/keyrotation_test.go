package keyrotation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
)

func TestRotateAndRollbackPreserveReadableData(t *testing.T) {
	oldCrypto := newTestCrypto(t, 'o')
	newCrypto := newTestCrypto(t, 'n')
	records := []Record{
		newRecord(t, AuthCredential, "user-1", "user@example.com", oldCrypto),
		newRecord(t, Student, "student-1", "student@example.com", oldCrypto),
		newRecord(t, AuthCredential, "user-2", "already@example.com", newCrypto),
	}

	rotated, report, err := Rotate(records, oldCrypto, newCrypto)
	require.NoError(t, err)
	assert.Equal(t, 2, report.ChangedAES)
	assert.Equal(t, 1, report.ChangedHMAC)
	assertReadableWith(t, rotated, newCrypto)

	rolledBack, rollbackReport, err := Rotate(rotated, newCrypto, oldCrypto)
	require.NoError(t, err)
	assert.Equal(t, 3, rollbackReport.ChangedAES)
	assert.Equal(t, 2, rollbackReport.ChangedHMAC)
	assertReadableWith(t, rolledBack, oldCrypto)
}

func TestRotateRejectsUnreadableCiphertext(t *testing.T) {
	oldCrypto := newTestCrypto(t, 'o')
	newCrypto := newTestCrypto(t, 'n')
	_, _, err := Rotate([]Record{{Kind: Student, ID: "broken", Ciphertext: []byte("broken")}}, oldCrypto, newCrypto)
	require.Error(t, err)
	assert.ErrorContains(t, err, "old or new AES key")
}

func newTestCrypto(t *testing.T, marker byte) *cryptox.Cryptox {
	t.Helper()
	aesKey := make([]byte, 32)
	hmacKey := make([]byte, 32)
	for i := range aesKey {
		aesKey[i] = marker
		hmacKey[i] = marker + 1
	}
	client, err := cryptox.New(aesKey, hmacKey)
	require.NoError(t, err)
	return client
}

func newRecord(t *testing.T, kind Kind, id, email string, client *cryptox.Cryptox) Record {
	t.Helper()
	ciphertext, err := client.Encrypt([]byte(email))
	require.NoError(t, err)
	record := Record{Kind: kind, ID: id, Ciphertext: ciphertext}
	if kind == AuthCredential {
		record.EmailHash = client.Hash([]byte(email))
	}
	return record
}

func assertReadableWith(t *testing.T, records []Record, client *cryptox.Cryptox) {
	t.Helper()
	for _, record := range records {
		plaintext, err := client.Decrypt(record.Ciphertext)
		require.NoError(t, err)
		assert.NotEmpty(t, plaintext)
		if record.Kind == AuthCredential {
			assert.Equal(t, client.Hash(plaintext), record.EmailHash)
		}
	}
}
