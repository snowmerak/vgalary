package backend

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Store keeps the durable catalog. Vector payloads live only in Qdrant.
type Store struct{ db *sql.DB }

func openStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "catalog.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS records(kind TEXT NOT NULL,id TEXT NOT NULL,body BLOB NOT NULL,PRIMARY KEY(kind,id));
 CREATE INDEX IF NOT EXISTS catalog_asset_order ON records(
   json_extract(CAST(body AS TEXT),'$.profile_id'),
   julianday(json_extract(CAST(body AS TEXT),'$.updated_at')) DESC,id
 ) WHERE kind='asset';
 CREATE INDEX IF NOT EXISTS catalog_asset_source ON records(
   json_extract(CAST(body AS TEXT),'$.source_id'),
   json_extract(CAST(body AS TEXT),'$.profile_id')
 ) WHERE kind='asset';
 PRAGMA user_version=1;`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func (s *Store) put(ctx context.Context, kind, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO records(kind,id,body) VALUES(?,?,?) ON CONFLICT(kind,id) DO UPDATE SET body=excluded.body`, kind, id, b)
	return err
}
func (s *Store) get(ctx context.Context, kind, id string, out any) error {
	var b []byte
	if err := s.db.QueryRowContext(ctx, `SELECT body FROM records WHERE kind=? AND id=?`, kind, id).Scan(&b); err != nil {
		return err
	}
	return decodeRecord(b, out)
}

// Older saved settings, profile snapshots and durable jobs predate batching.
// Apply defaults only while loading durable records; explicit API zero is invalid.
func decodeRecord(b []byte, out any) error {
	switch record := out.(type) {
	case *Settings:
		record.Processing.EmbeddingBatchSize = DefaultSettings().Processing.EmbeddingBatchSize
	case *Profile:
		record.Settings.Processing.EmbeddingBatchSize = DefaultSettings().Processing.EmbeddingBatchSize
	case *Job:
		record.Settings.Processing.EmbeddingBatchSize = DefaultSettings().Processing.EmbeddingBatchSize
	case *cleanupTask:
		record.Settings.Processing.EmbeddingBatchSize = DefaultSettings().Processing.EmbeddingBatchSize
	}
	return json.Unmarshal(b, out)
}
func listRecords[T any](ctx context.Context, s *Store, kind string) ([]T, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM records WHERE kind=? ORDER BY id`, kind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]T, 0)
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var item T
		if err := decodeRecord(b, &item); err != nil {
			return nil, fmt.Errorf("decode %s: %w", kind, err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
func (s *Store) delete(ctx context.Context, kind, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM records WHERE kind=? AND id=?`, kind, id)
	return err
}

// Asset visibility and the intent to remove superseded vectors commit together.
func (s *Store) changeAsset(ctx context.Context, asset Asset, remove bool, cleanup *cleanupTask) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if cleanup != nil {
		b, err := json.Marshal(cleanup)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO records(kind,id,body) VALUES('cleanup',?,?)`, cleanup.ID, b); err != nil {
			return err
		}
	}
	if remove {
		_, err = tx.ExecContext(ctx, `DELETE FROM records WHERE kind='asset' AND id=?`, asset.ID)
	} else {
		var b []byte
		b, err = json.Marshal(asset)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO records(kind,id,body) VALUES('asset',?,?)`, asset.ID, b)
		}
	}
	if err != nil {
		return err
	}
	if !remove {
		if _, err = tx.ExecContext(ctx, `DELETE FROM records WHERE kind='cleanup' AND id=?`, "pending-"+asset.ID+"-"+asset.Revision); err != nil {
			return err
		}
	}
	return tx.Commit()
}
