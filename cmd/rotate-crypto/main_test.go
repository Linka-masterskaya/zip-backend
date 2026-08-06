package main

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
)

func TestLoadCryptoPairRejectsNoopRotation(t *testing.T) {
	aesKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'a'}, 32))
	hmacKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'h'}, 32))
	t.Setenv("ROTATION_OLD_AES_KEY", aesKey)
	t.Setenv("ROTATION_NEW_AES_KEY", aesKey)
	t.Setenv("ROTATION_OLD_HMAC_KEY", hmacKey)
	t.Setenv("ROTATION_NEW_HMAC_KEY", hmacKey)

	_, _, err := loadCryptoPair()
	if err == nil {
		t.Fatal("identical old and new key pairs must be rejected")
	}
}

func TestLoadCryptoPairAllowsPartialRotation(t *testing.T) {
	oldAES := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'a'}, 32))
	newAES := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'b'}, 32))
	hmacKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'h'}, 32))
	t.Setenv("ROTATION_OLD_AES_KEY", oldAES)
	t.Setenv("ROTATION_NEW_AES_KEY", newAES)
	t.Setenv("ROTATION_OLD_HMAC_KEY", hmacKey)
	t.Setenv("ROTATION_NEW_HMAC_KEY", hmacKey)

	oldCrypto, newCrypto, err := loadCryptoPair()
	if err != nil {
		t.Fatalf("partial key rotation was rejected: %v", err)
	}
	if oldCrypto == nil || newCrypto == nil {
		t.Fatal("crypto clients were not created")
	}
}

func TestLoadCryptoPairRejectsDevelopmentNewKey(t *testing.T) {
	oldAES := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'o'}, 32))
	oldHMAC := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'p'}, 32))
	newHMAC := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'n'}, 32))
	t.Setenv("ROTATION_OLD_AES_KEY", oldAES)
	t.Setenv("ROTATION_OLD_HMAC_KEY", oldHMAC)
	t.Setenv("ROTATION_NEW_AES_KEY", config.DevAESKeyBase64)
	t.Setenv("ROTATION_NEW_HMAC_KEY", newHMAC)

	_, _, err := loadCryptoPair()
	if err == nil {
		t.Fatal("known development AES key must be rejected as a rotation target")
	}
	if !strings.Contains(err.Error(), "known development credential") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCryptoPairAllowsRotatingAwayFromDevelopmentKeys(t *testing.T) {
	newAES := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'a'}, 32))
	newHMAC := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'h'}, 32))
	t.Setenv("ROTATION_OLD_AES_KEY", config.DevAESKeyBase64)
	t.Setenv("ROTATION_OLD_HMAC_KEY", config.DevHMACKeyBase64)
	t.Setenv("ROTATION_NEW_AES_KEY", newAES)
	t.Setenv("ROTATION_NEW_HMAC_KEY", newHMAC)

	oldCrypto, newCrypto, err := loadCryptoPair()
	if err != nil {
		t.Fatalf("rotation away from known development keys was rejected: %v", err)
	}
	if oldCrypto == nil || newCrypto == nil {
		t.Fatal("crypto clients were not created")
	}
}

