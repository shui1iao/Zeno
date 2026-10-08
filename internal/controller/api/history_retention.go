package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/shui1iao/zeno/internal/controller/history"
)

// Retention policy and SQL live in internal/controller/history. This file
// keeps the store-facing execution: batching against the agent write
// scheduler, the rollback grace decision, and the background loop.

const (
	rawHistoryRetention            = history.RawRetention
	historyRollupRetention         = history.RollupRetention
	historyRetentionBatchSize      = history.BatchSize
	historyRetentionBatchPause     = history.BatchPause
	historyRetentionMaxBatchCycles = history.MaxBatchCycles
	historyRetentionScheduleOffset = history.ScheduleOffset
	historyRetentionVacuumPages    = history.VacuumPagesPerPass
)

const (
	// One slow backlog must not consume the whole maintenance pass and starve
	// rollup expiry, notification expiry, or incremental vacuum indefinitely.
	// Each stage releases SQLite's writer between small batches; these budgets
	// bound total background work without turning maintenance into one long
	// transaction.
	historyRetentionPassTimeout        = 4 * time.Minute
	historyRetentionVacuumBudget       = 30 * time.Second
	historyRetentionRawBudget          = 90 * time.Second
	historyRetentionRollupBudget       = 30 * time.Second
	historyRetentionNotificationBudget = 15 * time.Second
	// A single 20k-page PRAGMA exceeded 15 seconds on the production-sized
	// database. One thousand pages stayed below the shared 8-second writer
	// lease, so a pass is split into resumable chunks that release the scheduler
	// between each filesystem truncation.
	historyRetentionVacuumStepPages = 1000
)

// sqliteAutoVacuumIncremental is the PRAGMA auto_vacuum value for INCREMENTAL.
const sqliteAutoVacuumIncremental = 2

const (
	pruneExpiredProbeRoundsSQL  = history.PruneExpiredProbeRoundsSQL
	pruneExpiredStateSamplesSQL = history.PruneExpiredStateSamplesSQL
)

type historyRetentionStore interface {
	MaintainHistory(ctx context.Context, now time.Time) error
}

type sqliteHistoryStore struct {
	db     *sql.DB
	writes *sqliteWriteState
}

