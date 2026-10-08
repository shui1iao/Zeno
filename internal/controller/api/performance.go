package api

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

type runtimePerformance struct {
	startedAt            time.Time
	summaryFreshHits     atomic.Uint64
	summaryStaleHits     atomic.Uint64
	summaryMisses        atomic.Uint64
	summaryBuilds        atomic.Uint64
	summaryBuildFailures atomic.Uint64
	summaryBuildNanos    atomic.Uint64
	summaryBuildMaxNanos atomic.Uint64
	summaryLastBytes     atomic.Uint64
}

type runtimePerformanceSnapshot struct {
	UptimeSeconds int64 `json:"uptime_seconds"`
	Summary       struct {
		FreshCacheHits uint64  `json:"fresh_cache_hits"`
		StaleCacheHits uint64  `json:"stale_cache_hits"`
		CacheMisses    uint64  `json:"cache_misses"`
		Builds         uint64  `json:"builds"`
		BuildFailures  uint64  `json:"build_failures"`
		BuildTotalMS   float64 `json:"build_total_ms"`
		BuildMaxMS     float64 `json:"build_max_ms"`
		LastBytes      uint64  `json:"last_bytes"`
	} `json:"summary"`
	SQLite struct {
		BusyRetries uint64 `json:"busy_retries"`
		// The outbox_* keys are kept for API compatibility and now describe
		// notification_states: pending = rows with an undelivered target,
		// failed = rows whose current target failed at least once, leased = 0.
		OutboxPending        int64  `json:"outbox_pending"`
		OutboxLeased         int64  `json:"outbox_leased"`
		OutboxFailed         int64  `json:"outbox_failed"`
		LatencyRollupRows    int64  `json:"latency_rollup_rows_approx"`
		StateRollupRows      int64  `json:"state_rollup_rows_approx"`
		RawProbeRoundsApprox int64  `json:"raw_probe_rounds_approx"`
		RawStateRowsApprox   int64  `json:"raw_state_rows_approx"`
		RollupEnabledAfter   string `json:"rollup_enabled_after"`
		RollupReady          bool   `json:"rollup_ready"`
	} `json:"sqlite"`
}

type sqliteRuntimePerformance struct {
	BusyRetries          uint64
	OutboxPending        int64
	OutboxLeased         int64
	OutboxFailed         int64
	LatencyRollupRows    int64
	StateRollupRows      int64
	RawProbeRoundsApprox int64
	RawStateRowsApprox   int64
	RollupEnabledAfter   int64
}

type runtimePerformanceStore interface {
	RuntimePerformance(ctx context.Context) (sqliteRuntimePerformance, error)
}

func newRuntimePerformance() *runtimePerformance {
	return &runtimePerformance{startedAt: time.Now()}
}

func (performance *runtimePerformance) recordSummaryBuild(duration time.Duration, bytes int, err error) {
	if performance == nil {
		return
	}
	performance.summaryBuilds.Add(1)
	if err != nil {
		performance.summaryBuildFailures.Add(1)
	}
	nanos := uint64(max(duration.Nanoseconds(), 0))
	performance.summaryBuildNanos.Add(nanos)
	for {
		current := performance.summaryBuildMaxNanos.Load()
		if nanos <= current || performance.summaryBuildMaxNanos.CompareAndSwap(current, nanos) {
			break
		}
	}
	if bytes > 0 {
		performance.summaryLastBytes.Store(uint64(bytes))
	}
}

func (performance *runtimePerformance) snapshot(now time.Time) runtimePerformanceSnapshot {
	var snapshot runtimePerformanceSnapshot
	if performance == nil {
		return snapshot
	}
	snapshot.UptimeSeconds = int64(max(now.Sub(performance.startedAt).Seconds(), 0))
	snapshot.Summary.FreshCacheHits = performance.summaryFreshHits.Load()
	snapshot.Summary.StaleCacheHits = performance.summaryStaleHits.Load()
	snapshot.Summary.CacheMisses = performance.summaryMisses.Load()
	snapshot.Summary.Builds = performance.summaryBuilds.Load()
	snapshot.Summary.BuildFailures = performance.summaryBuildFailures.Load()
	snapshot.Summary.BuildTotalMS = float64(performance.summaryBuildNanos.Load()) / float64(time.Millisecond)
	snapshot.Summary.BuildMaxMS = float64(performance.summaryBuildMaxNanos.Load()) / float64(time.Millisecond)
	snapshot.Summary.LastBytes = performance.summaryLastBytes.Load()
	return snapshot
}

