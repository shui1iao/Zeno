package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shui1iao/zeno/internal/shared/probe"
)

func testF64(v float64) *float64 { return &v }

func TestEmptyPublicSummaryUsesJSONArrays(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open empty sqlite store: %v", err)
	}
	defer store.Close()

	summary, err := store.Summary(context.Background())
	if err != nil {
		t.Fatalf("read empty summary: %v", err)
	}
	if summary.Nodes == nil || summary.Services == nil || summary.LatencyPoints == nil {
		t.Fatalf("empty summary contains nil slices: %#v", summary)
	}
	payload, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal empty summary: %v", err)
	}
	if got := string(payload); got != `{"nodes":[],"services":[],"latency_points":[],"exchange_rates":{"CNY":1}}` {
		t.Fatalf("empty summary JSON = %s", got)
	}
}

func TestProbeTargetsHaveNoGlobalEnabledColumn(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	rows, err := store.db.QueryContext(context.Background(), `PRAGMA table_info(probe_targets)`)
	if err != nil {
		t.Fatalf("read probe target schema: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan probe target schema: %v", err)
		}
		if name == "enabled" {
			t.Fatal("probe_targets still contains the removed global enabled column")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate probe target schema: %v", err)
	}
}

func TestOpenSQLiteStoreRemovesLegacyProbeTargetEnabledColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zeno.db")
	store, err := OpenSQLiteStore(path)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `ALTER TABLE probe_targets ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1`); err != nil {
		t.Fatalf("add legacy enabled column: %v", err)
	}
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE probe_targets SET enabled = 0 WHERE id = 'google-dns'`); err != nil {
		t.Fatalf("set legacy global enabled value: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close legacy sqlite store: %v", err)
	}

	reopened, err := OpenSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen migrated sqlite store: %v", err)
	}
	defer reopened.Close()
	exists, err := reopened.columnExists(ctx, "probe_targets", "enabled")
	if err != nil {
		t.Fatalf("inspect migrated probe target schema: %v", err)
	}
	if exists {
		t.Fatal("legacy probe target global enabled column still exists after migration")
	}
	targets, err := reopened.EnabledProbeTargets(ctx, "example-node-a")
	if err != nil {
		t.Fatalf("read assigned targets: %v", err)
	}
	assigned := false
	for _, target := range targets {
		if target.ID == "google-dns" {
			assigned = true
		}
	}
	if !assigned {
		t.Fatalf("legacy global enabled=0 still suppressed google-dns from Agent config: %+v", targets)
	}
	services, err := reopened.serviceTargets(ctx)
	if err != nil {
		t.Fatalf("read public service targets: %v", err)
	}
	for _, target := range services {
		if target.ID == "google-dns" {
			return
		}
	}
	t.Fatalf("legacy global enabled=0 still suppressed google-dns from public services: %+v", services)
}

func TestOpenSQLiteStoreMigratesLegacyNotificationDeliveryLeaseColumnsBeforeIndex(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "zeno.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite database: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE notification_deliveries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			node_id TEXT NOT NULL DEFAULT '',
			node_name TEXT NOT NULL DEFAULT '',
			previous_status TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			channel_id TEXT NOT NULL,
			channel_name TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0,
			next_attempt_at INTEGER NOT NULL,
			last_error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			delivered_at INTEGER
		)
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create legacy notification deliveries table: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO notification_deliveries (
			event_type, channel_id, state, next_attempt_at, created_at, updated_at
		) VALUES ('node_offline', 'legacy-channel', 'leased', 1, 100, 200)
	`); err != nil {
		_ = db.Close()
		t.Fatalf("insert legacy leased notification delivery: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy sqlite database: %v", err)
	}

	store, err := OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("migrate legacy sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	for _, column := range []string{"lease_until", "claim_token", "request_started_at"} {
		exists, err := store.columnExists(ctx, "notification_deliveries", column)
		if err != nil {
			t.Fatalf("inspect migrated %s column: %v", column, err)
		}
		if !exists {
			t.Fatalf("expected migrated %s column", column)
		}
	}
	var indexCount int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_notification_deliveries_claim'
	`).Scan(&indexCount); err != nil {
		t.Fatalf("inspect notification delivery claim index: %v", err)
	}
	if indexCount != 1 {
		t.Fatalf("expected claim index after migration, got %d", indexCount)
	}
	var state string
	var requestStartedAt int64
	if err := store.db.QueryRowContext(ctx, `SELECT state, request_started_at FROM notification_deliveries WHERE id = 1`).Scan(&state, &requestStartedAt); err != nil {
		t.Fatalf("read legacy notification delivery: %v", err)
	}
	if state != "leased" || requestStartedAt != 0 {
		t.Fatalf("legacy row state=%q request_started_at=%d, want untouched (this release never writes notification_deliveries)", state, requestStartedAt)
	}
}

func TestRecordAgentHeartbeatWaitsForConcurrentNodeWriter(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Unix()
	if _, err := store.db.Exec(`
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, created_at, updated_at, last_seen_at)
		VALUES ('heartbeat-lock-node', 'Heartbeat Lock Node', 'hash', 'online', 'HK', ?, ?, ?)
	`, now, now, now); err != nil {
		t.Fatalf("insert heartbeat lock node: %v", err)
	}

	blockingTx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocking writer: %v", err)
	}
	if _, err := blockingTx.Exec(`UPDATE nodes SET updated_at = updated_at WHERE id = 'heartbeat-lock-node'`); err != nil {
		_ = blockingTx.Rollback()
		t.Fatalf("reserve blocking writer: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := store.RecordAgentHeartbeatTransition(context.Background(), "heartbeat-lock-node", time.Now().UTC(), "online", "v0.2.0")
		result <- err
	}()
	time.Sleep(100 * time.Millisecond)
	if err := blockingTx.Commit(); err != nil {
		t.Fatalf("commit blocking writer: %v", err)
	}

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("heartbeat should wait for concurrent writer: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat did not resume after concurrent writer committed")
	}
}

func TestNotificationAuthorityRequiresMatchingExternalKey(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "zeno.db")
	store, err := OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	ctx := context.Background()
	authorized, err := store.AuthorizeNotificationAuthority(ctx, "primary-secret")
	if err != nil || !authorized {
		t.Fatalf("bind primary notification authority: authorized=%v err=%v", authorized, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close sqlite store: %v", err)
	}

	reopened, err := OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer reopened.Close()
	if authorized, err := reopened.AuthorizeNotificationAuthority(ctx, "primary-secret"); err != nil || !authorized {
		t.Fatalf("reauthorize matching key: authorized=%v err=%v", authorized, err)
	}
	if authorized, err := reopened.AuthorizeNotificationAuthority(ctx, "candidate-secret"); err != nil || authorized {
		t.Fatalf("authorize copied DB with different key: authorized=%v err=%v", authorized, err)
	}
	if authorized, err := reopened.AuthorizeNotificationAuthority(ctx, ""); err != nil || authorized {
		t.Fatalf("authorize copied DB without key: authorized=%v err=%v", authorized, err)
	}
}

func TestSQLiteBackedHandlerReturnsPersistedLatencyInsteadOfMock(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, created_at, updated_at, last_seen_at)
		VALUES ('example-node-a', 'Example Node A', 'hash-for-test', 'online', 'HK', ?, ?, ?);
	`, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO host_info (node_id, os_name, cpu_cores, memory_total_bytes, disk_total_bytes, updated_at)
		VALUES ('example-node-a', 'debian', 2, 4096, 8192, ?);
	`, now.Unix()); err != nil {
		t.Fatalf("insert host info: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_targets (id, name, type, address, count, timeout_ms, interval_sec, created_at, updated_at)
		VALUES ('google', 'Google', 'ping', '8.8.8.8', 3, 1000, 60, ?, ?);
	`, now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO node_probe_targets (node_id, target_id, enabled)
		VALUES ('example-node-a', 'google', 1);
	`); err != nil {
		t.Fatalf("insert node target: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms)
		VALUES ('example-node-a', 'google', ?, 'ping', 3, 3, 0, 1.1, 1.3, 1.2, 1.6, 0.2);
	`, now.Unix()); err != nil {
		t.Fatalf("insert round: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/nodes/example-node-a/latency?range=1h", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response LatencyResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.NodeID != "example-node-a" {
		t.Fatalf("node_id = %q, want example-node-a", response.NodeID)
	}
	if response.Range != "1h" {
		t.Fatalf("range = %q, want 1h", response.Range)
	}
	var point *LatencyPoint
	for index := range response.Points {
		if response.Points[index].TargetID == "google" && response.Points[index].AvgMS != nil {
			point = &response.Points[index]
			break
		}
	}
	if point == nil {
		t.Fatalf("missing persisted google point; points=%+v", response.Points)
	}
	if point.TargetID != "google" || point.TargetName != "Google" {
		t.Fatalf("target = %q/%q, want google/Google", point.TargetID, point.TargetName)
	}
	if point.AvgMS == nil || *point.AvgMS != 1.3 {
		t.Fatalf("avg_ms = %v, want 1.3", point.AvgMS)
	}
	if point.LossPercent != 0 {
		t.Fatalf("loss_percent = %v, want 0", point.LossPercent)
	}
}

func TestProbeRoundIdempotencyMigrationBackfillsLegacyRowsAndReplacesIndex(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "zeno.db")
	store, err := OpenSQLiteStore(databasePath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	targets, err := store.EnabledProbeTargets(ctx, "example-node-a")
	if err != nil || len(targets) == 0 {
		t.Fatalf("list targets: targets=%d err=%v", len(targets), err)
	}
	var googleDNS ProbeTarget
	for _, target := range targets {
		if target.ID == "google-dns" {
			googleDNS = target
			break
		}
	}
	if googleDNS.ID == "" {
		t.Fatalf("google-dns target missing from seeded targets: %+v", targets)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	rawDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer rawDB.Close()
	if _, err := rawDB.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable raw foreign keys: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, `
		DROP INDEX IF EXISTS idx_probe_rounds_idempotency;
		DROP INDEX IF EXISTS idx_probe_rounds_node_target_ts;
		DROP INDEX IF EXISTS idx_probe_rounds_node_ts;
		DROP INDEX IF EXISTS idx_probe_rounds_target_ts;
		DROP INDEX IF EXISTS idx_probe_rounds_ts_target_node;
		DROP TABLE probe_samples;
		DROP TABLE probe_rounds;
		CREATE TABLE probe_rounds (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL REFERENCES nodes(id),
			target_id TEXT NOT NULL REFERENCES probe_targets(id),
			ts INTEGER NOT NULL,
			type TEXT NOT NULL,
			sent INTEGER NOT NULL,
			received INTEGER NOT NULL,
			loss_percent REAL NOT NULL,
			min_ms REAL,
			avg_ms REAL,
			median_ms REAL,
			max_ms REAL,
			stddev_ms REAL,
			error TEXT
		);
		CREATE INDEX idx_probe_rounds_node_target_ts ON probe_rounds(node_id, target_id, ts);
		CREATE INDEX idx_probe_rounds_node_ts ON probe_rounds(node_id, ts);
		CREATE INDEX idx_probe_rounds_target_ts ON probe_rounds(target_id, ts);
		CREATE TABLE probe_samples (
			round_id INTEGER NOT NULL REFERENCES probe_rounds(id) ON DELETE CASCADE,
			seq INTEGER NOT NULL,
			success INTEGER NOT NULL,
			latency_ms REAL,
			error TEXT,
			PRIMARY KEY (round_id, seq)
		);
	`); err != nil {
		t.Fatalf("create true legacy probe schema: %v", err)
	}
	probeTS := time.Now().UTC().Truncate(time.Second)
	legacyRounds := []struct {
		latency float64
		error   string
	}{
		{latency: 12.5},
		{latency: 12.5},
		{latency: 13.5},
	}
	for index, legacyRound := range legacyRounds {
		result, err := rawDB.ExecContext(ctx, `
			INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms, error)
			VALUES ('example-node-a', 'google-dns', ?, 'tcping', 1, 1, 0, ?, ?, ?, ?, 0, NULL)
		`, probeTS.Unix(), legacyRound.latency, legacyRound.latency, legacyRound.latency, legacyRound.latency)
		if err != nil {
			t.Fatalf("insert legacy round %d: %v", index+1, err)
		}
		roundID, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("legacy round id %d: %v", index+1, err)
		}
		if _, err := rawDB.ExecContext(ctx, `INSERT INTO probe_samples (round_id, seq, success, latency_ms, error) VALUES (?, 1, 1, ?, NULL)`, roundID, legacyRound.latency); err != nil {
			t.Fatalf("insert legacy sample %d: %v", index+1, err)
		}
	}
	var beforeRounds, beforeSamples int
	if err := rawDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&beforeRounds); err != nil {
		t.Fatalf("count legacy rounds before migration: %v", err)
	}
	if err := rawDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples`).Scan(&beforeSamples); err != nil {
		t.Fatalf("count legacy samples before migration: %v", err)
	}
	if beforeRounds != len(legacyRounds) || beforeSamples != len(legacyRounds) {
		t.Fatalf("legacy fixture rows rounds/samples=%d/%d, want %d/%d", beforeRounds, beforeSamples, len(legacyRounds), len(legacyRounds))
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw legacy sqlite: %v", err)
	}

	reopened, err := OpenSQLiteStore(databasePath)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	defer reopened.Close()
	var afterRounds, afterSamples, distinctKeys, legacyKeyRows, payloadHashRows int
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&afterRounds); err != nil {
		t.Fatalf("count migrated rounds: %v", err)
	}
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples`).Scan(&afterSamples); err != nil {
		t.Fatalf("count migrated samples: %v", err)
	}
	if afterRounds != beforeRounds || afterSamples != beforeSamples {
		t.Fatalf("migrated rows rounds/samples=%d/%d, want preserved %d/%d", afterRounds, afterSamples, beforeRounds, beforeSamples)
	}
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT idempotency_key) FROM probe_rounds`).Scan(&distinctKeys); err != nil {
		t.Fatalf("count migrated distinct keys: %v", err)
	}
	if distinctKeys != beforeRounds {
		t.Fatalf("migrated distinct idempotency keys=%d, want one per legacy row %d", distinctKeys, beforeRounds)
	}
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds WHERE idempotency_key GLOB 'legacy:[0-9]*:*'`).Scan(&legacyKeyRows); err != nil {
		t.Fatalf("count migrated legacy keys: %v", err)
	}
	if legacyKeyRows != beforeRounds {
		t.Fatalf("migrated legacy key rows=%d, want %d", legacyKeyRows, beforeRounds)
	}
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds WHERE payload_hash <> ''`).Scan(&payloadHashRows); err != nil {
		t.Fatalf("count migrated payload hashes: %v", err)
	}
	if payloadHashRows != beforeRounds {
		t.Fatalf("migrated payload hash rows=%d, want %d", payloadHashRows, beforeRounds)
	}
	columns, err := sqliteIndexColumns(ctx, reopened.db, "idx_probe_rounds_idempotency")
	if err != nil {
		t.Fatalf("read migrated index: %v", err)
	}
	if !stringSlicesEqual(columns, []string{"node_id", "target_id", "ts", "type", "idempotency_key"}) {
		t.Fatalf("migrated index columns = %v", columns)
	}
	agentColumns, err := sqliteIndexColumns(ctx, reopened.db, "idx_probe_rounds_agent_id")
	if err != nil {
		t.Fatalf("read agent round index: %v", err)
	}
	if !stringSlicesEqual(agentColumns, []string{"node_id", "agent_round_id"}) {
		t.Fatalf("agent round index columns = %v", agentColumns)
	}
	samples := []probe.Sample{{Seq: 1, Success: true, LatencyMS: testF64(12.5)}}
	if err := reopened.InsertProbeRound(ctx, "example-node-a", googleDNS, probeTS, samples); err != nil {
		t.Fatalf("retry migrated probe round: %v", err)
	}
	var rounds int
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&rounds); err != nil {
		t.Fatalf("count migrated rounds: %v", err)
	}
	if rounds != beforeRounds {
		t.Fatalf("retry after migration stored %d rounds, want still-preserved %d", rounds, beforeRounds)
	}
}

func TestAgentProbeRoundLookupUsesPartialUniqueIndex(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	rows, err := store.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+agentProbeRoundLookupSQL, "node-a", "round-a")
	if err != nil {
		t.Fatalf("explain agent round lookup: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read query plan: %v", err)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "idx_probe_rounds_agent_id") {
		t.Fatalf("agent round lookup plan does not use partial unique index:\n%s", joined)
	}
}

func TestLocalProbeInsertRetriesWholeTransactionAfterConcurrentWriter(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	targets, err := store.EnabledProbeTargets(ctx, "example-node-a")
	if err != nil || len(targets) == 0 {
		t.Fatalf("load target: targets=%d err=%v", len(targets), err)
	}

	writer, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin competing writer: %v", err)
	}
	if _, err := writer.ExecContext(ctx, `UPDATE nodes SET updated_at = updated_at WHERE id = 'example-node-a'`); err != nil {
		t.Fatalf("hold competing write lock: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- store.InsertProbeRound(ctx, "example-node-a", targets[0], time.Now().UTC(), []probe.Sample{{Seq: 1, Success: true, LatencyMS: testF64(12)}})
	}()
	select {
	case err := <-done:
		t.Fatalf("local probe insert returned before competing writer released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := writer.Commit(); err != nil {
		t.Fatalf("commit competing writer: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("local probe insert after writer release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("local probe insert did not recover after competing writer released")
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds WHERE node_id = 'example-node-a'`).Scan(&count); err != nil {
		t.Fatalf("count local probe rounds: %v", err)
	}
	if count != 1 {
		t.Fatalf("local probe rounds = %d, want 1", count)
	}
}

