package mailer

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// ВСПОМОГАТЕЛЬНЫЕ ФУНКЦИИ
// ============================================================

// isMailpitAvailable checks if Mailpit SMTP server is available.
func isMailpitAvailable() bool {
	dialer := net.Dialer{
		Timeout: 2 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", "localhost:1025")
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// GetMailpitConfig returns the Mailpit configuration for tests.
func GetMailpitConfig() config.SMTPConfig {
	return config.SMTPConfig{
		Host:     "localhost",
		Port:     1025,
		From:     "noreply@linka.local",
		Username: "admin",
		Password: "smtppass",
		Timeout:  10 * time.Second,
		TLS:      false,
	}
}

// ============================================================
// ТЕСТЫ
// ============================================================

func TestMailpit_SendEmailVerify(t *testing.T) {
	if !isMailpitAvailable() {
		t.Skip("Mailpit not available, skipping test")
	}

	cfg := GetMailpitConfig()
	sender, err := NewSMTPSender(cfg, "http://localhost:3000")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = sender.Send(ctx, "test@example.com", domain.EmailVerify, domain.EmailData{
		Token:    "verify-token-123",
		Username: "TestUser",
		Email:    "test@example.com",
	})

	assert.NoError(t, err, "Should send email verification successfully")
	t.Log("✓ Email verification sent successfully")
}

func TestMailpit_SendPasswordReset(t *testing.T) {
	if !isMailpitAvailable() {
		t.Skip("Mailpit not available, skipping test")
	}

	cfg := GetMailpitConfig()
	sender, err := NewSMTPSender(cfg, "http://localhost:3000")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = sender.Send(ctx, "test@example.com", domain.PasswordReset, domain.EmailData{
		Token:    "reset-token-456",
		Username: "TestUser",
		Email:    "test@example.com",
	})

	assert.NoError(t, err, "Should send password reset successfully")
	t.Log("✓ Password reset sent successfully")
}

func TestMailpit_SendEmailChange(t *testing.T) {
	if !isMailpitAvailable() {
		t.Skip("Mailpit not available, skipping test")
	}

	cfg := GetMailpitConfig()
	sender, err := NewSMTPSender(cfg, "http://localhost:3000")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = sender.Send(ctx, "test@example.com", domain.EmailChange, domain.EmailData{
		Token:    "change-token-789",
		Username: "TestUser",
		Email:    "old@example.com",
		NewEmail: "new@example.com",
	})

	assert.NoError(t, err, "Should send email change successfully")
	t.Log("✓ Email change sent successfully")
}

func TestMailpit_SendAllTemplates(t *testing.T) {
	if !isMailpitAvailable() {
		t.Skip("Mailpit not available, skipping test")
	}

	cfg := GetMailpitConfig()
	sender, err := NewSMTPSender(cfg, "http://localhost:3000")
	require.NoError(t, err)

	tests := []struct {
		name     string
		template domain.Template
		data     domain.EmailData
	}{
		{
			name:     "Email Verification",
			template: domain.EmailVerify,
			data: domain.EmailData{
				Token:    "verify-token-123",
				Username: "TestUser",
				Email:    "test@example.com",
			},
		},
		{
			name:     "Password Reset",
			template: domain.PasswordReset,
			data: domain.EmailData{
				Token:    "reset-token-456",
				Username: "TestUser",
				Email:    "test@example.com",
			},
		},
		{
			name:     "Email Change",
			template: domain.EmailChange,
			data: domain.EmailData{
				Token:    "change-token-789",
				Username: "TestUser",
				Email:    "old@example.com",
				NewEmail: "new@example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			err := sender.Send(ctx, "recipient@example.com", tt.template, tt.data)

			assert.NoError(t, err, "Should send %s successfully", tt.name)
			t.Logf("✓ %s sent successfully", tt.name)
		})
	}
}

func TestMailpit_SendWithSpecialCharacters(t *testing.T) {
	if !isMailpitAvailable() {
		t.Skip("Mailpit not available, skipping test")
	}

	cfg := GetMailpitConfig()
	sender, err := NewSMTPSender(cfg, "http://localhost:3000")
	require.NoError(t, err)

	// Данные со спецсимволами
	username := "Тест Пользователь!"
	email := "user+test@example.com"
	newEmail := "new+user@example.com"
	token := "token-with-🚀-emoji"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = sender.Send(ctx, "test@example.com", domain.EmailChange, domain.EmailData{
		Token:    token,
		Username: username,
		Email:    email,
		NewEmail: newEmail,
	})

	assert.NoError(t, err, "Should send email with special characters")
	t.Log("✓ Special characters email sent successfully")
}

func TestMailpit_SendWithEmptyData(t *testing.T) {
	if !isMailpitAvailable() {
		t.Skip("Mailpit not available, skipping test")
	}

	cfg := GetMailpitConfig()
	sender, err := NewSMTPSender(cfg, "http://localhost:3000")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = sender.Send(ctx, "test@example.com", domain.EmailVerify, domain.EmailData{
		Token: "minimal-token",
		// Username, Email - пустые
	})

	assert.NoError(t, err, "Should send email with minimal data")
	t.Log("✓ Minimal data email sent successfully")
}

func TestMailpit_SendMultipleRecipients(t *testing.T) {
	if !isMailpitAvailable() {
		t.Skip("Mailpit not available, skipping test")
	}

	cfg := GetMailpitConfig()
	sender, err := NewSMTPSender(cfg, "http://localhost:3000")
	require.NoError(t, err)

	recipients := []string{
		"user1@example.com",
		"user2@example.com",
		"user3@example.com",
	}

	for _, recipient := range recipients {
		t.Run("recipient_"+recipient, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err := sender.Send(ctx, recipient, domain.EmailVerify, domain.EmailData{
				Token:    "test-token-" + recipient,
				Username: "TestUser",
				Email:    recipient,
			})

			assert.NoError(t, err, "Should send email to %s", recipient)
			t.Logf("✓ Email sent to %s", recipient)
		})
	}
}

func TestMailpit_ConcurrentSends(t *testing.T) {
	if !isMailpitAvailable() {
		t.Skip("Mailpit not available, skipping test")
	}

	cfg := GetMailpitConfig()
	sender, err := NewSMTPSender(cfg, "http://localhost:3000")
	require.NoError(t, err)

	var wg sync.WaitGroup
	numGoroutines := 5
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err := sender.Send(ctx,
				"test@example.com",
				domain.EmailVerify,
				domain.EmailData{
					Token:    "concurrent-token-" + string(rune('A'+id)),
					Username: "TestUser",
					Email:    "test@example.com",
				},
			)

			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}

	assert.Empty(t, errs, "All concurrent sends should succeed")
	t.Logf("✓ %d concurrent emails sent successfully", numGoroutines)
}

