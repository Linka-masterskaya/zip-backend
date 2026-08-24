package migrations

import (
	"database/sql"
	"log"

	"github.com/pressly/goose/v3"
)

// Run применяет миграции из встроенной FS.
func Run(db *sql.DB) error {
	goose.SetBaseFS(FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	// WithAllowMissing применяет миграции, чей timestamp меньше уже
	// применённого. Без этой опции goose останавливается с ошибкой
	// "found N missing migrations", и деплой падает всякий раз, когда
	// две ветки добавили миграции параллельно, а смержены были в
	// обратном порядке timestamp'ов.
	//
	// Появление новых расхождений ловит scripts/check-migration-order.sh
	// на PR, поэтому здесь опция нужна только чтобы применить те, что
	// уже попали в main.
	if err := goose.Up(db, ".", goose.WithAllowMissing()); err != nil {
		return err
	}
	log.Println("Migrations applied successfully")
	return nil
}
