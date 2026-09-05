package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shui1iao/zeno/internal/controller/history"
)

func TestAdminDeletionRollupBatchesAreBounded(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kind  string
		table string
	}{
		{"node_state", "node", "state_history_rollups"},
		{"node_latency", "node", "latency_history_rollups"},
		{"target_latency", "probe_target", "latency_history_rollups"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newAdminDeletionContentionStore(t)
			ctx := context.Background()
			rowCount := adminDeleteBatchSize*2 + 3
			seedAdminDeleteRollups(t, store, tc.table, "example-node-a", "example-node-a-local", rowCount)
			// Same bucket values on another entity must not be selected or
			// cascaded away by either node or target cleanup.
			if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "survivor", DisplayName: "Survivor", CountryCode: "US", AgentToken: "survivor-token"}); err != nil {
				t.Fatal(err)
			}
			seedAdminDeleteRollups(t, store, tc.table, "survivor", "google-dns", 1)
			job, parentQuery := enqueueAdminRollupDeletion(t, store, tc.kind)
			countQuery := "SELECT COUNT(*) FROM " + tc.table + " WHERE node_id = 'example-node-a'"
			for remaining := rowCount; remaining > 0; {
				processed, err := store.processNextAdminDeletionBatch(ctx)
				if err != nil || !processed {
					t.Fatalf("rollup batch = %v, %v", processed, err)
				}
				remaining -= min(remaining, adminDeleteBatchSize)
				assertAdminDeleteCount(t, store, countQuery, remaining)
				// Even the last non-empty history batch must release its writer
				// before the distinct final metadata transaction.
				assertAdminDeleteCount(t, store, parentQuery, 1)
				assertAdminDeleteCount(t, store, "SELECT COUNT(*) FROM "+tc.table+" WHERE node_id = 'survivor'", 1)
				assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM admin_deletion_jobs WHERE state = 'running'`, 1)
			}
			drainAdminDeletions(t, store, 5*time.Second)
			assertAdminDeleteCount(t, store, parentQuery, 0)
			waitForAdminDeletionCompleted(t, store, job.kind, job.id, time.Second)
			if err := finalizeAdminRollupDeletion(ctx, store, job); err != nil {
				t.Fatalf("repeat finalization: %v", err)
			}
			assertSQLiteForeignKeysClean(t, store)
		})
	}
}

func TestAdminDeletionFinalizeRejectsRemainingRollups(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kind  string
		table string
	}{
		{"node_state", "node", "state_history_rollups"},
		{"node_latency", "node", "latency_history_rollups"},
		{"target_latency", "probe_target", "latency_history_rollups"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newAdminDeletionContentionStore(t)
			seedAdminDeleteRollups(t, store, tc.table, "example-node-a", "example-node-a-local", 1)
			job, parentQuery := enqueueAdminRollupDeletion(t, store, tc.kind)
			err := finalizeAdminRollupDeletion(context.Background(), store, job)
			if !errors.Is(err, errAdminDeletionHistoryRemaining) {
				t.Fatalf("finalize with only %s remaining = %v, want history remaining (no cascade)", tc.table, err)
			}
			assertAdminDeleteCount(t, store, parentQuery, 1)
			assertAdminDeleteCount(t, store, "SELECT COUNT(*) FROM "+tc.table, 1)
			assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM admin_deletion_jobs WHERE state = 'pending' AND completed_at IS NULL`, 1)
			assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM node_probe_targets WHERE node_id = 'example-node-a' AND target_id = 'example-node-a-local'`, 1)
			assertSQLiteForeignKeysClean(t, store)
		})
	}
}

func TestAdminDeletionRollupsResumeAfterRestart(t *testing.T) {
	for _, kind := range []string{"node", "probe_target"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "zeno.db")
			store, err := OpenSQLiteStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			store.stopAdminDeletionWorker()
			ctx := context.Background()
			if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", AgentToken: "test-token"}); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"state_history_rollups", "latency_history_rollups"} {
				seedAdminDeleteRollups(t, store, table, "example-node-a", "example-node-a-local", adminDeleteBatchSize+1)
			}
			job, parentQuery := enqueueAdminRollupDeletion(t, store, kind)
			if processed, err := store.processNextAdminDeletionBatch(ctx); err != nil || !processed {
				t.Fatalf("first rollup batch = %v, %v", processed, err)
			}
			stateRemaining, latencyRemaining := adminDeleteBatchSize+1, 1
			if kind == "node" {
				stateRemaining, latencyRemaining = 1, adminDeleteBatchSize+1
			}
			// Node state and latency must not share one non-empty batch.
			assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM state_history_rollups`, stateRemaining)
			assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM latency_history_rollups`, latencyRemaining)
			assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM admin_deletion_jobs WHERE state = 'running'`, 1)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenSQLiteStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			waitForAdminDeletionCompleted(t, reopened, job.kind, job.id, 5*time.Second)
			assertAdminDeleteCount(t, reopened, parentQuery, 0)
			assertAdminDeleteCount(t, reopened, `SELECT COUNT(*) FROM latency_history_rollups`, 0)
			if kind == "node" {
				stateRemaining = 0
			}
			assertAdminDeleteCount(t, reopened, `SELECT COUNT(*) FROM state_history_rollups`, stateRemaining)
			assertSQLiteForeignKeysClean(t, reopened)
		})
	}
}

