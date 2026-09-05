package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	moderncsqlite "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
	*sqliteAdminDomain
	*sqliteAgentDomain
	*sqliteMonitoringDomain
	*sqliteNotificationDomain
	*sqliteSchemaStore
	*sqliteWriteState
}

type sqliteSchemaStore struct {
	db *sql.DB
}

type sqliteAdminDomain struct {
	*sqliteAdminAlertRules
	*sqliteAdminAuth
	*sqliteAdminDeletion
	*sqliteSettings
	agentAccess *sqliteAgentAccess
	db          *sql.DB
}

type sqliteAgentDomain struct {
	*sqliteAgentAccess
	db               *sql.DB
	telemetryStorage *telemetryStorageGuard
	writes           *sqliteWriteState
}

type sqliteMonitoringDomain struct {
	*sqliteHistoryStore
	*sqliteLatencyQueries
	*sqliteReadQueries
	db           *sql.DB
	summaryCache sqliteSummaryCache
	writes       *sqliteWriteState
}

type sqliteNotificationDomain struct {
	*sqliteNotificationAuthority
	*sqliteRenewalNotifications
	db                      *sql.DB
	notificationCredentials notificationCredentialState
	writes                  *sqliteWriteState
}

type sqliteWriteState struct {
	scheduler   agentWriteScheduler
	busyRetries atomic.Uint64
}

const (
	nodeHeartbeatOfflineAfter = 60 * time.Second
	// Node state remains live at the Agent cadence, while the expensive rolling
	// 24-hour loss/reporting aggregates are reused for this bounded interval.
	// Management writes still hard-invalidate incompatible aggregate snapshots.
	summaryAggregateFreshFor = 30 * time.Second
	// Keep SQLite's one-writer authority behind a fair, bounded scheduler shared
	// by Agent writes and recurring maintenance. Short busy waits still cover
	// external SQLite writers without allowing an unbounded process-global queue.
	sqliteAgentWriteTimeout    = 8 * time.Second
	sqliteBusyRetryFor         = 6 * time.Second
	sqliteBusyRetryInitial     = 25 * time.Millisecond
	sqliteBusyRetryMax         = 250 * time.Millisecond
	historyRetentionWriteKey   = "_history_retention"
	notificationOutboxWriteKey = "_notification_outbox"
	adminDeletionWriteKey      = "_admin_deletion"
)

func (s *sqliteWriteState) withAgentWrite(ctx context.Context, nodeID string, operation func(context.Context) error) error {
	writeCtx, cancel := context.WithTimeout(ctx, sqliteAgentWriteTimeout)
	defer cancel()
	release, err := s.scheduler.acquire(writeCtx, nodeID)
	if err != nil {
		return err
	}
	defer release()

	return retrySQLiteBusyObserved(writeCtx, func() error {
		if err := writeCtx.Err(); err != nil {
			return err
		}
		return operation(writeCtx)
	}, func() { s.busyRetries.Add(1) })
}

func withAgentWriteResult[T any](state *sqliteWriteState, ctx context.Context, nodeID string, operation func(context.Context) (T, error)) (T, error) {
	var result T
	err := state.withAgentWrite(ctx, nodeID, func(writeCtx context.Context) error {
		var operationErr error
		result, operationErr = operation(writeCtx)
		return operationErr
	})
	return result, err
}

func retrySQLiteBusy(ctx context.Context, operation func() error) error {
	return retrySQLiteBusyObserved(ctx, operation, nil)
}

func retrySQLiteBusyObserved(ctx context.Context, operation func() error, onRetry func()) error {
	started := time.Now()
	delay := sqliteBusyRetryInitial
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation()
		if err == nil || !isSQLiteBusyError(err) {
			return err
		}
		if onRetry != nil {
			onRetry()
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		remaining := sqliteBusyRetryFor - time.Since(started)
		if remaining <= 0 {
			return err
		}
		sleepFor := delay
		if sleepFor > remaining {
			sleepFor = remaining
		}
		timer := time.NewTimer(sleepFor)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
		if delay < sqliteBusyRetryMax {
			delay *= 2
			if delay > sqliteBusyRetryMax {
				delay = sqliteBusyRetryMax
			}
		}
	}
}

