package mailer

import (
	"context"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/metrics"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// ГЛОБАЛЬНЫЕ ПЕРЕМЕННЫЕ ДЛЯ ТЕСТОВ
// ============================================================

var (
	testSender   *SMTPSender
	testCache    *cache.Client
	testCleanup  func()
	testCfg      config.SMTPConfig
	mailpitReady bool
)

// ============================================================
// ИНИЦИАЛИЗАЦИЯ (TestMain)
// ============================================================

func TestMain(m *testing.M) {
	// Инициализация метрик
	metrics.Initialize()

	// Проверяем доступность Mailpit
	mailpitReady = isMailpitAvailable()

	if mailpitReady {
		testCfg = GetMailpitConfig()

		// Поднимаем Redis через testcontainers
		redisClient, cleanupRedis, err := testutil.NewRedisNoT()
		if err != nil {
			panic("failed to create Redis container: " + err.Error())
		}

		testCache = cache.NewClientFromRedis(redisClient)

		var errSender error
		testSender, errSender = NewSMTPSender(testCfg, "http://localhost:3000", testCache)
		if errSender != nil {
			panic("failed to create test sender: " + errSender.Error())
		}

		testCleanup = func() {
			testCache.Close()
			cleanupRedis()
		}
	}

	// Запускаем тесты
	code := m.Run()

	// Выполняем cleanup после всех тестов
	if testCleanup != nil {
		testCleanup()
	}

	os.Exit(code)
}

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
		Host:       "localhost",
		Port:       1025,
		From:       "noreply@linka.local",
		Username:   "admin",
		Password:   "smtppass",
		Timeout:    10 * time.Second,
		TLS:        false,
		DailyLimit: 300,
	}
}

// ============================================================
// ТЕСТЫ
// ============================================================

func TestMailpit_SendEmailVerify(t *testing.T) {
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := testSender.Send(ctx, "test@example.com", EmailVerify, EmailData{
		Token:    "verify-token-123",
		Username: "TestUser",
		Email:    "test@example.com",
	})

	assert.NoError(t, err, "Should send email verification successfully")
	t.Log("✓ Email verification sent successfully")
}

func TestMailpit_SendPasswordReset(t *testing.T) {
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := testSender.Send(ctx, "test@example.com", PasswordReset, EmailData{
		Token:    "reset-token-456",
		Username: "TestUser",
		Email:    "test@example.com",
	})

	assert.NoError(t, err, "Should send password reset successfully")
	t.Log("✓ Password reset sent successfully")
}

func TestMailpit_SendEmailChange(t *testing.T) {
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := testSender.Send(ctx, "test@example.com", EmailChange, EmailData{
		Token:    "change-token-789",
		Username: "TestUser",
		Email:    "old@example.com",
		NewEmail: "new@example.com",
	})

	assert.NoError(t, err, "Should send email change successfully")
	t.Log("✓ Email change sent successfully")
}

func TestMailpit_SendAllTemplates(t *testing.T) {
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	tests := []struct {
		name     string
		template Template
		data     EmailData
	}{
		{
			name:     "Email Verification",
			template: EmailVerify,
			data: EmailData{
				Token:    "verify-token-123",
				Username: "TestUser",
				Email:    "test@example.com",
			},
		},
		{
			name:     "Password Reset",
			template: PasswordReset,
			data: EmailData{
				Token:    "reset-token-456",
				Username: "TestUser",
				Email:    "test@example.com",
			},
		},
		{
			name:     "Email Change",
			template: EmailChange,
			data: EmailData{
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

			err := testSender.Send(ctx, "recipient@example.com", tt.template, tt.data)

			assert.NoError(t, err, "Should send %s successfully", tt.name)
			t.Logf("✓ %s sent successfully", tt.name)
		})
	}
}

func TestMailpit_SendWithSpecialCharacters(t *testing.T) {
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	username := "Тест Пользователь!"
	email := "user+test@example.com"
	newEmail := "new+user@example.com"
	token := "token-with-🚀-emoji"

	err := testSender.Send(ctx, "test@example.com", EmailChange, EmailData{
		Token:    token,
		Username: username,
		Email:    email,
		NewEmail: newEmail,
	})

	assert.NoError(t, err, "Should send email with special characters")
	t.Log("✓ Special characters email sent successfully")
}

func TestMailpit_SendWithEmptyData(t *testing.T) {
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := testSender.Send(ctx, "test@example.com", EmailVerify, EmailData{
		Token: "minimal-token",
	})

	assert.NoError(t, err, "Should send email with minimal data")
	t.Log("✓ Minimal data email sent successfully")
}

