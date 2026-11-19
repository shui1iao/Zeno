package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestOpenSQLiteStoreAllowsWALReadsWhileWriterTransactionIsOpen(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	if got := store.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("writer max open connections = %d, want 1", got)
	}
	if store.readDB == nil || store.readDB == store.db {
		t.Fatal("expected dedicated readDB pool for file-backed SQLite store")
	}
	if got := store.readDB.Stats().MaxOpenConnections; got < 2 {
		t.Fatalf("reader max open connections = %d, want concurrent WAL readers", got)
	}

	var syncMode int
	if err := store.db.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&syncMode); err != nil {
		t.Fatalf("read writer synchronous pragma: %v", err)
	}
	if syncMode != 2 { // 2 = FULL
		t.Fatalf("writer PRAGMA synchronous = %d, want 2 (FULL)", syncMode)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin writer transaction: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
		t.Fatalf("acquire writer transaction: %v", err)
	}

	readCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if _, err := store.NodeLatency(readCtx, "example-node-a", latencyWindow{Name: "1h", Samples: 20, Step: 3 * time.Minute}); err != nil {
		t.Fatalf("history read blocked behind writer transaction: %v", err)
	}
	if _, err := store.Summary(readCtx); err != nil {
		t.Fatalf("public summary blocked behind writer transaction: %v", err)
	}
	if _, err := store.AdminNodes(readCtx); err != nil {
		t.Fatalf("admin nodes blocked behind writer transaction: %v", err)
	}
	if _, err := store.AdminAlertRules(readCtx); err != nil {
		t.Fatalf("admin alert rules blocked behind writer transaction: %v", err)
	}

	for index := 0; index < 3; index++ {
		conn, err := store.readDB.Conn(readCtx)
		if err != nil {
			t.Fatalf("open pooled read connection %d: %v", index, err)
		}
		var foreignKeys, queryOnly, tempStore int
		if err := conn.QueryRowContext(readCtx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			conn.Close()
			t.Fatalf("read foreign_keys on connection %d: %v", index, err)
		}
		if err := conn.QueryRowContext(readCtx, `PRAGMA query_only`).Scan(&queryOnly); err != nil {
			conn.Close()
			t.Fatalf("read query_only on connection %d: %v", index, err)
		}
		if err := conn.QueryRowContext(readCtx, `PRAGMA temp_store`).Scan(&tempStore); err != nil {
			conn.Close()
			t.Fatalf("read temp_store on connection %d: %v", index, err)
		}
		_, writeErr := conn.ExecContext(readCtx, `UPDATE probe_config_meta SET version = version + 1 WHERE id = 1`)
		conn.Close()
		if foreignKeys != 1 {
			t.Fatalf("foreign_keys on pooled read connection %d = %d, want 1", index, foreignKeys)
		}
		if queryOnly != 1 {
			t.Fatalf("query_only on pooled read connection %d = %d, want 1", index, queryOnly)
		}
		if tempStore != 2 {
			t.Fatalf("temp_store on pooled read connection %d = %d, want 2 (MEMORY)", index, tempStore)
		}
		if writeErr == nil {
			t.Fatalf("expected readDB connection %d to reject write statement", index)
		}
	}
}

func TestConcurrentProbeTargetPatchRevalidatesAfterWriterLock(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	store.db.SetMaxOpenConns(2)
	store.db.SetMaxIdleConns(2)
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	lockConn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("open lock connection: %v", err)
	}
	defer lockConn.Close()
	if _, err := lockConn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("begin immediate transaction: %v", err)
	}
	defer func() { _, _ = lockConn.ExecContext(context.Background(), `ROLLBACK`) }()

	result := make(chan error, 1)
	go func() {
		port := adminOptionalInt64{Set: true, Valid: true, Value: 5353}
		_, updateErr := store.UpdateAdminProbeTarget(ctx, "google-dns", AdminProbeTargetUpdateRequest{Port: port})
		result <- updateErr
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	for store.db.Stats().InUse < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(25 * time.Millisecond)
	if _, err := lockConn.ExecContext(ctx, `UPDATE probe_targets SET type = 'ping', port = NULL WHERE id = 'google-dns'`); err != nil {
		t.Fatalf("commit concurrent type change: %v", err)
	}
	if _, err := lockConn.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatalf("commit writer transaction: %v", err)
	}

	if err := <-result; !errors.Is(err, errInvalidAdminTargetWrite) {
		t.Fatalf("stale port patch error = %v, want invalid target rejection", err)
	}
	var targetType, address string
	var port sql.NullInt64
	if err := store.db.QueryRowContext(ctx, `SELECT type, address, port FROM probe_targets WHERE id = 'google-dns'`).Scan(&targetType, &address, &port); err != nil {
		t.Fatalf("read final target: %v", err)
	}
	if !validAdminProbeTargetForType(targetType, address, port) {
		t.Fatalf("concurrent patches persisted invalid target type=%s address=%s port=%v", targetType, address, port)
	}
}