func TestStateSampleIdempotencyKeepsPartialIndex(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	var partial int
	var sql string
	if err := store.db.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FILTER (WHERE name = 'idx_state_samples_node_sample_id'),
		       COALESCE(MAX(sql), '')
		FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_state_samples_node_sample_id'
	`).Scan(&partial, &sql); err != nil {
		t.Fatalf("inspect state sample indexes: %v", err)
	}
	if partial != 1 || !strings.Contains(sql, "WHERE sample_id IS NOT NULL AND sample_id <> ''") {
		t.Fatalf("state sample idempotency index = count %d sql %q, want partial unique index", partial, sql)
	}
}

func TestAgentStateSampleLookupUsesPartialUniqueIndex(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	rows, err := store.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+agentStateSampleLookupSQL, "node-a", "sample-a")
	if err != nil {
		t.Fatalf("explain agent state sample lookup: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read query plan: %v", err)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "idx_state_samples_node_sample_id") {
		t.Fatalf("agent state sample lookup plan does not use partial unique index:\n%s", joined)
	}
}

func TestSQLiteBackedLatencyUsesKulinMinuteGridAndAverageDelay(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, created_at, updated_at, last_seen_at)
		VALUES ('example-node-a', 'Example Node A', 'hash-for-test', 'online', 'HK', ?, ?, ?);
	`, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_targets (id, name, type, address, count, timeout_ms, interval_sec, created_at, updated_at)
		VALUES ('google', 'Google', 'ping', '8.8.8.8', 3, 1000, 60, ?, ?);
	`, now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO node_probe_targets (node_id, target_id, enabled)
		VALUES ('example-node-a', 'google', 1);
	`); err != nil {
		t.Fatalf("insert node target: %v", err)
	}
	for _, row := range []struct {
		offsetSec int
		median    float64
		avg       float64
		loss      float64
	}{
		{offsetSec: 10, median: 10, avg: 12, loss: 0},
		{offsetSec: 40, median: 20, avg: 30, loss: 2},
	} {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms)
			VALUES ('example-node-a', 'google', ?, 'ping', 3, 3, ?, 1, ?, ?, 40, 0.2);
		`, now.Add(time.Duration(row.offsetSec)*time.Second).Unix(), row.loss, row.avg, row.median); err != nil {
			t.Fatalf("insert round: %v", err)
		}
	}

	response, err := store.NodeLatency(ctx, "example-node-a", latencyWindow{Name: "1d", Samples: 48, Step: 30 * time.Minute})
	if err != nil {
		t.Fatalf("node latency: %v", err)
	}
	if got := len(uniquePointTimes(response.Points)); got != 1440 {
		t.Fatalf("unique grid timestamps = %d, want 1440", got)
	}
	if len(response.Points) != 1440 {
		t.Fatalf("points len = %d, want 1440 for one target", len(response.Points))
	}
	var bucketPoint *LatencyPoint
	wantTS := now.Format(time.RFC3339)
	for index := range response.Points {
		if response.Points[index].TS == wantTS {
			bucketPoint = &response.Points[index]
			break
		}
	}
	if bucketPoint == nil {
		t.Fatalf("missing minute bucket %s", wantTS)
	}
	if bucketPoint.AvgMS == nil || *bucketPoint.AvgMS != 21 {
		t.Fatalf("avg_ms = %v, want average delay 21", bucketPoint.AvgMS)
	}
	if bucketPoint.MedianMS == nil || *bucketPoint.MedianMS != 15 {
		t.Fatalf("median_ms = %v, want bucket median average 15", bucketPoint.MedianMS)
	}
	if bucketPoint.LossPercent != 1 {
		t.Fatalf("loss_percent = %v, want 1", bucketPoint.LossPercent)
	}
	for _, point := range response.Points {
		if point.TS != wantTS && point.AvgMS != nil {
			t.Fatalf("unexpected non-empty grid point outside source minute: %+v", point)
		}
	}
}

func TestSQLiteBackedSummaryUsesPersistedNodeAndLatestLatency(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, created_at, updated_at, last_seen_at)
		VALUES ('example-node-a', 'Example Node A', 'hash-for-test', 'online', 'HK', ?, ?, ?);
	`, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO host_info (node_id, os_name, arch, cpu_cores, memory_total_bytes, disk_total_bytes, updated_at)
		VALUES ('example-node-a', 'debian', 'aarch64', 2, 4096, 8192, ?);
	`, now.Unix()); err != nil {
		t.Fatalf("insert host info: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO state_samples (node_id, ts, cpu_percent, load1, load5, load15, memory_used_bytes, memory_total_bytes, disk_used_bytes, disk_total_bytes, net_in_total_bytes, net_out_total_bytes, net_in_speed_bps, net_out_speed_bps, uptime_seconds)
		VALUES ('example-node-a', ?, 18.5, 0.42, 0.35, 0.28, 1024, 4096, 2048, 8192, 1000, 2000, 128, 256, 3660);
	`, now.Unix()); err != nil {
		t.Fatalf("insert state sample: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_targets (id, name, type, address, count, timeout_ms, interval_sec, created_at, updated_at)
		VALUES ('google', 'Google', 'ping', '8.8.8.8', 3, 1000, 60, ?, ?);
	`, now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO node_probe_targets (node_id, target_id, enabled)
		VALUES ('example-node-a', 'google', 1);
	`); err != nil {
		t.Fatalf("insert node target: %v", err)
	}
	for offset, median := range []float64{9.9, 8.8} {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms)
			VALUES ('example-node-a', 'google', ?, 'ping', 3, 3, ?, 1.1, ?, ?, 9.9, 0.2);
		`, now.Add(time.Duration(offset-1)*time.Minute).Unix(), float64(offset), median, median); err != nil {
			t.Fatalf("insert round: %v", err)
		}
	}

	handler := NewHandler(HandlerOptions{Store: store})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/summary", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	raw := recorder.Body.String()
	var summary SummaryResponse
	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	var publicShape struct {
		Nodes []struct {
			Arch string `json:"arch"`
		} `json:"nodes"`
	}
	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&publicShape); err != nil {
		t.Fatalf("decode public shape: %v", err)
	}
	if len(summary.Nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(summary.Nodes))
	}
	node := summary.Nodes[0]
	if node.DisplayName != "Example Node A" || node.OS != "debian" || node.CountryCode != "HK" {
		t.Fatalf("node = %+v, want persisted Example Node A card", node)
	}
	if publicShape.Nodes[0].Arch != "aarch64" {
		t.Fatalf("node arch = %q, want persisted host architecture", publicShape.Nodes[0].Arch)
	}
	if node.LatencySummary != nil {
		t.Fatalf("latency summary = %+v, want no homepage latency summary without configured home target", node.LatencySummary)
	}
	if len(node.LatencySummaries) != 1 || node.LatencySummaries[0].TargetID != "google" || node.LatencySummaries[0].MedianMS == nil || *node.LatencySummaries[0].MedianMS != 8.8 {
		t.Fatalf("latency summaries = %+v, want latest google median 8.8", node.LatencySummaries)
	}
	if node.Load1 == nil || *node.Load1 != 0.42 || node.Load5 == nil || *node.Load5 != 0.35 || node.Load15 == nil || *node.Load15 != 0.28 || node.UptimeSeconds == nil || *node.UptimeSeconds != 3660 {
		t.Fatalf("summary node live facts = load %v/%v/%v uptime %v, want latest state", node.Load1, node.Load5, node.Load15, node.UptimeSeconds)
	}
	if len(summary.LatencyPoints) != 0 {
		t.Fatalf("summary latency points len = %d, want 0 because latency charts use dedicated websocket feeds", len(summary.LatencyPoints))
	}
	if len(summary.Services) != 1 || summary.Services[0].ID != "google" || summary.Services[0].ReportingNodeCount != 1 || summary.Services[0].AvgMS == nil || *summary.Services[0].AvgMS != 8.8 {
		t.Fatalf("summary services = %+v, want persisted google service status", summary.Services)
	}

	serviceRecorder := httptest.NewRecorder()
	handler.ServeHTTP(serviceRecorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/services/google/latency?range=1h", nil))
	if serviceRecorder.Code != http.StatusOK {
		t.Fatalf("service status = %d, want 200; body=%s", serviceRecorder.Code, serviceRecorder.Body.String())
	}
	var serviceResponse ServiceTargetLatencyResponse
	if err := json.NewDecoder(serviceRecorder.Body).Decode(&serviceResponse); err != nil {
		t.Fatalf("decode service response: %v", err)
	}
	var servicePoint *ServiceLatencyPoint
	for index := range serviceResponse.Points {
		if serviceResponse.Points[index].NodeID == "example-node-a" && serviceResponse.Points[index].AvgMS != nil {
			servicePoint = &serviceResponse.Points[index]
			break
		}
	}
	if serviceResponse.Target.ID != "google" || servicePoint == nil {
		t.Fatalf("service response = %+v, want google points by node", serviceResponse)
	}
}

func TestSQLiteBackedSummaryUsesConfiguredHomeLatencyTarget(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, home_probe_target_id, created_at, updated_at, last_seen_at)
		VALUES ('example-node-a', 'Example Node A', 'hash-for-test', 'online', 'HK', 'cloudflare', ?, ?, ?);
	`, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	for _, target := range []struct {
		id   string
		name string
		addr string
	}{
		{id: "google", name: "Google", addr: "8.8.8.8"},
		{id: "cloudflare", name: "Cloudflare", addr: "1.1.1.1"},
	} {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO probe_targets (id, name, type, address, count, timeout_ms, interval_sec, created_at, updated_at)
			VALUES (?, ?, 'ping', ?, 3, 1000, 60, ?, ?);
		`, target.id, target.name, target.addr, now.Unix(), now.Unix()); err != nil {
			t.Fatalf("insert target %s: %v", target.id, err)
		}
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO node_probe_targets (node_id, target_id, enabled)
			VALUES ('example-node-a', ?, 1);
		`, target.id); err != nil {
			t.Fatalf("insert node target %s: %v", target.id, err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms)
		VALUES ('example-node-a', 'cloudflare', ?, 'ping', 3, 3, 0, 1.0, 1.1, 1.2, 1.3, 0.1);
	`, now.Add(-time.Minute).Unix()); err != nil {
		t.Fatalf("insert cloudflare round: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms)
		VALUES ('example-node-a', 'cloudflare', ?, 'ping', 3, 2, 33.3333333333333, 1.4, 1.5, 1.6, 1.7, 0.1);
	`, now.Add(-2*time.Minute).Unix()); err != nil {
		t.Fatalf("insert lossy cloudflare round: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms)
		VALUES ('example-node-a', 'google', ?, 'ping', 3, 3, 0, 8.0, 8.5, 8.8, 9.0, 0.2);
	`, now.Unix()); err != nil {
		t.Fatalf("insert google round: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, home_probe_target_id, created_at, updated_at, last_seen_at)
		VALUES ('loss-only', 'Loss Only', 'loss-only-hash', 'online', 'HK', 'cloudflare', ?, ?, ?);
		INSERT INTO node_probe_targets (node_id, target_id, enabled)
		VALUES ('loss-only', 'cloudflare', 1);
	`, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert loss-only home node: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms)
		VALUES ('loss-only', 'cloudflare', ?, 'ping', 3, 3, 0, 123, 123, 123, 123, 0);
	`, now.Add(-25*time.Hour).Unix()); err != nil {
		t.Fatalf("insert old loss-only home round: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms)
		VALUES ('loss-only', 'cloudflare', ?, 'ping', 3, 0, 100, NULL, NULL, NULL, NULL, NULL);
	`, now.Add(-time.Minute).Unix()); err != nil {
		t.Fatalf("insert recent loss-only home round: %v", err)
	}

	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 2 {
		t.Fatalf("summary nodes = %+v, want two nodes", summary.Nodes)
	}
	var configured, lossOnly *LatencySummary
	for _, node := range summary.Nodes {
		switch node.ID {
		case "example-node-a":
			configured = node.LatencySummary
		case "loss-only":
			lossOnly = node.LatencySummary
		}
	}
	if configured == nil || configured.TargetID != "cloudflare" || configured.TargetName != "Cloudflare" || configured.MedianMS == nil || *configured.MedianMS != 1.2 {
		t.Fatalf("latency summary = %+v, want configured home target cloudflare", configured)
	}
	if configured.LossPercent == nil || *configured.LossPercent <= 16.6 || *configured.LossPercent >= 16.7 {
		t.Fatalf("latency summary loss = %+v, want 1d average loss around 16.67", configured.LossPercent)
	}
	if lossOnly == nil || lossOnly.MedianMS != nil || lossOnly.AvgMS != nil || lossOnly.LossPercent == nil || *lossOnly.LossPercent != 100 {
		t.Fatalf("loss-only summary = %+v, want no stale pre-24h latency value and 100%% loss", lossOnly)
	}
}

