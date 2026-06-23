package main

import (
	"database/sql"
	"os"
	"path/filepath"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	_ "modernc.org/sqlite"
)

// goquDB is the shared database connection used by all model methods.
var goquDB *goqu.Database

func initDB() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(configDir, "FreeSurf")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	dbPath := filepath.Join(dir, "freesurf.db")

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
