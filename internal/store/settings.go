package store

import (
	"database/sql"
	"errors"
	"strconv"

	"github.com/doug-martin/goqu/v9"
)

var settingsTable = goqu.T("settings")

// Setting keys.
const keyAutoRefreshMinutes = "auto_refresh_minutes"

// DefaultAutoRefreshMinutes is the out-of-the-box subscription refresh interval.
const DefaultAutoRefreshMinutes = 30

type setting struct {
	Key   string `db:"key"`
	Value string `db:"value"`
}

// getSetting returns the raw string value for key, or ("", false) if unset.
func getSetting(key string) (string, bool, error) {
	var s setting
	found, err := goquDB.From(settingsTable).Where(goqu.C("key").Eq(key)).ScanStruct(&s)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	return s.Value, found, nil
}

// setSetting upserts a key/value pair.
func setSetting(key, value string) error {
	_, err := goquDB.Insert(settingsTable).
		Rows(setting{Key: key, Value: value}).
		OnConflict(goqu.DoUpdate("key", goqu.Record{"value": value})).
		Executor().Exec()
	return err
}

// GetAutoRefreshMinutes returns the configured interval, falling back to the
// default if unset or invalid.
func GetAutoRefreshMinutes() int {
	v, found, err := getSetting(keyAutoRefreshMinutes)
	if err != nil || !found {
		return DefaultAutoRefreshMinutes
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return DefaultAutoRefreshMinutes
	}
	return n
}

// SetAutoRefreshMinutes persists the interval (clamped to a sane minimum).
func SetAutoRefreshMinutes(minutes int) error {
	if minutes < 1 {
		minutes = 1
	}
	return setSetting(keyAutoRefreshMinutes, strconv.Itoa(minutes))
}
