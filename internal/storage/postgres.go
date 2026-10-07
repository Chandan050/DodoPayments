package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "github.com/lib/pq"
)

func Open(ctx context.Context, databaseURL string) (*sql.DB, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func Migrate(db *sql.DB, directory string) error {
	files, err := filepath.Glob(filepath.Join(directory, "*.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if _, err := db.Exec(string(content)); err != nil {
			return fmt.Errorf("execute migration %s: %w", filepath.Base(file), err)
		}
	}
	return nil
}

func SeedDemoBusiness(db *sql.DB, apiKey string) error {
	hash := sha256.Sum256([]byte(apiKey))
	_, err := db.Exec(`
		INSERT INTO businesses (id, name, api_key_prefix, api_key_hash, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (id) DO NOTHING`,
		"00000000-0000-0000-0000-000000000001",
		"Demo Business",
		"dev_business",
		hex.EncodeToString(hash[:]),
	)
	return err
}
