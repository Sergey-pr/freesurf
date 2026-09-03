// Package store is FreeSurf's SQLite persistence layer: servers and their nodes.
package store

import (
	"database/sql"

	"freesurf/internal/paths"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	_ "modernc.org/sqlite"
)

// goquDB is the shared connection; rawDB is the same handle, kept to close it.
var (
	goquDB *goqu.Database
	rawDB  *sql.DB
)

// InitDB opens the app's database, applies migrations, and wires the connection.
func InitDB() error {
	dbPath, err := paths.DB()
	if err != nil {
		return err
	}
	return InitDBAt(dbPath)
}

// InitDBAt opens the database at dbPath. Tests point it at a scratch file.
func InitDBAt(dbPath string) error {
	if err := CloseDB(); err != nil {
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
	// SQLite creates this world-readable, and the node URIs in it carry credentials.
	if err := paths.RestrictFile(dbPath); err != nil {
		return err
	}
	if err := migrateDB(sqlDB); err != nil {
		return err
	}

	goquDB = goqu.New("sqlite3", sqlDB)
	rawDB = sqlDB
	return nil
}

// CloseDB releases the database file; Windows cannot delete one still open.
func CloseDB() error {
	if rawDB == nil {
		return nil
	}
	err := rawDB.Close()
	rawDB, goquDB = nil, nil
	return err
}
