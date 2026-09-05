package api

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newAdminDeletionContentionStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.stopAdminDeletionWorker()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example", CountryCode: "HK", AgentToken: "test-token"}); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestAdminDeletionWritesUseSharedScheduler(t *testing.T) {
	job := adminDeletionJob{kind: "node", id: "example-node-a"}
	operations := []struct {
		name string
		run  func(context.Context, *SQLiteStore) error
	}{
		{"enqueue_node", func(ctx context.Context, s *SQLiteStore) error { return s.DeleteAdminNode(ctx, job.id) }},
		{"enqueue_target", func(ctx context.Context, s *SQLiteStore) error {
			return s.DeleteAdminProbeTarget(ctx, "example-node-a-local")
		}},
		{"mark_running", func(ctx context.Context, s *SQLiteStore) error { return s.markAdminDeletionJobRunning(ctx, job) }},
		{"record_error", func(_ context.Context, s *SQLiteStore) error {
			s.recordAdminDeletionError(job, errors.New("test retry"))
			return nil
		}},
		{"node_batch", func(ctx context.Context, s *SQLiteStore) error {
			_, err := s.processAdminNodeDeletionBatch(ctx, job.id)
			return err
		}},
		{"target_batch", func(ctx context.Context, s *SQLiteStore) error {
			_, err := s.processAdminProbeTargetDeletionBatch(ctx, "example-node-a-local")
			return err
		}},
		{"state_batch", func(ctx context.Context, s *SQLiteStore) error {
			_, err := s.deleteAdminRowsBatch(ctx, deleteNodeStateSamplesBatchSQL, job.id)
			return err
		}},
		{"node_state_rollup_batch", func(ctx context.Context, s *SQLiteStore) error {
			_, err := s.deleteAdminRowsBatch(ctx, deleteNodeStateRollupsBatchSQL, job.id)
			return err
		}},
		{"node_latency_rollup_batch", func(ctx context.Context, s *SQLiteStore) error {
			_, err := s.deleteAdminRowsBatch(ctx, deleteNodeLatencyRollupsBatchSQL, job.id)
			return err
		}},
		{"target_latency_rollup_batch", func(ctx context.Context, s *SQLiteStore) error {
			_, err := s.deleteAdminRowsBatch(ctx, deleteTargetLatencyRollupsBatchSQL, "example-node-a-local")
			return err
		}},
		{"finalize_node", func(ctx context.Context, s *SQLiteStore) error { return s.finalizeAdminNodeDeletion(ctx, job.id) }},
		{"finalize_target", func(ctx context.Context, s *SQLiteStore) error {
			return s.finalizeAdminProbeTargetDeletion(ctx, "example-node-a-local")
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			store := newAdminDeletionContentionStore(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			// Hold only the fair scheduler, not a SQLite transaction: an admin
			// operation finishing now proves that it bypassed admission entirely.
			release, err := store.scheduler.acquire(ctx, "live-agent")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			done := make(chan error, 1)
			go func() { done <- operation.run(ctx, store) }()
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				store.scheduler.mu.Lock()
				queued := store.scheduler.queued
				store.scheduler.mu.Unlock()
				if queued > 0 {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("admin write bypassed shared scheduler: returned %v while live-agent holds permit", err)
				case <-ctx.Done():
					t.Fatal("admin operation neither queued nor completed")
				case <-ticker.C:
				}
			}
			release()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("admin operation after scheduler release: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("admin operation did not finish after admission (nested acquisition?)")
			}
		})
	}
}

func TestAdminDeletionBoundsRoundSearchAndRemovesEmptyRounds(t *testing.T) {
	for _, kind := range []string{"node", "probe_target"} {
		t.Run(kind, func(t *testing.T) {
			store := newAdminDeletionContentionStore(t)
			ctx := context.Background()
			// Model a partly drained durable job: a large prefix of rounds has
			// no samples, followed by one nonempty round. LIMIT on joined sample
			// rows alone scans past the entire empty prefix under a write lock.
			seedAdminDeleteHistory(t, store, "example-node-a", "example-node-a-local", adminDeleteBatchSize+1, 0)
			if _, err := store.db.ExecContext(ctx, `INSERT INTO probe_samples (round_id, seq, success) SELECT MAX(id), 1, 1 FROM probe_rounds`); err != nil {
				t.Fatal(err)
			}
			var processed bool
			var err error
			if kind == "node" {
				processed, err = store.processAdminNodeDeletionBatch(ctx, "example-node-a")
			} else {
				processed, err = store.processAdminProbeTargetDeletionBatch(ctx, "example-node-a-local")
			}
			if err != nil || !processed {
				t.Fatalf("first bounded batch = %v, %v", processed, err)
			}
			assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM probe_samples`, 1)
			assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM probe_rounds`, 1)
			assertSQLiteForeignKeysClean(t, store)
		})
	}
}

func TestAdminDeletionSampleAndRoundBatchIsAtomic(t *testing.T) {
	store := newAdminDeletionContentionStore(t)
	ctx := context.Background()
	seedAdminDeleteHistory(t, store, "example-node-a", "example-node-a-local", 1, 2)
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER fail_admin_round_delete BEFORE DELETE ON probe_rounds BEGIN SELECT RAISE(ABORT, 'round delete failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.processAdminProbeTargetDeletionBatch(ctx, "example-node-a-local"); err == nil {
		t.Fatal("batch succeeded: samples and newly empty rounds were not cleaned atomically")
	}
	assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM probe_samples`, 2)
	assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM probe_rounds`, 1)
	assertSQLiteForeignKeysClean(t, store)
}