func TestSQLiteBackedHandlerReturnsPersistedStateHistory(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	bucket := time.Unix((now.Unix()/180)*180, 0).UTC()
	for offset, cpu := range []float64{12.5, 18.75} {
		if err := store.InsertAgentState(ctx, "example-node-a", AgentStateRequest{
			TS:               bucket.Add(time.Duration(offset-1) * 3 * time.Minute).Unix(),
			CPUPercent:       cpu,
			MemoryUsedBytes:  int64(3+offset) * 1024,
			MemoryTotalBytes: 8 * 1024,
			DiskUsedBytes:    int64(40+offset) * 1024,
			DiskTotalBytes:   160 * 1024,
			NetInTotalBytes:  int64(1_000_000 + offset*1000),
			NetOutTotalBytes: int64(2_000_000 + offset*2000),
			NetInSpeedBps:    2048.5 + float64(offset),
			NetOutSpeedBps:   1024.25 + float64(offset),
			UptimeSeconds:    int64(3600 + offset),
		}); err != nil {
			t.Fatalf("insert agent state %d: %v", offset, err)
		}
	}

	recorder := httptest.NewRecorder()
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/nodes/example-node-a/state?range=1h", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	bodyLower := strings.ToLower(recorder.Body.String())
	if strings.Contains(bodyLower, "token") || strings.Contains(bodyLower, "secret") {
		t.Fatalf("public state response leaked sensitive wording: %s", recorder.Body.String())
	}

	var response StateResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode state response: %v", err)
	}
	if response.NodeID != "example-node-a" || response.Range != "1h" {
		t.Fatalf("state response identity = %q/%q, want example-node-a/1h", response.NodeID, response.Range)
	}
	if len(response.Points) != 2 {
		t.Fatalf("state points len = %d, want 2", len(response.Points))
	}
	latest := response.Points[1]
	if latest.CPUPercent == nil || *latest.CPUPercent != 18.75 || latest.MemoryUsedBytes == nil || *latest.MemoryUsedBytes != 4*1024 || latest.NetOutSpeedBps == nil || *latest.NetOutSpeedBps != 1025.25 || latest.UptimeSeconds == nil || *latest.UptimeSeconds != 3601 {
		t.Fatalf("latest state point = %+v, want persisted agent values", latest)
	}
}

