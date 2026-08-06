// Package keyrotation safely inspects and re-encrypts persisted email data.
package keyrotation

import (
	"crypto/hmac"
	"fmt"

	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
)

type Kind string

const (
	AuthCredential Kind = "auth_cred"
	Student        Kind = "students"
)

type Record struct {
	Kind       Kind
	ID         string
	Ciphertext []byte
	EmailHash  []byte
}

type Report struct {
	Records     int
	AESOld      int
	AESNew      int
	HMACOld     int
	HMACNew     int
	ChangedAES  int
	ChangedHMAC int
}

func Inspect(records []Record, oldCrypto, newCrypto *cryptox.Cryptox) (Report, error) {
	report := Report{Records: len(records)}
	for _, record := range records {
		plaintext, usesNewAES, err := decryptRecord(record, oldCrypto, newCrypto)
		if err != nil {
			return Report{}, err
		}
		if usesNewAES {
			report.AESNew++
		} else {
			report.AESOld++
		}
		if record.Kind == AuthCredential {
			usesNewHMAC, hashErr := inspectHash(record, plaintext, oldCrypto, newCrypto)
			if hashErr != nil {
				return Report{}, hashErr
			}
			if usesNewHMAC {
				report.HMACNew++
			} else {
				report.HMACOld++
			}
		}
	}
	return report, nil
}

func Rotate(records []Record, oldCrypto, newCrypto *cryptox.Cryptox) ([]Record, Report, error) {
	rotated := make([]Record, 0, len(records))
	report := Report{Records: len(records)}
	for _, record := range records {
		updated, recordReport, err := rotateRecord(record, oldCrypto, newCrypto)
		if err != nil {
			return nil, Report{}, err
		}
		rotated = append(rotated, updated)
		report.AESOld += recordReport.AESOld
		report.AESNew += recordReport.AESNew
		report.HMACOld += recordReport.HMACOld
		report.HMACNew += recordReport.HMACNew
		report.ChangedAES += recordReport.ChangedAES
		report.ChangedHMAC += recordReport.ChangedHMAC
	}
	if _, err := Inspect(rotated, newCrypto, newCrypto); err != nil {
		return nil, Report{}, fmt.Errorf("verify rotated records: %w", err)
	}
	return rotated, report, nil
}

func rotateRecord(record Record, oldCrypto, newCrypto *cryptox.Cryptox) (Record, Report, error) {
	report := Report{Records: 1}
	plaintext, usesNewAES, err := decryptRecord(record, oldCrypto, newCrypto)
	if err != nil {
		return Record{}, Report{}, err
	}
	updated := record
	if usesNewAES {
		report.AESNew = 1
	} else {
		report.AESOld = 1
		updated.Ciphertext, err = newCrypto.Encrypt(plaintext)
		if err != nil {
			return Record{}, Report{}, fmt.Errorf("encrypt %s %s with new AES key: %w", record.Kind, record.ID, err)
		}
		report.ChangedAES = 1
	}
	if record.Kind == AuthCredential {
		usesNewHMAC, hashErr := inspectHash(record, plaintext, oldCrypto, newCrypto)
		if hashErr != nil {
			return Record{}, Report{}, hashErr
		}
		if usesNewHMAC {
			report.HMACNew = 1
		} else {
			report.HMACOld = 1
			updated.EmailHash = newCrypto.Hash(plaintext)
			report.ChangedHMAC = 1
		}
	}
	verified, err := newCrypto.Decrypt(updated.Ciphertext)
	if err != nil || !hmac.Equal(verified, plaintext) {
		return Record{}, Report{}, fmt.Errorf("verify %s %s after AES rotation", record.Kind, record.ID)
	}
	if record.Kind == AuthCredential && !hmac.Equal(updated.EmailHash, newCrypto.Hash(plaintext)) {
		return Record{}, Report{}, fmt.Errorf("verify %s %s after HMAC rotation", record.Kind, record.ID)
	}
	return updated, report, nil
}

func decryptRecord(record Record, oldCrypto, newCrypto *cryptox.Cryptox) ([]byte, bool, error) {
	plaintext, err := newCrypto.Decrypt(record.Ciphertext)
	if err == nil {
		return plaintext, true, nil
	}
	plaintext, oldErr := oldCrypto.Decrypt(record.Ciphertext)
	if oldErr != nil {
		return nil, false, fmt.Errorf("decrypt %s %s with old or new AES key", record.Kind, record.ID)
	}
	return plaintext, false, nil
}

func inspectHash(record Record, plaintext []byte, oldCrypto, newCrypto *cryptox.Cryptox) (bool, error) {
	if hmac.Equal(record.EmailHash, newCrypto.Hash(plaintext)) {
		return true, nil
	}
	if hmac.Equal(record.EmailHash, oldCrypto.Hash(plaintext)) {
		return false, nil
	}
	return false, fmt.Errorf("verify %s %s email hash with old or new HMAC key", record.Kind, record.ID)
}
