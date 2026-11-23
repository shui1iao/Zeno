package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	moderncsqlite "modernc.org/sqlite"
)

const sqliteReadPoolMaxConns = 4

type SQLiteStore struct {
	db     *sql.DB
	readDB *sql.DB
	*sqliteAdminDomain
	*sqliteAgentDomain
	*sqliteMonitoringDomain
	*sqliteNotificationDomain
	*sqliteSchemaStore
	*sqliteWriteState
	walCheckpoint *sqliteWALCheckpointer
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
	readDB      *sql.DB
}

type sqliteAgentDomain struct {
	*sqliteAgentAccess
	db               *sql.DB
	readDB           *sql.DB
	telemetryStorage *telemetryStorageGuard
	writes           *sqliteWriteState
}

type sqliteMonitoringDomain struct {
	*sqliteHistoryStore
	*sqliteLatencyQueries
	*sqliteReadQueries
	db           *sql.DB
	readDB       *sql.DB
	summaryCache sqliteSummaryCache
	writes       *sqliteWriteState
}

type sqliteNotificationDomain struct {
	*sqliteNotificationAuthority
	db                      *sql.DB
	readDB                  *sql.DB
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
	sqliteAgentWriteTimeout  = 18 * time.Second
	sqliteBusyRetryFor       = 12 * time.Second
	sqliteBusyRetryInitial   = 25 * time.Millisecond
	sqliteBusyRetryMax       = 250 * time.Millisecond
	historyRetentionWriteKey = "_history_retention"
	adminDeletionWriteKey    = "_admin_deletion"
	walCheckpointWriteKey    = "_wal_checkpoint"

	defaultWALCheckpointInterval    = 2 * time.Minute
	defaultWALTruncateMinBytes      = 16 * 1024 * 1024
	defaultWALTruncateMinInterval   = 10 * time.Minute
	walCheckpointSchedulerWaitLimit = 500 * time.Millisecond
	walCheckpointPassiveTimeout     = 1500 * time.Millisecond
	walCheckpointTruncateTimeout    = 750 * time.Millisecond
	walCheckpointTruncateBusyMS     = 100
	sqliteWriterBusyTimeoutMS       = 1000
	sqliteReaderBusyTimeoutMS       = 5000
	sqliteWriterCacheSizeKiB        = -16384
	sqliteReaderCacheSizeKiB        = -32768
	sqliteMMapSizeBytes             = 268435456
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
	memoryDB := isSQLiteMemoryPath(path)
	dsn, walPath, err := sqliteWriteDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Keep the write authority strictly single-connection (MaxOpenConns=1) so
	// stage-level total_changes() deltas stay exact at startup and runtime write
	// transactions never contend with read queries on the same connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	pragmas := []string{
		`PRAGMA foreign_keys = ON;`,
		`PRAGMA busy_timeout = 1000;`,
		`PRAGMA temp_store = MEMORY;`,
		fmt.Sprintf(`PRAGMA cache_size = %d;`, sqliteWriterCacheSizeKiB),
	}
	if !memoryDB {
		pragmas = append(pragmas, fmt.Sprintf(`PRAGMA mmap_size = %d;`, sqliteMMapSizeBytes))
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, err
		}
	}

	var guard *telemetryStorageGuard
	if !memoryDB {
		guard = newTelemetryStorageGuard(path)
	}
	store := newSQLiteStore(db, guard)
	if err := store.ensureSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA synchronous = FULL;`); err != nil {
		_ = db.Close()
		return nil, err
	}

	if !memoryDB {
		readDSN, err := sqliteReadDSN(path)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		readDB, err := sql.Open("sqlite", readDSN)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		readDB.SetMaxOpenConns(sqliteReadPoolMaxConns)
		readDB.SetMaxIdleConns(sqliteReadPoolMaxConns)
		var one int
		if err := readDB.QueryRow(`SELECT 1`).Scan(&one); err != nil {
			_ = readDB.Close()
			_ = db.Close()
			return nil, err
		}
		store.attachReadPool(readDB)
		store.walCheckpoint = newSQLiteWALCheckpointer(db, store.sqliteWriteState, walPath)
		store.walCheckpoint.start()
	}

	store.startAdminDeletionWorker()
	return store, nil
}

func newSQLiteStore(db *sql.DB, telemetryStorage *telemetryStorageGuard) *SQLiteStore {
	agentAccess := &sqliteAgentAccess{db: db, readDB: db}
	latencyQueries := &sqliteLatencyQueries{db: db}
	readQueries := &sqliteReadQueries{db: db, latency: latencyQueries}
	writes := &sqliteWriteState{}
	return &SQLiteStore{
		db:                db,
		readDB:            db,
		sqliteSchemaStore: &sqliteSchemaStore{db: db},
		sqliteWriteState:  writes,
		sqliteAdminDomain: &sqliteAdminDomain{
			sqliteAdminAlertRules: &sqliteAdminAlertRules{db: db, readDB: db},
			sqliteAdminAuth:       &sqliteAdminAuth{db: db},
			sqliteAdminDeletion:   &sqliteAdminDeletion{db: db, writes: writes},
			sqliteSettings:        &sqliteSettings{db: db, readDB: db},
			agentAccess:           agentAccess,
			db:                    db,
			readDB:                db,
		},
		sqliteAgentDomain: &sqliteAgentDomain{
			sqliteAgentAccess: agentAccess,
			db:                db,
			readDB:            db,
			telemetryStorage:  telemetryStorage,
			writes:            writes,
		},
		sqliteMonitoringDomain: &sqliteMonitoringDomain{
			sqliteHistoryStore:   &sqliteHistoryStore{db: db, writes: writes},
			sqliteLatencyQueries: latencyQueries,
			sqliteReadQueries:    readQueries,
			db:                   db,
			readDB:               db,
			writes:               writes,
		},
		sqliteNotificationDomain: &sqliteNotificationDomain{
			sqliteNotificationAuthority: &sqliteNotificationAuthority{db: db},
			db:                          db,
			readDB:                      db,
			writes:                      writes,
		},
	}
}

func (s *SQLiteStore) attachReadPool(readDB *sql.DB) {
	if s == nil || readDB == nil {
		return
	}
	s.readDB = readDB
	if s.sqliteAdminDomain != nil {
		s.sqliteAdminDomain.readDB = readDB
		if s.sqliteAdminAlertRules != nil {
			s.sqliteAdminAlertRules.readDB = readDB
		}
		if s.sqliteSettings != nil {
			s.sqliteSettings.readDB = readDB
		}
		if s.sqliteAdminDomain.agentAccess != nil {
			s.sqliteAdminDomain.agentAccess.readDB = readDB
		}
	}
	if s.sqliteAgentDomain != nil {
		s.sqliteAgentDomain.readDB = readDB
		if s.sqliteAgentAccess != nil {
			s.sqliteAgentAccess.readDB = readDB
		}
	}
	if s.sqliteMonitoringDomain != nil {
		s.sqliteMonitoringDomain.readDB = readDB
		if s.sqliteLatencyQueries != nil {
			s.sqliteLatencyQueries.db = readDB
		}
		if s.sqliteReadQueries != nil {
			s.sqliteReadQueries.db = readDB
		}
	}
	if s.sqliteNotificationDomain != nil {
		s.sqliteNotificationDomain.readDB = readDB
	}
}

func isSQLiteMemoryPath(path string) bool {
	trimmed := strings.TrimSpace(path)
	lower := strings.ToLower(trimmed)
	if lower == ":memory:" || strings.HasPrefix(lower, "file::memory:") {
		return true
	}
	return strings.HasPrefix(lower, "file:") && strings.Contains(lower, "mode=memory")
}

func resolveSQLiteFilePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("sqlite path is required")
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "file:") {
		parsed, err := url.Parse(trimmed)
		if err == nil && parsed.Path != "" {
			return filepath.Abs(parsed.Path)
		}
	}
	return filepath.Abs(trimmed)
}

func sqliteDSN(path string) (string, error) {
	dsn, _, err := sqliteWriteDSN(path)
	return dsn, err
}

func sqliteWriteDSN(path string) (string, string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", "", fmt.Errorf("sqlite path is required")
	}
	if isSQLiteMemoryPath(trimmed) {
		return trimmed, "", nil
	}
	absolutePath, err := resolveSQLiteFilePath(trimmed)
	if err != nil {
		return "", "", err
	}
	values := url.Values{}
	values.Add("_pragma", "foreign_keys(1)")
	values.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", sqliteWriterBusyTimeoutMS))
	values.Add("_pragma", "synchronous(FULL)")
	values.Add("_pragma", "temp_store(MEMORY)")
	values.Add("_pragma", fmt.Sprintf("cache_size(%d)", sqliteWriterCacheSizeKiB))
	values.Add("_pragma", fmt.Sprintf("mmap_size(%d)", sqliteMMapSizeBytes))
	return (&url.URL{Scheme: "file", Path: absolutePath, RawQuery: values.Encode()}).String(), absolutePath + "-wal", nil
}

func sqliteReadDSN(path string) (string, error) {
	absolutePath, err := resolveSQLiteFilePath(path)
	if err != nil {
		return "", err
	}
	values := url.Values{}
	values.Add("_pragma", "foreign_keys(1)")
	values.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", sqliteReaderBusyTimeoutMS))
	values.Add("_pragma", "query_only(1)")
	values.Add("_pragma", "temp_store(MEMORY)")
	values.Add("_pragma", fmt.Sprintf("cache_size(%d)", sqliteReaderCacheSizeKiB))
	values.Add("_pragma", fmt.Sprintf("mmap_size(%d)", sqliteMMapSizeBytes))
	return (&url.URL{Scheme: "file", Path: absolutePath, RawQuery: values.Encode()}).String(), nil
}

type sqliteWALCheckpointer struct {
	db                  *sql.DB
	writes              *sqliteWriteState
	walPath             string
	interval            time.Duration
	truncateMinBytes    int64
	truncateMinInterval time.Duration

	mu             sync.Mutex
	lastTruncateAt time.Time
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

func newSQLiteWALCheckpointer(db *sql.DB, writes *sqliteWriteState, walPath string) *sqliteWALCheckpointer {
	return &sqliteWALCheckpointer{
		db:                  db,
		writes:              writes,
		walPath:             walPath,
		interval:            defaultWALCheckpointInterval,
		truncateMinBytes:    defaultWALTruncateMinBytes,
		truncateMinInterval: defaultWALTruncateMinInterval,
	}
}

func (c *sqliteWALCheckpointer) start() {
	if c == nil || c.db == nil || c.walPath == "" {
		return
	}
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.wg.Add(1)
	interval := c.interval
	if interval <= 0 {
		interval = defaultWALCheckpointInterval
	}
	c.mu.Unlock()

	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := c.checkpointOnce(ctx, now); err != nil && ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) && !isSQLiteBusyError(err) {
					log.Printf("sqlite wal checkpoint failed: %v", err)
				}
			}
		}
	}()
}

func (c *sqliteWALCheckpointer) stop() {
	if c == nil {
		return
	}
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
		c.wg.Wait()
	}
}

func (s *SQLiteStore) checkpointWALOnce(ctx context.Context, now time.Time) error {
	if s == nil || s.walCheckpoint == nil {
		return nil
	}
	return s.walCheckpoint.checkpointOnce(ctx, now)
}

func (c *sqliteWALCheckpointer) checkpointOnce(ctx context.Context, now time.Time) error {
	if c == nil || c.db == nil || c.walPath == "" {
		return nil
	}
	acquireCtx, acquireCancel := context.WithTimeout(ctx, walCheckpointSchedulerWaitLimit)
	release, err := c.writes.scheduler.acquire(acquireCtx, walCheckpointWriteKey)
	acquireCancel()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return err
	}
	defer release()

	passiveCtx, passiveCancel := context.WithTimeout(ctx, walCheckpointPassiveTimeout)
	var busy, logFrames, checkpointedFrames int
	err = c.db.QueryRowContext(passiveCtx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &logFrames, &checkpointedFrames)
	passiveCancel()
	if err != nil {
		return err
	}

	c.mu.Lock()
	minBytes := c.truncateMinBytes
	minInterval := c.truncateMinInterval
	lastTruncate := c.lastTruncateAt
	c.mu.Unlock()

	if minBytes <= 0 {
		return nil
	}
	info, statErr := os.Stat(c.walPath)
	if statErr != nil || info.Size() < minBytes {
		return nil
	}
	if !lastTruncate.IsZero() && now.Sub(lastTruncate) < minInterval {
		return nil
	}

	c.mu.Lock()
	c.lastTruncateAt = now
	c.mu.Unlock()

	truncCtx, truncCancel := context.WithTimeout(ctx, walCheckpointTruncateTimeout)
	defer truncCancel()
	conn, err := c.db.Conn(truncCtx)
	if err != nil {
		return nil
	}
	defer conn.Close()
	if _, err := conn.ExecContext(truncCtx, fmt.Sprintf("PRAGMA busy_timeout = %d", walCheckpointTruncateBusyMS)); err != nil {
		return nil
	}
	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = conn.ExecContext(restoreCtx, fmt.Sprintf("PRAGMA busy_timeout = %d", sqliteWriterBusyTimeoutMS))
	}()
	_ = conn.QueryRowContext(truncCtx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointedFrames)
	return nil
}

func (s *SQLiteStore) Close() error {
	if s == nil {
		return nil
	}
	if s.walCheckpoint != nil {
		s.walCheckpoint.stop()
	}
	if s.sqliteAdminDeletion != nil {
		s.stopAdminDeletionWorker()
	}
	var readErr error
	if s.readDB != nil && s.readDB != s.db {
		readErr = s.readDB.Close()
	}
	var writeErr error
	if s.db != nil {
		writeErr = s.db.Close()
	}
	return errors.Join(readErr, writeErr)
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
	if s.readDB != nil && s.readDB != s.db {
		var readOne int
		if err := s.readDB.QueryRowContext(ctx, `SELECT 1`).Scan(&readOne); err != nil {
			return err
		}
		if readOne != 1 {
			return fmt.Errorf("sqlite read pool readiness probe returned %d", readOne)
		}
	}
	var tableName string
	readTarget := s.db
	if s.readDB != nil {
		readTarget = s.readDB
	}
	if err := readTarget.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'nodes'`).Scan(&tableName); err != nil {
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