func TestRecordAgentHeartbeatRetriesAfterSQLiteBusyTimeout(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	store.db.SetMaxOpenConns(2)
	store.db.SetMaxIdleConns(2)

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	blockingConn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("open blocking connection: %v", err)
	}
	if _, err := blockingConn.ExecContext(ctx, `PRAGMA busy_timeout = 50`); err != nil {
		blockingConn.Close()
		t.Fatalf("set blocking busy_timeout: %v", err)
	}

	heartbeatConn, err := store.db.Conn(ctx)
	if err != nil {
		blockingConn.Close()
		t.Fatalf("open heartbeat connection: %v", err)
	}
	if _, err := heartbeatConn.ExecContext(ctx, `PRAGMA busy_timeout = 50`); err != nil {
		heartbeatConn.Close()
		blockingConn.Close()
		t.Fatalf("set heartbeat busy_timeout: %v", err)
	}
	if err := heartbeatConn.Close(); err != nil {
		blockingConn.Close()
		t.Fatalf("close heartbeat connection: %v", err)
	}

	blockingTx, err := blockingConn.BeginTx(ctx, nil)
	if err != nil {
		blockingConn.Close()
		t.Fatalf("begin blocking transaction: %v", err)
	}
	if _, err := blockingTx.ExecContext(ctx, `UPDATE nodes SET updated_at = updated_at WHERE id = 'example-node-a'`); err != nil {
		_ = blockingTx.Rollback()
		blockingConn.Close()
		t.Fatalf("reserve blocking writer: %v", err)
	}

	release := make(chan struct{})
	released := make(chan error, 1)
	go func() {
		<-release
		err := blockingTx.Commit()
		if closeErr := blockingConn.Close(); err == nil {
			err = closeErr
		}
		released <- err
	}()
	var releaseOnce sync.Once
	releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
	timer := time.AfterFunc(150*time.Millisecond, releaseWriter)
	defer func() {
		if !timer.Stop() {
			releaseWriter()
		}
	}()

	result := make(chan error, 1)
	go func() {
		_, err := store.RecordAgentHeartbeatTransition(ctx, "example-node-a", time.Now().UTC(), "online", "agent-test")
		result <- err
	}()

	select {
	case err := <-result:
		releaseWriter()
		if releaseErr := <-released; releaseErr != nil {
			t.Fatalf("release blocking writer: %v", releaseErr)
		}
		if err != nil {
			t.Fatalf("heartbeat should retry after SQLITE_BUSY timeout: %v", err)
		}
	case <-time.After(2 * time.Second):
		releaseWriter()
		if releaseErr := <-released; releaseErr != nil {
			t.Fatalf("release blocking writer: %v", releaseErr)
		}
		t.Fatal("heartbeat did not resume after blocking writer committed")
	}
}

