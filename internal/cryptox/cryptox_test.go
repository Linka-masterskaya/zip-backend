package cryptox

import (
	"errors"
	"testing"
)

func TestDecryptRoundTrip(t *testing.T) {
	aesKey := make([]byte, 32)
	for i := range aesKey {
		aesKey[i] = byte(i)
	}
	hmacKey := make([]byte, 32)
	for i := range hmacKey {
		hmacKey[i] = byte(i + 1)
	}

	c, err := New(aesKey, hmacKey)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	plaintext := []byte("hello world")
	encrypted, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() failed: %v", err)
	}

	decrypted, err := c.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt() failed: %v", err)
	}

	if string(decrypted) != string(plaintext) {
		t.Errorf("Decrypted plaintext mismatch: got %q, want %q", string(decrypted), string(plaintext))
	}
}

func TestDecryptCiphertextTooShort(t *testing.T) {
	aesKey := make([]byte, 32)
	for i := range aesKey {
		aesKey[i] = byte(i)
	}
	hmacKey := make([]byte, 32)
	for i := range hmacKey {
		hmacKey[i] = byte(i + 1)
	}

	c, err := New(aesKey, hmacKey)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Ciphertext shorter than nonce size
	shortCiphertext := []byte("short")
	_, err = c.Decrypt(shortCiphertext)
	if err == nil {
		t.Fatal("Decrypt() should have failed with short ciphertext")
	}

	if !errors.Is(err, ErrCiphertextTooShort) {
		t.Errorf("Decrypt() error mismatch: got %v, want ErrCiphertextTooShort", err)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	// Create two cryptox instances with different keys
	aesKey1 := make([]byte, 32)
	for i := range aesKey1 {
		aesKey1[i] = byte(i)
	}
	hmacKey1 := make([]byte, 32)
	for i := range hmacKey1 {
		hmacKey1[i] = byte(i + 1)
	}

	c1, err := New(aesKey1, hmacKey1)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	aesKey2 := make([]byte, 32)
	for i := range aesKey2 {
		aesKey2[i] = byte(i + 2)
	}
	hmacKey2 := make([]byte, 32)
	for i := range hmacKey2 {
		hmacKey2[i] = byte(i + 3)
	}

	c2, err := New(aesKey2, hmacKey2)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	plaintext := []byte("secret message")
	encrypted, err := c1.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() failed: %v", err)
	}

	// Try to decrypt with a different key
	_, err = c2.Decrypt(encrypted)
	if err == nil {
		t.Fatal("Decrypt() should have failed with wrong key")
	}

	if !errors.Is(err, ErrDecryptFailed) {
		t.Errorf("Decrypt() error mismatch: got %v, want ErrDecryptFailed", err)
	}
}