func TestSQLiteBackedSummaryMarksStaleAgentOffline(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	staleSeenAt := now.Add(-10 * time.Minute).Unix()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, created_at, updated_at, last_seen_at)
		VALUES ('example-node-a', 'Example Node A', 'hash-for-test', 'online', 'HK', ?, ?, ?);
	`, now.Unix(), now.Unix(), staleSeenAt); err != nil {
		t.Fatalf("insert stale node: %v", err)
	}

	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(summary.Nodes))
	}
	if summary.Nodes[0].Status != "offline" {
		t.Fatalf("stale node status = %q, want offline", summary.Nodes[0].Status)
	}
}

func TestSQLiteBackedSummaryUsesOfflineNotificationDuration(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `UPDATE alert_rules SET duration_sec = 60 WHERE id = 'node_offline'`); err != nil {
		t.Fatalf("set offline duration: %v", err)
	}
	seen45 := now.Add(-45 * time.Second).Unix()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, created_at, updated_at, last_seen_at)
		VALUES ('example-node-a', 'Example Node A', 'hash-for-test', 'online', 'HK', ?, ?, ?);
	`, now.Unix(), now.Unix(), seen45); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 1 || summary.Nodes[0].Status != "online" {
		t.Fatalf("summary nodes = %+v, want node online before configured 60s offline duration", summary.Nodes)
	}
	seen65 := now.Add(-65 * time.Second).Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET last_seen_at = ? WHERE id = 'example-node-a'`, seen65); err != nil {
		t.Fatalf("age node heartbeat: %v", err)
	}
	summary, err = store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary after stale: %v", err)
	}
	if len(summary.Nodes) != 1 || summary.Nodes[0].Status != "offline" {
		t.Fatalf("summary nodes = %+v, want node offline after configured 60s offline duration", summary.Nodes)
	}
}

func TestSeedPreviewDataDoesNotFakeAgentOnlineStatus(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(summary.Nodes))
	}
	if summary.Nodes[0].Status != "no_data" {
		t.Fatalf("seed-only node status = %q, want no_data until an agent reports", summary.Nodes[0].Status)
	}

	if err := store.RecordAgentHeartbeat(ctx, "example-node-a", time.Now().UTC(), "online", "test-agent"); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK"}); err != nil {
		t.Fatalf("seed preview data after heartbeat: %v", err)
	}
	summary, err = store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary after heartbeat: %v", err)
	}
	if summary.Nodes[0].Status != "online" {
		t.Fatalf("node status after heartbeat and reseed = %q, want online", summary.Nodes[0].Status)
	}
}

func TestSeedPreviewDataIsIdempotentAndWiresExampleNodeATargets(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK"}); err != nil {
			t.Fatalf("seed preview data run %d: %v", i+1, err)
		}
	}

	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1 seeded example-node-a node", len(summary.Nodes))
	}
	if summary.Nodes[0].ID != "example-node-a" || summary.Nodes[0].DisplayName != "Example Node A" || summary.Nodes[0].CountryCode != "HK" {
		t.Fatalf("seeded node = %+v, want example-node-a/HK", summary.Nodes[0])
	}

	targets, err := store.EnabledProbeTargets(ctx, "example-node-a")
	if err != nil {
		t.Fatalf("enabled probe targets: %v", err)
	}
	if len(targets) != len(DefaultPreviewProbeTargets()) || len(targets) < 3 {
		t.Fatalf("targets len = %d, want complete safe preview target set", len(targets))
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if seen[target.ID] {
			t.Fatalf("duplicate target id after idempotent seed: %s", target.ID)
		}
		seen[target.ID] = true
		if target.Type != "tcping" {
			t.Fatalf("target %s type = %q, want tcping for controller-local preview collector", target.ID, target.Type)
		}
		if target.Port == nil || *target.Port <= 0 {
			t.Fatalf("target %s port = %v, want tcp port", target.ID, target.Port)
		}
		if target.Count <= 0 || target.TimeoutMS <= 0 || target.IntervalSec <= 0 {
			t.Fatalf("target %s has invalid probe settings: %+v", target.ID, target)
		}
	}
	for _, id := range []string{"example-node-a-local", "google-dns", "cloudflare-dns", "telegram-dc5", "example-edge-a", "example-edge-b", "example-edge-c"} {
		if !seen[id] {
			t.Fatalf("missing seeded preview target %q; seen=%v", id, seen)
		}
	}
}

func TestEnabledProbeTargetsBoundsLegacyUnsafeConfigForDownlinkAndCollector(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	now := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_targets (id, name, type, address, port, count, timeout_ms, interval_sec, display_order, created_at, updated_at)
		VALUES ('unsafe-legacy', 'Unsafe Legacy', 'tcping', '203.0.113.1', 443, 100, 50000, 1, 5, ?, ?)
	`, now, now); err != nil {
		t.Fatalf("insert unsafe legacy target: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO node_probe_targets (node_id, target_id, enabled) VALUES ('example-node-a', 'unsafe-legacy', 1)`); err != nil {
		t.Fatalf("assign unsafe legacy target: %v", err)
	}
	for index := 0; index < 40; index++ {
		targetID := fmt.Sprintf("bulk-legacy-%02d", index)
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO probe_targets (id, name, type, address, port, count, timeout_ms, interval_sec, display_order, created_at, updated_at)
			VALUES (?, ?, 'tcping', '203.0.113.2', 443, 1, 100, 5, ?, ?, ?)
		`, targetID, targetID, 100+index, now, now); err != nil {
			t.Fatalf("insert bulk legacy target %d: %v", index, err)
		}
		if _, err := store.db.ExecContext(ctx, `INSERT INTO node_probe_targets (node_id, target_id, enabled) VALUES ('example-node-a', ?, 1)`, targetID); err != nil {
			t.Fatalf("assign bulk legacy target %d: %v", index, err)
		}
	}

	targets, err := store.EnabledProbeTargets(ctx, "example-node-a")
	if err != nil {
		t.Fatalf("enabled probe targets: %v", err)
	}
	if len(targets) != maxProbeTargetsPerNode {
		t.Fatalf("enabled targets len=%d, want capped %d", len(targets), maxProbeTargetsPerNode)
	}
	var totalBudgetMS int64
	seenUnsafe := false
	for _, target := range targets {
		if !validProbeTargetResourceConfig(target.Count, target.TimeoutMS, target.IntervalSec) {
			t.Fatalf("target %s delivered unsafe config: %+v", target.ID, target)
		}
		totalBudgetMS += probeTargetRoundBudgetMS(target.Count, target.TimeoutMS)
		if target.ID == "unsafe-legacy" {
			seenUnsafe = true
			if target.Count != 12 || target.TimeoutMS != maxProbeTargetTimeoutMS || target.IntervalSec != 60 {
				t.Fatalf("unsafe legacy target sanitized to %+v, want count=12 timeout=5000 interval=60", target)
			}
		}
	}
	if !seenUnsafe {
		t.Fatal("sanitized unsafe legacy target was not included in ordered downlink")
	}
	if totalBudgetMS > maxProbeNodeRoundBudgetMS {
		t.Fatalf("downlink round budget=%dms, want <=%dms", totalBudgetMS, maxProbeNodeRoundBudgetMS)
	}

	collectedTargets := map[string]ProbeTarget{}
	collector := NewLocalProbeCollector(store, LocalProbeCollectorOptions{
		NodeID: "example-node-a",
		Now:    func() time.Time { return time.Now().UTC().Truncate(time.Second) },
		ProbeRunner: func(ctx context.Context, target ProbeTarget) ([]probe.Sample, error) {
			collectedTargets[target.ID] = target
			return []probe.Sample{{Seq: 1, Success: true, LatencyMS: testF64(1)}}, nil
		},
	})
	if err := collector.CollectOnce(ctx); err != nil {
		t.Fatalf("collect once with bounded targets: %v", err)
	}
	if len(collectedTargets) != len(targets) {
		t.Fatalf("collector saw %d targets, want bounded downlink count %d", len(collectedTargets), len(targets))
	}
	if collectedTargets["unsafe-legacy"].Count != 12 {
		t.Fatalf("collector received unsanitized unsafe target: %+v", collectedTargets["unsafe-legacy"])
	}
}

func TestLocalProbeCollectorCollectOnceWritesRealRoundShape(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	collector := NewLocalProbeCollector(store, LocalProbeCollectorOptions{
		NodeID: "example-node-a",
		Now: func() time.Time {
			return now
		},
		ProbeRunner: func(ctx context.Context, target ProbeTarget) ([]probe.Sample, error) {
			return []probe.Sample{
				{Seq: 1, Success: true, LatencyMS: testF64(10)},
				{Seq: 2, Success: false, Error: "timeout"},
				{Seq: 3, Success: true, LatencyMS: testF64(20)},
			}, nil
		},
	})
	if err := collector.CollectOnce(ctx); err != nil {
		t.Fatalf("collect once: %v", err)
	}

	targets, err := store.EnabledProbeTargets(ctx, "example-node-a")
	if err != nil {
		t.Fatalf("enabled targets: %v", err)
	}
	latency, err := store.NodeLatency(ctx, "example-node-a", latencyWindow{Name: "1h", Samples: 36, Step: 2 * time.Minute})
	if err != nil {
		t.Fatalf("node latency: %v", err)
	}
	if len(latency.Points) != len(targets) {
		t.Fatalf("latency points len = %d, want one point per target (%d)", len(latency.Points), len(targets))
	}
	for _, point := range latency.Points {
		if point.MedianMS == nil || *point.MedianMS != 15 {
			t.Fatalf("point %s median = %v, want 15", point.TargetID, point.MedianMS)
		}
		if math.Abs(point.LossPercent-100.0/3.0) > 0.000001 {
			t.Fatalf("point %s loss = %v, want 33.333", point.TargetID, point.LossPercent)
		}
	}

	var sampleRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples`).Scan(&sampleRows); err != nil {
		t.Fatalf("count samples: %v", err)
	}
	if sampleRows != len(targets)*3 {
		t.Fatalf("probe sample rows = %d, want %d", sampleRows, len(targets)*3)
	}
}