func TestAgentHighFrequencyWritersSerializeAndRetrySQLiteBusy(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	const pooledConnections = 12
	store.db.SetMaxOpenConns(pooledConnections)
	store.db.SetMaxIdleConns(pooledConnections)
	setSQLiteBusyTimeoutForPool(t, store, pooledConnections, 50)

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	probeConfigVersion, err := store.ProbeConfigVersion(ctx)
	if err != nil {
		t.Fatalf("read probe config version: %v", err)
	}

	blockingConn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("open blocking connection: %v", err)
	}
	blockingTx, err := blockingConn.BeginTx(ctx, nil)
	if err != nil {
		blockingConn.Close()
		t.Fatalf("begin blocking transaction: %v", err)
	}
	if _, err := blockingTx.ExecContext(ctx, `UPDATE nodes SET updated_at = updated_at WHERE id = 'example-node-a'`); err != nil {
		_ = blockingTx.Rollback()
		blockingConn.Close()
		t.Fatalf("reserve blocking writer: %v", err)
	}

	release := make(chan struct{})
	released := make(chan error, 1)
	go func() {
		<-release
		err := blockingTx.Commit()
		if closeErr := blockingConn.Close(); err == nil {
			err = closeErr
		}
		released <- err
	}()
	var releaseOnce sync.Once
	releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
	timer := time.AfterFunc(150*time.Millisecond, releaseWriter)
	defer func() {
		if !timer.Stop() {
			releaseWriter()
		}
	}()

	baseTS := time.Now().UTC().Unix()
	writers := []struct {
		name string
		run  func(context.Context) error
	}{
		{name: "heartbeat-online", run: func(ctx context.Context) error {
			_, err := store.RecordAgentHeartbeatTransition(ctx, "example-node-a", time.Unix(baseTS+1, 0).UTC(), "online", "agent-test")
			return err
		}},
		{name: "heartbeat-warning", run: func(ctx context.Context) error {
			_, err := store.RecordAgentHeartbeatTransition(ctx, "example-node-a", time.Unix(baseTS+2, 0).UTC(), "warning", "agent-test")
			return err
		}},
		{name: "host", run: func(ctx context.Context) error {
			return store.UpsertAgentHost(ctx, "example-node-a", AgentHostRequest{Hostname: "example-node-a", OSName: "Linux", Arch: "amd64", AgentVersion: "agent-test", CPUCores: 2, MemoryTotalBytes: 1024, DiskTotalBytes: 4096})
		}},
		{name: "state-report", run: func(ctx context.Context) error {
			_, _, err := store.RecordAgentStateReport(ctx, "example-node-a", sqliteConcurrencyAgentState(baseTS+3, "busy-state-report"))
			return err
		}},
		{name: "state-insert", run: func(ctx context.Context) error {
			return store.InsertAgentState(ctx, "example-node-a", sqliteConcurrencyAgentState(baseTS+4, "busy-state-insert"))
		}},
		{name: "state-alert", run: func(ctx context.Context) error {
			_, err := store.RecordAgentStateAlertRuleTransition(ctx, "example-node-a", time.Unix(baseTS+5, 0).UTC(), sqliteConcurrencyAgentState(baseTS+5, "busy-state-alert"))
			return err
		}},
		{name: "presence-online", run: func(ctx context.Context) error {
			_, err := store.RecordAgentPresenceOnlineTransition(ctx, "example-node-a", time.Unix(baseTS+6, 0).UTC())
			return err
		}},
		{name: "presence-offline", run: func(ctx context.Context) error {
			_, err := store.RecordAgentPresenceOfflineTransition(ctx, "example-node-a", time.Unix(baseTS+7, 0).UTC())
			return err
		}},
		{name: "stale-offline", run: func(ctx context.Context) error {
			_, _, err := store.RecordStaleAgentOfflineTransition(ctx, "example-node-a", time.Unix(baseTS+7200, 0).UTC())
			return err
		}},
		{name: "presence-config-applied", run: func(ctx context.Context) error {
			return store.RecordProbeConfigApplied(ctx, "example-node-a", probeConfigVersion, time.Unix(baseTS+8, 0).UTC())
		}},
	}

	start := make(chan struct{})
	errs := make(chan error, len(writers))
	var wg sync.WaitGroup
	for _, writer := range writers {
		writer := writer
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := writer.run(writeCtx); err != nil {
				errs <- fmt.Errorf("%s: %w", writer.name, err)
			}
		}()
	}
	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		releaseWriter()
		if releaseErr := <-released; releaseErr != nil {
			t.Fatalf("release blocking writer: %v", releaseErr)
		}
	case <-time.After(6 * time.Second):
		releaseWriter()
		if releaseErr := <-released; releaseErr != nil {
			t.Fatalf("release blocking writer: %v", releaseErr)
		}
		t.Fatal("agent writers did not drain after blocking writer committed")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("agent writer returned error: %v", err)
		}
	}

	var samples int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_samples WHERE node_id = 'example-node-a'`).Scan(&samples); err != nil {
		t.Fatalf("count state samples: %v", err)
	}
	if samples == 0 {
		t.Fatal("agent state writers did not persist any samples")
	}
}

func TestAgentSQLiteBusyRetryBudgetStaysBelowAgentClientTimeout(t *testing.T) {
	const agentClientTimeout = 30 * time.Second
	const configuredSQLiteBusyTimeout = 1 * time.Second
	if sqliteAgentWriteTimeout >= agentClientTimeout {
		t.Fatalf("agent write timeout %s must stay below agent client timeout %s", sqliteAgentWriteTimeout, agentClientTimeout)
	}
	if sqliteBusyRetryFor+configuredSQLiteBusyTimeout >= agentClientTimeout {
		t.Fatalf("busy retry budget %s plus SQLite busy_timeout %s must stay below agent client timeout %s", sqliteBusyRetryFor, configuredSQLiteBusyTimeout, agentClientTimeout)
	}
	if sqliteBusyRetryFor >= sqliteAgentWriteTimeout {
		t.Fatalf("busy retry budget %s must be lower than agent write timeout %s", sqliteBusyRetryFor, sqliteAgentWriteTimeout)
	}
}

func setSQLiteBusyTimeoutForPool(t *testing.T, store *SQLiteStore, connections int, timeoutMS int) {
	t.Helper()
	ctx := context.Background()
	conns := make([]*sql.Conn, 0, connections)
	defer func() {
		for _, conn := range conns {
			if err := conn.Close(); err != nil {
				t.Fatalf("close primed sqlite connection: %v", err)
			}
		}
	}()
	for index := 0; index < connections; index++ {
		conn, err := store.db.Conn(ctx)
		if err != nil {
			t.Fatalf("open sqlite connection %d: %v", index, err)
		}
		conns = append(conns, conn)
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", timeoutMS)); err != nil {
			t.Fatalf("set sqlite busy_timeout on connection %d: %v", index, err)
		}
	}
}

func sqliteConcurrencyAgentState(ts int64, sampleID string) AgentStateRequest {
	return AgentStateRequest{
		SampleID:         sampleID,
		TS:               ts,
		CPUPercent:       12.5,
		MemoryUsedBytes:  512,
		MemoryTotalBytes: 2048,
		DiskUsedBytes:    1024,
		DiskTotalBytes:   8192,
		NetInTotalBytes:  ts * 10,
		NetOutTotalBytes: ts * 20,
		NetInSpeedBps:    128,
		NetOutSpeedBps:   256,
		UptimeSeconds:    3600,
	}
}

func TestOpenSQLiteStoreInMemorySharesPoolAndClosesCleanly(t *testing.T) {
	store, err := OpenSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("open in-memory store: %v", err)
	}
	if store.readDB != store.db {
		store.Close()
		t.Fatal("in-memory store must share db and readDB so readers see the same database")
	}
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "mem-node", DisplayName: "Memory Node", AgentToken: "mem-token"}); err != nil {
		store.Close()
		t.Fatalf("seed in-memory store: %v", err)
	}
	summary, err := store.Summary(ctx)
	if err != nil {
		store.Close()
		t.Fatalf("read summary from in-memory store: %v", err)
	}
	if len(summary.Nodes) != 1 || summary.Nodes[0].ID != "mem-node" {
		store.Close()
		t.Fatalf("summary nodes = %+v, want mem-node", summary.Nodes)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close in-memory store: %v", err)
	}
}

func TestWALCheckpointerTruncatesOversizedWALWithoutBlocking(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "zeno.db")
	store, err := OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "wal-node", DisplayName: "WAL Node", AgentToken: "token"}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// Hold a read transaction on readDB while writing so frames accumulate in the -wal file.
	readTx, err := store.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read tx: %v", err)
	}
	var count int
	if err := readTx.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes`).Scan(&count); err != nil {
		_ = readTx.Rollback()
		t.Fatalf("pin read snapshot: %v", err)
	}

	if _, err := store.db.ExecContext(ctx, `
		WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 5000)
		INSERT INTO state_samples (node_id, ts, cpu_percent)
		SELECT 'wal-node', x, 10 FROM c
	`); err != nil {
		_ = readTx.Rollback()
		t.Fatalf("grow wal: %v", err)
	}

	walPath := dbPath + "-wal"
	beforeInfo, err := os.Stat(walPath)
	if err != nil || beforeInfo.Size() == 0 {
		_ = readTx.Rollback()
		t.Fatalf("expected non-empty wal file before checkpoint, size=%v err=%v", beforeInfo, err)
	}

	// Lower threshold for deterministic unit test and verify checkpoint does NOT
	// block even while readTx is still open.
	store.walCheckpoint.mu.Lock()
	store.walCheckpoint.truncateMinBytes = 1024
	store.walCheckpoint.truncateMinInterval = time.Minute
	store.walCheckpoint.mu.Unlock()

	start := time.Now()
	if err := store.checkpointWALOnce(ctx, start); err != nil {
		_ = readTx.Rollback()
		t.Fatalf("checkpoint while reader open: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		_ = readTx.Rollback()
		t.Fatalf("checkpoint blocked for %s while reader open", elapsed)
	}
	_ = readTx.Rollback()

	// Once reader is released and minInterval has elapsed, TRUNCATE shrinks -wal to 0 bytes.
	if err := store.checkpointWALOnce(ctx, start.Add(2*time.Minute)); err != nil {
		t.Fatalf("truncate checkpoint after reader closed: %v", err)
	}
	afterInfo, err := os.Stat(walPath)
	if err != nil {
		t.Fatalf("stat wal after truncate: %v", err)
	}
	if afterInfo.Size() != 0 {
		t.Fatalf("wal size after truncate = %d, want 0", afterInfo.Size())
	}
}
