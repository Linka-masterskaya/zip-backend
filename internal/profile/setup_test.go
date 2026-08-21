// internal/profile/setup_test.go
package profile

import (
	"context"
	"database/sql"
	"log"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	"github.com/Linka-masterskaya/zip-backend/migrations"
)

var (
	testDB      *sql.DB
	testPool    *pgxpool.Pool
	testCrypto  *cryptox.Cryptox
	testCleanup func()
)

// TestMain - общая инициализация для всех тестов.
func TestMain(m *testing.M) {
	// Запускаем основную логику и получаем код выхода
	code := run(m)
	os.Exit(code)
}

// run содержит всю логику инициализации и запуска тестов.
func run(m *testing.M) int {
	ctx := context.Background()

	// Инициализация базы данных
	var err error
	testPool, testCleanup, err = testutil.NewPostgresCtx(ctx)
	if err != nil {
		log.Printf("failed to start postgres container: %v", err)
		return 1
	}
	// Явно вызываем cleanup перед выходом
	defer testCleanup()

	// Открываем SQL соединение для миграций
	testDB, err = sql.Open("pgx", testPool.Config().ConnString())
	if err != nil {
		log.Printf("failed to open sql connection: %v", err)
		return 1
	}
	defer testDB.Close()

	// Применяем миграции
	if err := migrations.Run(testDB); err != nil {
		log.Printf("failed to run migrations: %v", err)
		return 1
	}

	// Инициализируем криптографию для всех тестов
	aesKey := make([]byte, 32)
	hmacKey := make([]byte, 32)
	for i := range aesKey {
		aesKey[i] = byte(i)
		hmacKey[i] = byte(i + 32)
	}
	testCrypto, err = cryptox.New(aesKey, hmacKey)
	if err != nil {
		log.Printf("failed to create crypto: %v", err)
		return 1
	}

	// Запускаем тесты
	return m.Run()
}

// getTestDB возвращает подключение к БД для тестов.
func getTestDB() *sql.DB {
	return testDB
}

// getTestPool возвращает пул соединений для тестов.
func getTestPool() *pgxpool.Pool {
	return testPool
}

// getTestCrypto возвращает экземпляр Cryptox для тестов.
func getTestCrypto() *cryptox.Cryptox {
	return testCrypto
}

// getTestContext возвращает контекст для тестов.
func getTestContext() context.Context {
	return context.Background()
}