func enqueueAdminRollupDeletion(t *testing.T, store *SQLiteStore, kind string) (adminDeletionJob, string) {
	t.Helper()
	ctx := context.Background()
	if kind == "node" {
		if err := store.DeleteAdminNode(ctx, "example-node-a"); err != nil {
			t.Fatal(err)
		}
		return adminDeletionJob{kind: kind, id: "example-node-a"}, `SELECT COUNT(*) FROM nodes WHERE id = 'example-node-a'`
	}
	if err := store.DeleteAdminProbeTarget(ctx, "example-node-a-local"); err != nil {
		t.Fatal(err)
	}
	return adminDeletionJob{kind: kind, id: "example-node-a-local"}, `SELECT COUNT(*) FROM probe_targets WHERE id = 'example-node-a-local'`
}

func finalizeAdminRollupDeletion(ctx context.Context, store *SQLiteStore, job adminDeletionJob) error {
	if job.kind == "node" {
		return store.finalizeAdminNodeDeletion(ctx, job.id)
	}
	return store.finalizeAdminProbeTargetDeletion(ctx, job.id)
}

func seedAdminDeleteRollups(t *testing.T, store *SQLiteStore, table, nodeID, targetID string, count int) {
	t.Helper()
	columns := []string{"node_id", "target_id", "bucket_start", "median_sum", "median_count", "avg_sum", "avg_count", "loss_sum", "loss_count"}
	values := []string{"?", "?", "bucket", "1", "1", "1", "1", "0", "1"}
	args := []any{count, nodeID, targetID}
	if table == "state_history_rollups" {
		columns = []string{"node_id", "bucket_start"}
		values = []string{"?", "bucket"}
		args = []any{count, nodeID}
		for _, metric := range history.StateRollupMetrics {
			columns = append(columns, metric+"_sum", metric+"_count")
			values = append(values, "1", "1")
		}
	} else if table != "latency_history_rollups" {
		t.Fatalf("unsupported rollup table %q", table)
	}
	_, err := store.db.ExecContext(context.Background(), `
		WITH RECURSIVE buckets(bucket) AS (
			SELECT 1 UNION ALL SELECT bucket + 1 FROM buckets WHERE bucket < ?
		)
		INSERT INTO `+table+` (`+strings.Join(columns, ",")+`)
		SELECT `+strings.Join(values, ",")+` FROM buckets
	`, args...)
	if err != nil {
		t.Fatalf("seed %s: %v", table, err)
	}
}
