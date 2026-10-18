package main

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const driverName = "sqlite"

const schema = `
CREATE TABLE IF NOT EXISTS category (
	id   INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS activity (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	kind        TEXT    NOT NULL CHECK (kind IN ('planned', 'done')),
	date        TEXT    NOT NULL, -- yyyy-MM-dd
	start_time  TEXT    NOT NULL, -- HH:mm
	duration    INTEGER NOT NULL, -- minutes
	name        TEXT    NOT NULL,
	category_id INTEGER NOT NULL REFERENCES category (id),
	tags        TEXT    NOT NULL DEFAULT '',
	inserted    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX IF NOT EXISTS activity_kind_date ON activity (kind, date);
`

var defaultCategories = []string{
	"Break", "Assessment", "Triage", "Alerts", "Reporting", "Compliance",
	"Incident response", "Training", "Requests", "Remediation", "Meetings",
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open(driverName, path)
	if err != nil {
		return nil, err
	}
	// One connection: this is a single-user local app, and it keeps the
	// per-connection PRAGMA below in effect for every query.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	if err := seedCategories(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func seedCategories(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM category`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, name := range defaultCategories {
		if _, err := tx.Exec(`INSERT INTO category (name) VALUES (?)`, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// backup copies the database file (if it exists yet) into backupDir, named
// with a timestamp, before the app opens it.
func backup(dbPath, backupDir, suffix string) error {
	src, err := os.Open(dbPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer src.Close()

	name := time.Now().Format("2006-01-02-15-04-05") + "-" + suffix + ".db"
	dst, err := os.OpenFile(filepath.Join(backupDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fmt.Errorf("copying %s: %w", dbPath, err)
	}
	return dst.Close()
}
