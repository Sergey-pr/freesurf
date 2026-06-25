// Package store is FreeSurf's SQLite persistence layer: servers and their nodes.
package store

import (
	"database/sql"

	"freesurf/internal/paths"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	_ "modernc.org/sqlite"
)

// goquDB is the shared database connection used by all model methods.
var goquDB *goqu.Database

// InitDB opens the database, applies migrations, and wires the shared connection.
func InitDB() error {
	dbPath, err := paths.DB()
	if err != nil {
		return err
	}

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	sqlDB.SetMaxOpenConns(1)

	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return err
	}
	if err := sqlDB.Ping(); err != nil {
		return err
	}
	if err := migrateDB(sqlDB); err != nil {
		return err
	}

	goquDB = goqu.New("sqlite3", sqlDB)
	return nil
}