// PruneRawHistory compacts raw high-frequency samples into the rollup tiers
// once the one-day views no longer need them. Probe child samples disappear
// through the round foreign-key cascade.
func (s *sqliteHistoryStore) PruneRawHistory(ctx context.Context, before time.Time) error {
	latencyDone := false
	stateDone := false
	for cycle := 0; cycle < historyRetentionMaxBatchCycles && (!latencyDone || !stateDone); cycle++ {
		if !latencyDone {
			removed, err := s.compactLatencyHistoryBatch(ctx, before)
			if err != nil {
				return err
			}
			latencyDone = removed < historyRetentionBatchSize
		}
		if !stateDone {
			removed, err := s.compactStateHistoryBatch(ctx, before)
			if err != nil {
				return err
			}
			stateDone = removed < historyRetentionBatchSize
		}
		if !latencyDone || !stateDone {
			if err := pauseHistoryRetentionBatch(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *sqliteHistoryStore) MaintainHistory(ctx context.Context, now time.Time) error {
	now = now.UTC()
	cutoffs := history.CutoffsAt(now)
	return runHistoryMaintenanceStages(ctx, []historyMaintenanceStage{
		{
			name:    "reclaim existing free pages",
			timeout: historyRetentionVacuumBudget,
			run:     s.reclaimFreePages,
		},
		{
			name:    "compact raw history",
			timeout: historyRetentionRawBudget,
			run: func(stageCtx context.Context) error {
				rollupReady, err := s.historyRollupReady(stageCtx, now)
				if err != nil {
					return err
				}
				return s.pruneRawHistoryTier(stageCtx, cutoffs, rollupReady)
			},
		},
		{
			name:    "prune rollup history",
			timeout: historyRetentionRollupBudget,
			run:     func(stageCtx context.Context) error { return s.pruneRollupTiers(stageCtx, cutoffs) },
		},
		{
			name:    "prune notification history",
			timeout: historyRetentionNotificationBudget,
			run:     func(stageCtx context.Context) error { return s.pruneNotificationHistory(stageCtx, cutoffs) },
		},
		{
			name:    "reclaim newly freed pages",
			timeout: historyRetentionVacuumBudget,
			run:     s.reclaimFreePages,
		},
	})
}

type historyMaintenanceStage struct {
	name    string
	timeout time.Duration
	run     func(context.Context) error
}

// runHistoryMaintenanceStages gives every independent maintenance domain a
// chance to progress. A timed-out raw-history backlog is reported, but it no
// longer prevents rollup/notification expiry or the bounded vacuum stages.
func runHistoryMaintenanceStages(ctx context.Context, stages []historyMaintenanceStage) error {
	var stageErrors []error
	for _, stage := range stages {
		if err := ctx.Err(); err != nil {
			stageErrors = append(stageErrors, err)
			break
		}
		stageCtx, cancel := context.WithTimeout(ctx, stage.timeout)
		err := stage.run(stageCtx)
		cancel()
		if err != nil {
			stageErrors = append(stageErrors, fmt.Errorf("%s: %w", stage.name, err))
		}
	}
	return errors.Join(stageErrors...)
}

// reclaimFreePages returns pages freed by this pass to the filesystem so a
// database that just shed millions of rows also shrinks on disk. It is a
// bounded no-op when auto_vacuum is NONE (every database created before the
// INCREMENTAL default) or when nothing is on the freelist, and it never runs
// a full VACUUM: that would hold the single writer for minutes.
func (s *sqliteHistoryStore) reclaimFreePages(ctx context.Context) error {
	var autoVacuum int
	if err := s.db.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&autoVacuum); err != nil {
		return err
	}
	if autoVacuum != sqliteAutoVacuumIncremental {
		return nil
	}
	var freelist int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&freelist); err != nil {
		return err
	}
	if freelist <= 0 {
		return nil
	}
	remaining := min(freelist, int64(historyRetentionVacuumPages))
	for remaining > 0 {
		pages := min(remaining, int64(historyRetentionVacuumStepPages))
		if err := s.writes.withAgentWrite(ctx, historyRetentionWriteKey, func(writeCtx context.Context) error {
			_, err := s.db.ExecContext(writeCtx, `PRAGMA incremental_vacuum(`+strconv.FormatInt(pages, 10)+`)`)
			return err
		}); err != nil {
			return err
		}
		remaining -= pages
		if remaining > 0 {
			if err := pauseHistoryRetentionBatch(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// pruneRawHistoryTier either compacts raw rows into rollups or, during the
// rollback grace period, preserves the previous release's complete 30-day raw
// view and removes only data already outside the supported range.
func (s *sqliteHistoryStore) pruneRawHistoryTier(ctx context.Context, cutoffs history.Cutoffs, rollupReady bool) error {
	if rollupReady {
		return s.PruneRawHistory(ctx, time.Unix(cutoffs.Raw, 0).UTC())
	}
	return s.pruneHistoryStatements(ctx, cutoffs.LegacyRaw,
		history.PruneExpiredProbeRoundsSQL,
		history.PruneExpiredStateSamplesSQL,
	)
}

func (s *sqliteHistoryStore) pruneRollupTiers(ctx context.Context, cutoffs history.Cutoffs) error {
	if err := s.pruneHistoryStatements(ctx, cutoffs.LatencyRollup, history.PruneExpiredLatencyRollupsSQL); err != nil {
		return err
	}
	return s.pruneHistoryStatements(ctx, cutoffs.StateRollup, history.PruneExpiredStateRollupsSQL)
}

func (s *sqliteHistoryStore) pruneHistoryStatements(ctx context.Context, cutoff int64, statements ...string) error {
	for _, statement := range statements {
		if err := s.pruneRowsInBatches(ctx, statement, cutoff); err != nil {
			return err
		}
	}
	return nil
}

// pruneNotificationHistory trims the notification log. The legacy
// notification_deliveries table is kept read-only for rollback and is no
// longer pruned or expired by this release.
func (s *sqliteHistoryStore) pruneNotificationHistory(ctx context.Context, cutoffs history.Cutoffs) error {
	return s.pruneRowsInBatches(ctx, history.PruneNotificationLogSQL, cutoffs.NotificationHistory)
}

func (s *sqliteHistoryStore) historyRollupReady(ctx context.Context, now time.Time) (bool, error) {
	var enabledAfter int64
	if err := s.db.QueryRowContext(ctx, `SELECT enabled_after FROM history_rollup_meta WHERE id = 1`).Scan(&enabledAfter); err != nil {
		return false, err
	}
	return !now.Before(time.Unix(enabledAfter, 0).UTC()), nil
}

// runHistoryBatches repeats a bounded write until it stops filling a batch or
// the cycle budget runs out, leaving any remainder for the next pass.
func (s *sqliteHistoryStore) runHistoryBatches(ctx context.Context, exec func(context.Context) (int64, error)) error {
	for cycle := 0; cycle < historyRetentionMaxBatchCycles; cycle++ {
		var affected int64
		err := s.writes.withAgentWrite(ctx, historyRetentionWriteKey, func(writeCtx context.Context) error {
			var execErr error
			affected, execErr = exec(writeCtx)
			return execErr
		})
		if err != nil {
			return err
		}
		if affected < historyRetentionBatchSize {
			return nil
		}
		if err := pauseHistoryRetentionBatch(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqliteHistoryStore) pruneRowsInBatches(ctx context.Context, query string, cutoff int64) error {
	return s.runHistoryBatches(ctx, func(writeCtx context.Context) (int64, error) {
		result, err := s.db.ExecContext(writeCtx, query, cutoff, historyRetentionBatchSize)
		if err != nil {
			return 0, err
		}
		return result.RowsAffected()
	})
}

func pauseHistoryRetentionBatch(ctx context.Context) error {
	timer := time.NewTimer(historyRetentionBatchPause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (h *handler) runHistoryRetention(ctx context.Context, interval time.Duration) {
	store, ok := h.store.(historyRetentionStore)
	if !ok || interval <= 0 {
		return
	}
	prune := func() {
		pruneCtx, cancel := context.WithTimeout(ctx, historyRetentionPassTimeout)
		defer cancel()
		if err := store.MaintainHistory(pruneCtx, time.Now().UTC()); err != nil {
			log.Printf("history retention cleanup failed: %v", err)
		}
	}
	first := time.NewTimer(historyRetentionFirstDelay(interval))
	select {
	case <-ctx.Done():
		if !first.Stop() {
			select {
			case <-first.C:
			default:
			}
		}
		return
	case <-first.C:
		prune()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}

func historyRetentionFirstDelay(interval time.Duration) time.Duration {
	return history.FirstDelay(interval)
}