func TestLoadCryptoPairRejectsSameDecodedNewKeys(t *testing.T) {
	oldAES := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'o'}, 32))
	oldHMAC := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'p'}, 32))
	newKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'n'}, 32))
	t.Setenv("ROTATION_OLD_AES_KEY", oldAES)
	t.Setenv("ROTATION_OLD_HMAC_KEY", oldHMAC)
	t.Setenv("ROTATION_NEW_AES_KEY", newKey)
	t.Setenv("ROTATION_NEW_HMAC_KEY", "  "+newKey+"  ")

	_, _, err := loadCryptoPair()
	if err == nil {
		t.Fatal("AES and HMAC values that decode to the same bytes must be rejected")
	}
	if !strings.Contains(err.Error(), "must be different") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestLoadCryptoPairRejectsLegacyTrackedNewKey(t *testing.T) {
	legacy := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("0123456789abcdef"), 2))
	oldAES := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'o'}, 32))
	oldHMAC := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'p'}, 32))
	newHMAC := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'n'}, 32))
	t.Setenv("ROTATION_OLD_AES_KEY", oldAES)
	t.Setenv("ROTATION_OLD_HMAC_KEY", oldHMAC)
	t.Setenv("ROTATION_NEW_AES_KEY", legacy)
	t.Setenv("ROTATION_NEW_HMAC_KEY", newHMAC)

	_, _, err := loadCryptoPair()
	if err == nil {
		t.Fatal("previously tracked AES key must be rejected as a rotation target")
	}
	if !strings.Contains(err.Error(), "previously tracked development credential") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCryptoPairAllowsRotatingAwayFromLegacyTrackedKeys(t *testing.T) {
	legacy := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("0123456789abcdef"), 2))
	newAES := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'a'}, 32))
	newHMAC := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'h'}, 32))
	t.Setenv("ROTATION_OLD_AES_KEY", legacy)
	t.Setenv("ROTATION_OLD_HMAC_KEY", legacy)
	t.Setenv("ROTATION_NEW_AES_KEY", newAES)
	t.Setenv("ROTATION_NEW_HMAC_KEY", newHMAC)

	oldCrypto, newCrypto, err := loadCryptoPair()
	if err != nil {
		t.Fatalf("rotation away from a previously tracked shared key was rejected: %v", err)
	}
	if oldCrypto == nil || newCrypto == nil {
		t.Fatal("crypto clients were not created")
	}
}

func TestLoadCryptoPairRejectsHistoricalRawMaterialAsNewKeys(t *testing.T) {
	legacyAES := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("0123456789", 3) + "01"))
	legacyHMAC := base64.StdEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyz" + "123456"))
	oldAES := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'o'}, 32))
	oldHMAC := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'p'}, 32))
	safeAES := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'a'}, 32))
	safeHMAC := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'h'}, 32))

	t.Run("AES", func(t *testing.T) {
		t.Setenv("ROTATION_OLD_AES_KEY", oldAES)
		t.Setenv("ROTATION_OLD_HMAC_KEY", oldHMAC)
		t.Setenv("ROTATION_NEW_AES_KEY", legacyAES)
		t.Setenv("ROTATION_NEW_HMAC_KEY", safeHMAC)

		_, _, err := loadCryptoPair()
		if err == nil || !strings.Contains(err.Error(), "previously tracked key material") {
			t.Fatalf("historical AES material was not rejected: %v", err)
		}
	})

	t.Run("HMAC", func(t *testing.T) {
		t.Setenv("ROTATION_OLD_AES_KEY", oldAES)
		t.Setenv("ROTATION_OLD_HMAC_KEY", oldHMAC)
		t.Setenv("ROTATION_NEW_AES_KEY", safeAES)
		t.Setenv("ROTATION_NEW_HMAC_KEY", legacyHMAC)

		_, _, err := loadCryptoPair()
		if err == nil || !strings.Contains(err.Error(), "previously tracked key material") {
			t.Fatalf("historical HMAC material was not rejected: %v", err)
		}
	})
}

func TestLoadCryptoPairAllowsRotatingAwayFromHistoricalRawMaterial(t *testing.T) {
	legacyAES := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("0123456789", 3) + "01"))
	legacyHMAC := base64.StdEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyz" + "123456"))
	newAES := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'a'}, 32))
	newHMAC := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'h'}, 32))
	t.Setenv("ROTATION_OLD_AES_KEY", legacyAES)
	t.Setenv("ROTATION_OLD_HMAC_KEY", legacyHMAC)
	t.Setenv("ROTATION_NEW_AES_KEY", newAES)
	t.Setenv("ROTATION_NEW_HMAC_KEY", newHMAC)

	oldCrypto, newCrypto, err := loadCryptoPair()
	if err != nil {
		t.Fatalf("rotation away from historical raw key material was rejected: %v", err)
	}
	if oldCrypto == nil || newCrypto == nil {
		t.Fatal("crypto clients were not created")
	}
}
