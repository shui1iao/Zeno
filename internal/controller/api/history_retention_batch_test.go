package api

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shui1iao/zeno/internal/controller/history"
)

type scheduledHistoryRetentionStore struct {
	mockStore
	calls atomic.Int64
}

func (store *scheduledHistoryRetentionStore) MaintainHistory(context.Context, time.Time) error {
	store.calls.Add(1)
	return nil
}

func TestHistoryRetentionWaitsForConfiguredIntervalBeforeFirstPrune(t *testing.T) {
	store := &scheduledHistoryRetentionStore{}
	h := &handler{store: store}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.runHistoryRetention(ctx, 50*time.Millisecond)
	}()

	time.Sleep(20 * time.Millisecond)
	if calls := store.calls.Load(); calls != 0 {
		cancel()
		<-done
		t.Fatalf("startup retention calls = %d, want 0 before the first interval", calls)
	}
	deadline := time.Now().Add(time.Second)
	for store.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls := store.calls.Load(); calls != 1 {
		cancel()
		<-done
		t.Fatalf("retention calls after first interval = %d, want 1", calls)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("history retention did not stop after cancellation")
	}
}

func TestHistoryRetentionOffsetsHourlyMaintenanceFromRenewalScan(t *testing.T) {
	if got := historyRetentionFirstDelay(time.Hour); got != time.Hour+historyRetentionScheduleOffset {
		t.Fatalf("hourly first delay = %s, want %s", got, time.Hour+historyRetentionScheduleOffset)
	}
	if got := historyRetentionFirstDelay(50 * time.Millisecond); got != 50*time.Millisecond {
		t.Fatalf("short test first delay = %s, want unchanged", got)
	}
}

