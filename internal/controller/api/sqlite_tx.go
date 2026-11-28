package api

import (
	"context"
	"database/sql"
)

// Shared transaction plumbing for the SQLite stores. Every helper keeps the
// store's established idiom -- BeginTx, a deferred rollback that is skipped
// once the handle is cleared after a successful Commit -- so call sites keep
// exactly the transaction boundary they had when the idiom was written inline.

func rollbackUnlessCommitted(tx *sql.Tx) {
	if tx != nil {
		_ = tx.Rollback()
	}
}

// withSQLiteTx runs fn inside one read-write transaction on db. The
// transaction commits when fn returns nil and is rolled back otherwise;
// a Commit failure is returned unchanged.
func withSQLiteTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

// reserveSQLiteWriterTx takes SQLite's write reservation at the start of a
// deferred transaction with a no-op write, so the reads that follow are
// serialized with every other writer (Agent ingest, probe-config mutations and
// admin deletion) instead of racing them into BUSY_SNAPSHOT.
func reserveSQLiteWriterTx(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`)
	return err
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// execRequiringRows executes a statement and reports none when it changed no
// row, which is how the stores distinguish a missing (or already present, for
// INSERT OR IGNORE) entity from a successful write. Exec and RowsAffected
// errors are returned unchanged.
func execRequiringRows(ctx context.Context, execer sqlExecer, none error, query string, args ...any) error {
	return execCheckingRows(ctx, execer, func(affected int64) bool { return affected != 0 }, none, query, args...)
}

// execRequiringOneRow is execRequiringRows for call sites that require the
// statement to change exactly one row (affected != 1 reports none).
func execRequiringOneRow(ctx context.Context, execer sqlExecer, none error, query string, args ...any) error {
	return execCheckingRows(ctx, execer, func(affected int64) bool { return affected == 1 }, none, query, args...)
}

func execCheckingRows(ctx context.Context, execer sqlExecer, ok func(affected int64) bool, none error, query string, args ...any) error {
	result, err := execer.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if !ok(affected) {
		return none
	}
	return nil
}