func TestMailpit_SendMultipleRecipients(t *testing.T) {
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	recipients := []string{
		"user1@example.com",
		"user2@example.com",
		"user3@example.com",
	}

	for _, recipient := range recipients {
		t.Run("recipient_"+recipient, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err := testSender.Send(ctx, recipient, EmailVerify, EmailData{
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
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	var wg sync.WaitGroup
	numGoroutines := 5
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err := testSender.Send(ctx,
				"test@example.com",
				EmailVerify,
				EmailData{
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
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := testSender.Send(ctx, "invalid-email", EmailVerify, EmailData{
		Token:    "test-token",
		Username: "TestUser",
		Email:    "test@example.com",
	})

	assert.Error(t, err, "Should fail with invalid email")
	assert.Contains(t, err.Error(), "invalid recipient email")
	t.Log("✓ Invalid email correctly rejected")
}

func TestMailpit_TemplateNotFound(t *testing.T) {
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := testSender.Send(ctx, "test@example.com", "nonexistent", EmailData{
		Token:    "test-token",
		Username: "TestUser",
		Email:    "test@example.com",
	})

	assert.Error(t, err, "Should fail with template not found")
	assert.Contains(t, err.Error(), "template not found")
	t.Log("✓ Template not found correctly handled")
}

func TestMailpit_EmptyHTML(t *testing.T) {
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := testSender.Send(ctx, "test@example.com", EmailVerify, EmailData{
		Token:    "",
		Username: "",
		Email:    "",
	})

	assert.NoError(t, err, "Should handle empty data gracefully")
	t.Log("✓ Empty data handled gracefully")
}

func TestMailpit_RedisCounterIncrement(t *testing.T) {
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Получаем текущее значение счетчика
	initialCount, err := testCache.GetEmailSentToday(ctx)
	require.NoError(t, err)

	// Отправляем письмо
	err = testSender.Send(ctx, "test@example.com", EmailVerify, EmailData{
		Token:    "test-token",
		Username: "TestUser",
		Email:    "test@example.com",
	})
	require.NoError(t, err)

	// Проверяем, что счетчик увеличился
	newCount, err := testCache.GetEmailSentToday(ctx)
	require.NoError(t, err)
	assert.Equal(t, initialCount+1, newCount, "Redis counter should be incremented")

	t.Log("✓ Redis counter incremented successfully")
}

func TestMailpit_RedisCounterMultipleEmails(t *testing.T) {
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Получаем текущее значение
	initialCount, err := testCache.GetEmailSentToday(ctx)
	require.NoError(t, err)

	// Отправляем 3 письма
	for i := 0; i < 3; i++ {
		err := testSender.Send(ctx, "test@example.com", EmailVerify, EmailData{
			Token:    "test-token-" + string(rune('A'+i)),
			Username: "TestUser",
			Email:    "test@example.com",
		})
		require.NoError(t, err)
	}

	// Проверяем, что счетчик увеличился на 3
	newCount, err := testCache.GetEmailSentToday(ctx)
	require.NoError(t, err)
	assert.Equal(t, initialCount+3, newCount, "Redis counter should be incremented by 3")

	t.Log("✓ Redis counter correctly counts multiple emails")
}

func TestMailpit_EmailCounterMetric(t *testing.T) {
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Отправляем письмо
	err := testSender.Send(ctx, "test@example.com", EmailVerify, EmailData{
		Token:    "test-token",
		Username: "TestUser",
		Email:    "test@example.com",
	})
	require.NoError(t, err)

	// Проверяем, что счетчик в Redis увеличился
	count, err := testCache.GetEmailSentToday(ctx)
	require.NoError(t, err)
	assert.Greater(t, count, int64(0), "Counter should be > 0")

	t.Log("✓ Email counter metric updated successfully")
}

// ============================================================
// ТЕСТЫ БЕЗ REDIS (используют отдельный sender)
// ============================================================

func TestMailpit_SenderWithoutRedis(t *testing.T) {
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	cfg := GetMailpitConfig()
	sender, err := NewSMTPSender(cfg, "http://localhost:3000", nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = sender.Send(ctx, "test@example.com", EmailVerify, EmailData{
		Token:    "test-token",
		Username: "TestUser",
		Email:    "test@example.com",
	})

	assert.NoError(t, err, "Should send email without Redis")
	t.Log("✓ Email sent successfully without Redis")
}

func TestMailpit_RedisCounterWithoutCache(t *testing.T) {
	if !mailpitReady {
		t.Skip("Mailpit not available, skipping test")
	}

	cfg := GetMailpitConfig()
	sender, err := NewSMTPSender(cfg, "http://localhost:3000", nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = sender.Send(ctx, "test@example.com", EmailVerify, EmailData{
		Token:    "test-token",
		Username: "TestUser",
		Email:    "test@example.com",
	})
	require.NoError(t, err)

	assert.Nil(t, sender.cache, "cache should be nil")
	t.Log("✓ Sender works without Redis without panic")
}