func TestRunLocalProbeUsesTargetTypeImplementations(t *testing.T) {
	ctx := context.Background()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()
	tcpPort := listener.Addr().(*net.TCPAddr).Port
	tcpSamples, err := RunLocalProbe(ctx, ProbeTarget{ID: "tcp", Type: "tcping", Address: "127.0.0.1", Port: &tcpPort, Count: 1, TimeoutMS: 1000, IntervalSec: 30})
	if err != nil || len(tcpSamples) != 1 || !tcpSamples[0].Success {
		t.Fatalf("tcp samples=%+v err=%v, want successful tcping", tcpSamples, err)
	}

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer httpServer.Close()
	httpSamples, err := RunLocalProbe(ctx, ProbeTarget{ID: "http", Type: "http_get", Address: httpServer.URL, Count: 1, TimeoutMS: 1000, IntervalSec: 30})
	if err != nil || len(httpSamples) != 1 || !httpSamples[0].Success {
		t.Fatalf("http samples=%+v err=%v, want successful http_get", httpSamples, err)
	}

	parsed := parsePingLatencyMS("64 bytes from 127.0.0.1: icmp_seq=1 ttl=64 time=1.23 ms")
	if parsed == nil || *parsed != 1.23 {
		t.Fatalf("parsed ping latency = %v, want 1.23", parsed)
	}
}