func (s *sqliteMonitoringDomain) RuntimePerformance(ctx context.Context) (sqliteRuntimePerformance, error) {
	snapshot := sqliteRuntimePerformance{BusyRetries: s.writes.busyRetries.Load()}
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN pending_target <> '' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN pending_attempts > 0 THEN 1 ELSE 0 END), 0)
		FROM notification_states
	`).Scan(&snapshot.OutboxPending, &snapshot.OutboxFailed); err != nil {
		return sqliteRuntimePerformance{}, err
	}
	// MAX(rowid) is index-local and remains cheap on multi-gigabyte databases.
	// It is deliberately labelled approximate because retention creates gaps.
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE((SELECT MAX(rowid) FROM latency_history_rollups), 0) - COALESCE((SELECT MIN(rowid) FROM latency_history_rollups), 0) + CASE WHEN EXISTS (SELECT 1 FROM latency_history_rollups LIMIT 1) THEN 1 ELSE 0 END,
			COALESCE((SELECT MAX(rowid) FROM state_history_rollups), 0) - COALESCE((SELECT MIN(rowid) FROM state_history_rollups), 0) + CASE WHEN EXISTS (SELECT 1 FROM state_history_rollups LIMIT 1) THEN 1 ELSE 0 END,
			COALESCE((SELECT MAX(id) FROM probe_rounds), 0) - COALESCE((SELECT MIN(id) FROM probe_rounds), 0) + CASE WHEN EXISTS (SELECT 1 FROM probe_rounds LIMIT 1) THEN 1 ELSE 0 END,
			COALESCE((SELECT MAX(id) FROM state_samples), 0) - COALESCE((SELECT MIN(id) FROM state_samples), 0) + CASE WHEN EXISTS (SELECT 1 FROM state_samples LIMIT 1) THEN 1 ELSE 0 END
	`).Scan(&snapshot.LatencyRollupRows, &snapshot.StateRollupRows, &snapshot.RawProbeRoundsApprox, &snapshot.RawStateRowsApprox); err != nil {
		return sqliteRuntimePerformance{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT enabled_after FROM history_rollup_meta WHERE id = 1`).Scan(&snapshot.RollupEnabledAfter); err != nil {
		return sqliteRuntimePerformance{}, err
	}
	return snapshot, nil
}

func (h *handler) handleAdminPerformance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := h.authorizeAdminRequest(w, r); !ok {
		return
	}
	snapshot := h.performance.snapshot(time.Now())
	if store, ok := h.store.(runtimePerformanceStore); ok {
		sqliteSnapshot, err := store.RuntimePerformance(r.Context())
		if err != nil {
			writeStoreError(w, err)
			return
		}
		snapshot.SQLite.BusyRetries = sqliteSnapshot.BusyRetries
		snapshot.SQLite.OutboxPending = sqliteSnapshot.OutboxPending
		snapshot.SQLite.OutboxLeased = sqliteSnapshot.OutboxLeased
		snapshot.SQLite.OutboxFailed = sqliteSnapshot.OutboxFailed
		snapshot.SQLite.LatencyRollupRows = sqliteSnapshot.LatencyRollupRows
		snapshot.SQLite.StateRollupRows = sqliteSnapshot.StateRollupRows
		snapshot.SQLite.RawProbeRoundsApprox = sqliteSnapshot.RawProbeRoundsApprox
		snapshot.SQLite.RawStateRowsApprox = sqliteSnapshot.RawStateRowsApprox
		snapshot.SQLite.RollupEnabledAfter = time.Unix(sqliteSnapshot.RollupEnabledAfter, 0).UTC().Format(time.RFC3339)
		snapshot.SQLite.RollupReady = !time.Now().UTC().Before(time.Unix(sqliteSnapshot.RollupEnabledAfter, 0).UTC())
	}
	writeJSON(w, http.StatusOK, snapshot)
}
