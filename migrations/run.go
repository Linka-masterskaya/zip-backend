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
	if err := goose.Up(db, "."); err != nil {
		return err
	}
	log.Println("Migrations applied successfully")
	return nil
}
