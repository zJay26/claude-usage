package store

import (
	"context"
	"database/sql"
)

type database interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) writer() database {
	if s.tx != nil {
		return s.tx
	}
	return s.db
}

// beginWrite reuses the file transaction when an operation has several writes.
func (s *Store) beginWrite(ctx context.Context) (database, func() error, func(), error) {
	if s.tx != nil {
		return s.tx, func() error { return nil }, func() {}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	return tx, tx.Commit, func() { _ = tx.Rollback() }, nil
}

// Ingest serializes cursor reads across processes with BEGIN IMMEDIATE. Events,
// classification corrections, turn modes, progress and file offsets commit as
// one unit. A failed file can be retried without losing or duplicating usage.
func (s *Store) Ingest(ctx context.Context, fn func(*Store) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	bound := &Store{db: s.db, tx: tx, path: s.path, machine: s.machine, location: s.location}
	if err = fn(bound); err != nil {
		return err
	}
	return tx.Commit()
}

// ReadSnapshot keeps rows, costs and their revision in the same WAL snapshot.
func (s *Store) ReadSnapshot(ctx context.Context, fn func(*Store) error) error {
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(&Store{db: s.db, tx: tx, path: s.path, machine: s.machine, location: s.location})
}

func (s *Store) DataRevision(ctx context.Context) (uint64, error) {
	var revision uint64
	err := s.reader().QueryRowContext(ctx, `SELECT value FROM meta WHERE key='data_revision'`).Scan(&revision)
	return revision, err
}

// Revision is retained for internal callers that do not cache on failure.
func (s *Store) Revision() uint64 {
	revision, _ := s.DataRevision(context.Background())
	return revision
}