func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *moderncsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case 5, 6: // SQLITE_BUSY or SQLITE_LOCKED, including extended result codes.
			return true
		}
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
}

func OpenSQLiteStore(path string) (*SQLiteStore, error) {
	dsn, err := sqliteDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Schema startup is intentionally single-connection so stage-level
	// total_changes() deltas remain accurate. The normal concurrent pool is
	// restored after the schema is ready.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	store := newSQLiteStore(db, newTelemetryStorageGuard(path))
	if err := store.ensureSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	// WAL supports concurrent readers while SQLite still serializes writes.
	// Keeping a small pool prevents history/chart reads from queueing behind
	// Agent writes and summary refreshes on one shared connection.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	store.startAdminDeletionWorker()
	return store, nil
}

func newSQLiteStore(db *sql.DB, telemetryStorage *telemetryStorageGuard) *SQLiteStore {
	agentAccess := &sqliteAgentAccess{db: db}
	latencyQueries := &sqliteLatencyQueries{db: db}
	writes := &sqliteWriteState{}
	return &SQLiteStore{
		db:                db,
		sqliteSchemaStore: &sqliteSchemaStore{db: db},
		sqliteWriteState:  writes,
		sqliteAdminDomain: &sqliteAdminDomain{
			sqliteAdminAlertRules: &sqliteAdminAlertRules{db: db},
			sqliteAdminAuth:       &sqliteAdminAuth{db: db},
			sqliteAdminDeletion:   &sqliteAdminDeletion{db: db, writes: writes},
			sqliteSettings:        &sqliteSettings{db: db},
			agentAccess:           agentAccess,
			db:                    db,
		},
		sqliteAgentDomain: &sqliteAgentDomain{
			sqliteAgentAccess: agentAccess,
			db:                db,
			telemetryStorage:  telemetryStorage,
			writes:            writes,
		},
		sqliteMonitoringDomain: &sqliteMonitoringDomain{
			sqliteHistoryStore:   &sqliteHistoryStore{db: db, writes: writes},
			sqliteLatencyQueries: latencyQueries,
			sqliteReadQueries:    &sqliteReadQueries{db: db, latency: latencyQueries},
			db:                   db,
			writes:               writes,
		},
		sqliteNotificationDomain: &sqliteNotificationDomain{
			sqliteNotificationAuthority: &sqliteNotificationAuthority{db: db},
			sqliteRenewalNotifications:  &sqliteRenewalNotifications{db: db},
			db:                          db,
			writes:                      writes,
		},
	}
}

func sqliteDSN(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("sqlite path is required")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	values := url.Values{}
	values.Add("_pragma", "foreign_keys(1)")
	values.Add("_pragma", "busy_timeout(1000)")
	return (&url.URL{Scheme: "file", Path: absolutePath, RawQuery: values.Encode()}).String(), nil
}

func (s *SQLiteStore) Close() error {
	if s.sqliteAdminDeletion != nil {
		s.stopAdminDeletionWorker()
	}
	return s.db.Close()
}

func (s *SQLiteStore) Ready(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite store is closed")
	}
	var one int
	if err := s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		return err
	}
	if one != 1 {
		return fmt.Errorf("sqlite readiness probe returned %d", one)
	}
	var tableName string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'nodes'`).Scan(&tableName); err != nil {
		return err
	}
	if tableName != "nodes" {
		return fmt.Errorf("sqlite readiness schema missing nodes table")
	}
	return nil
}

func (s *SQLiteStore) QuickCheck(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite store is closed")
	}
	var result string
	if err := s.db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("sqlite quick_check: %s", result)
	}
	return nil
}

func (s *SQLiteStore) String() string {
	return fmt.Sprintf("SQLiteStore(%p)", s)
}