func TestMailpit_InvalidEmail(t *testing.T) {
	if !isMailpitAvailable() {
		t.Skip("Mailpit not available, skipping test")
	}

	cfg := GetMailpitConfig()
	sender, err := NewSMTPSender(cfg, "http://localhost:3000")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = sender.Send(ctx, "invalid-email", domain.EmailVerify, domain.EmailData{
		Token:    "test-token",
		Username: "TestUser",
		Email:    "test@example.com",
	})

	assert.Error(t, err, "Should fail with invalid email")
	assert.Contains(t, err.Error(), "invalid recipient email")
	t.Log("✓ Invalid email correctly rejected")
}

func TestMailpit_TemplateNotFound(t *testing.T) {
	if !isMailpitAvailable() {
		t.Skip("Mailpit not available, skipping test")
	}

	cfg := GetMailpitConfig()
	sender, err := NewSMTPSender(cfg, "http://localhost:3000")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Используем несуществующий шаблон
	err = sender.Send(ctx, "test@example.com", "nonexistent", domain.EmailData{
		Token:    "test-token",
		Username: "TestUser",
		Email:    "test@example.com",
	})

	assert.Error(t, err, "Should fail with template not found")
	assert.Contains(t, err.Error(), "template not found")
	t.Log("✓ Template not found correctly handled")
}

func TestMailpit_EmptyHTML(t *testing.T) {
	if !isMailpitAvailable() {
		t.Skip("Mailpit not available, skipping test")
	}

	cfg := GetMailpitConfig()
	sender, err := NewSMTPSender(cfg, "http://localhost:3000")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = sender.Send(ctx, "test@example.com", domain.EmailVerify, domain.EmailData{
		Token:    "",
		Username: "",
		Email:    "",
	})

	assert.NoError(t, err, "Should handle empty data gracefully")
	t.Log("✓ Empty data handled gracefully")
}