func TestHistoryMaintenanceStagesContinueAfterOneStageTimesOut(t *testing.T) {
	var calls []string
	err := runHistoryMaintenanceStages(context.Background(), []historyMaintenanceStage{
		{
			name:    "reclaim existing free pages",
			timeout: time.Second,
			run: func(context.Context) error {
				calls = append(calls, "reclaim-before")
				return nil
			},
		},
		{
			name:    "compact raw history",
			timeout: 10 * time.Millisecond,
			run: func(ctx context.Context) error {
				calls = append(calls, "raw")
				<-ctx.Done()
				return ctx.Err()
			},
		},
		{
			name:    "reclaim newly freed pages",
			timeout: time.Second,
			run: func(context.Context) error {
				calls = append(calls, "reclaim-after")
				return nil
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "compact raw history: context deadline exceeded") {
		t.Fatalf("maintenance error = %v, want named timeout", err)
	}
	if got, want := strings.Join(calls, ","), "reclaim-before,raw,reclaim-after"; got != want {
		t.Fatalf("maintenance calls = %q, want %q", got, want)
	}
}

func TestHistoryRetentionUsesTimeIndexesWhenNothingIsExpired(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	cutoff := time.Now().UTC().Add(-historyRollupRetention).Unix()

	probePlan := explainHistoryQueryPlan(t, store, pruneExpiredProbeRoundsSQL, cutoff)
	if !strings.Contains(probePlan, "idx_probe_rounds_ts_target_node") || strings.Contains(probePlan, "SCAN probe_rounds") {
		t.Fatalf("probe retention plan must seek the time index, got:\n%s", probePlan)
	}
	statePlan := explainHistoryQueryPlan(t, store, pruneExpiredStateSamplesSQL, cutoff)
	if !strings.Contains(statePlan, "idx_state_samples_node_ts") || strings.Contains(statePlan, "SCAN samples") {
		t.Fatalf("state retention plan must seek each node/time range, got:\n%s", statePlan)
	}
	logPlan := explainHistoryQueryPlan(t, store, history.PruneNotificationLogSQL, cutoff)
	if !strings.Contains(logPlan, "idx_notification_log_ts") {
		t.Fatalf("notification log retention plan must seek the time index, got:\n%s", logPlan)
	}
}

func explainHistoryQueryPlan(t *testing.T, store *SQLiteStore, query string, cutoff int64) string {
	t.Helper()
	rows, err := store.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, cutoff, historyRetentionBatchSize)
	if err != nil {
		t.Fatalf("explain history query: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan history plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read history plan: %v", err)
	}
	return strings.Join(plan, "\n")
}

func TestPruneRawHistoryDeletesInBatchesAndKeepsRecentRows(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	now := time.Now().UTC()
	oldTS := now.Add(-historyRollupRetention - time.Hour).Unix()
	recentTS := now.Unix()
	for i := 0; i < historyRetentionBatchSize+17; i++ {
		if _, err := store.db.ExecContext(ctx, `INSERT INTO state_samples (node_id, ts, cpu_percent) VALUES ('example-node-a', ?, 1)`, oldTS-int64(i)); err != nil {
			t.Fatalf("insert old state %d: %v", i, err)
		}
		if _, err := store.db.ExecContext(ctx, `INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent) VALUES ('example-node-a', 'google-dns', ?, 'ping', 1, 1, 0)`, oldTS-int64(i)); err != nil {
			t.Fatalf("insert old probe round %d: %v", i, err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO state_samples (node_id, ts, cpu_percent) VALUES ('example-node-a', ?, 2)`, recentTS); err != nil {
		t.Fatalf("insert recent state: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent) VALUES ('example-node-a', 'google-dns', ?, 'ping', 1, 1, 0)`, recentTS); err != nil {
		t.Fatalf("insert recent probe round: %v", err)
	}
	for i := 0; i < historyRetentionBatchSize+3; i++ {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO notification_log (ts, channel_id, node_id, kind, outcome, message)
			VALUES (?, ?, 'example-node-a', 'node_offline', 'sent', 'old')
		`, oldTS, fmt.Sprintf("channel-%d", i)); err != nil {
			t.Fatalf("insert old notification log %d: %v", i, err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO notification_log (ts, channel_id, node_id, kind, outcome, message)
		VALUES (?, 'recent-channel', 'example-node-a', 'node_offline', 'sent', 'recent')
	`, recentTS); err != nil {
		t.Fatalf("insert recent notification log: %v", err)
	}
	// The legacy outbox table is kept read-only for rollback: this release
	// neither prunes nor expires it.
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO notification_deliveries (event_type, channel_id, state, next_attempt_at, created_at, updated_at)
		VALUES ('node_offline', 'legacy-channel', 'delivered', 0, ?, ?), ('node_offline', 'legacy-channel', 'pending', 0, ?, ?)
	`, oldTS, oldTS, oldTS, oldTS); err != nil {
		t.Fatalf("insert legacy notification deliveries: %v", err)
	}

	if err := store.MaintainHistory(ctx, now); err != nil {
		t.Fatalf("maintain history: %v", err)
	}
	for _, check := range []struct {
		name  string
		query string
	}{
		{name: "old state", query: `SELECT COUNT(*) FROM state_samples WHERE ts < ?`},
		{name: "old probe", query: `SELECT COUNT(*) FROM probe_rounds WHERE ts < ?`},
		{name: "old notification log", query: `SELECT COUNT(*) FROM notification_log WHERE ts < ?`},
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, check.query, now.Add(-rawHistoryRetention).Unix()).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != 0 {
			t.Fatalf("%s remaining = %d, want 0", check.name, count)
		}
	}
	for _, check := range []struct {
		name  string
		query string
	}{
		{name: "expired latency rollup", query: `SELECT COUNT(*) FROM latency_history_rollups WHERE bucket_start < ?`},
		{name: "expired state rollup", query: `SELECT COUNT(*) FROM state_history_rollups WHERE bucket_start < ?`},
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, check.query, now.Add(-historyRollupRetention).Unix()).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != 0 {
			t.Fatalf("%s remaining = %d, want 0", check.name, count)
		}
	}
	for _, check := range []struct {
		name  string
		query string
	}{
		{name: "recent state", query: `SELECT COUNT(*) FROM state_samples WHERE ts = ?`},
		{name: "recent probe", query: `SELECT COUNT(*) FROM probe_rounds WHERE ts = ?`},
		{name: "recent notification log", query: `SELECT COUNT(*) FROM notification_log WHERE channel_id = 'recent-channel' AND ts = ?`},
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, check.query, recentTS).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != 1 {
			t.Fatalf("%s count = %d, want 1", check.name, count)
		}
	}
	var legacyRows, legacyPending int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*), SUM(state = 'pending') FROM notification_deliveries WHERE channel_id = 'legacy-channel'`).Scan(&legacyRows, &legacyPending); err != nil {
		t.Fatalf("count legacy deliveries: %v", err)
	}
	if legacyRows != 2 || legacyPending != 1 {
		t.Fatalf("legacy deliveries rows=%d pending=%d, want both rows untouched", legacyRows, legacyPending)
	}
}

func TestPruneRawHistoryBoundsAndResumesLargeBacklog(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := store.db.ExecContext(ctx, `INSERT INTO nodes (id, display_name, token_hash, status, created_at, updated_at) VALUES ('bounded-node', 'Bounded Node', 'hash', 'online', ?, ?)`, now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	total := historyRetentionMaxBatchCycles*historyRetentionBatchSize + 5
	oldTS := now.Add(-rawHistoryRetention - time.Hour).Unix()
	if _, err := store.db.ExecContext(ctx, `
		WITH RECURSIVE sequence(value) AS (
			SELECT 1
			UNION ALL
			SELECT value + 1 FROM sequence WHERE value < ?
		)
		INSERT INTO state_samples (node_id, ts, cpu_percent)
		SELECT 'bounded-node', ? - value, 10 FROM sequence
	`, total, oldTS); err != nil {
		t.Fatalf("insert bounded backlog: %v", err)
	}

	cutoff := now.Add(-rawHistoryRetention)
	if err := store.PruneRawHistory(ctx, cutoff); err != nil {
		t.Fatalf("first bounded prune: %v", err)
	}
	var remaining int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_samples WHERE ts < ?`, cutoff.Unix()).Scan(&remaining); err != nil {
		t.Fatalf("count bounded remainder: %v", err)
	}
	if remaining != 5 {
		t.Fatalf("bounded remainder = %d, want 5", remaining)
	}
	if err := store.PruneRawHistory(ctx, cutoff); err != nil {
		t.Fatalf("resume bounded prune: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_samples WHERE ts < ?`, cutoff.Unix()).Scan(&remaining); err != nil {
		t.Fatalf("count resumed remainder: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("resumed remainder = %d, want 0", remaining)
	}
}
