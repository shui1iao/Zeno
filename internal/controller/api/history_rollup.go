package api

import (
	"context"
	"database/sql"
	"time"

	"github.com/shui1iao/zeno/internal/controller/history"
)

// Rollup bucket widths, metric list and SQL generation live in
// internal/controller/history. This file executes one compaction batch inside
// a transaction and scans aggregated rows back into API types.

func (s *sqliteHistoryStore) compactStateHistoryBatch(ctx context.Context, before time.Time) (int64, error) {
	return s.compactHistoryTier(ctx, before, history.StateRollupStep, history.StateRollupInsertQuery, history.PruneExpiredStateSamplesSQL)
}

func (s *sqliteHistoryStore) compactLatencyHistoryBatch(ctx context.Context, before time.Time) (int64, error) {
	return s.compactHistoryTier(ctx, before, history.LatencyRollupStep, history.LatencyRollupInsertSQL, history.PruneExpiredProbeRoundsSQL)
}

func (s *sqliteHistoryStore) compactHistoryTier(ctx context.Context, before time.Time, step time.Duration, insertSQL, deleteSQL string) (int64, error) {
	stepSeconds := history.StepSeconds(step)
	return s.compactHistoryBatch(ctx, insertSQL, []any{before.UTC().Unix(), historyRetentionBatchSize, stepSeconds, stepSeconds}, deleteSQL, before)
}

// compactHistoryBatch folds one batch of raw rows into its rollup tier and
// deletes exactly those rows in the same transaction, so a crash can never
// drop raw data that was not aggregated.
func (s *sqliteHistoryStore) compactHistoryBatch(ctx context.Context, insertSQL string, insertArgs []any, deleteSQL string, before time.Time) (int64, error) {
	var removed int64
	err := s.writes.withAgentWrite(ctx, historyRetentionWriteKey, func(writeCtx context.Context) error {
		tx, err := s.db.BeginTx(writeCtx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(writeCtx, insertSQL, insertArgs...); err != nil {
			return err
		}
		result, err := tx.ExecContext(writeCtx, deleteSQL, before.UTC().Unix(), historyRetentionBatchSize)
		if err != nil {
			return err
		}
		removed, err = result.RowsAffected()
		if err != nil {
			return err
		}
		return tx.Commit()
	})
	return removed, err
}

// scanStateHistoryPointInto unpacks one aggregated row. Column order must match
// history.StateRollupMetrics.
func scanStateHistoryPointInto(rows *sql.Rows, slab *floatSlab) (StatePoint, error) {
	var ts int64
	var cpuPercent, load1, load5, load15, memoryUsed, memoryTotal, swapUsed, swapTotal, diskUsed, diskTotal, netInTotal, netOutTotal, netInSpeed, netOutSpeed, processCount, tcpConnectionCount, udpConnectionCount, uptimeSeconds sql.NullFloat64
	if err := rows.Scan(&ts, &cpuPercent, &load1, &load5, &load15, &memoryUsed, &memoryTotal, &swapUsed, &swapTotal, &diskUsed, &diskTotal, &netInTotal, &netOutTotal, &netInSpeed, &netOutSpeed, &processCount, &tcpConnectionCount, &udpConnectionCount, &uptimeSeconds); err != nil {
		return StatePoint{}, err
	}
	return StatePoint{
		TS:                 unixRFC3339(ts),
		TSUnix:             ts,
		CPUPercent:         slab.nullFloatPtr(cpuPercent),
		Load1:              slab.nullFloatPtr(load1),
		Load5:              slab.nullFloatPtr(load5),
		Load15:             slab.nullFloatPtr(load15),
		MemoryUsedBytes:    slab.nullFloatPtr(memoryUsed),
		MemoryTotalBytes:   slab.nullFloatPtr(memoryTotal),
		SwapUsedBytes:      slab.nullFloatPtr(swapUsed),
		SwapTotalBytes:     slab.nullFloatPtr(swapTotal),
		DiskUsedBytes:      slab.nullFloatPtr(diskUsed),
		DiskTotalBytes:     slab.nullFloatPtr(diskTotal),
		NetInTotalBytes:    slab.nullFloatPtr(netInTotal),
		NetOutTotalBytes:   slab.nullFloatPtr(netOutTotal),
		NetInSpeedBps:      slab.nullFloatPtr(netInSpeed),
		NetOutSpeedBps:     slab.nullFloatPtr(netOutSpeed),
		ProcessCount:       slab.nullFloatPtr(processCount),
		TCPConnectionCount: slab.nullFloatPtr(tcpConnectionCount),
		UDPConnectionCount: slab.nullFloatPtr(udpConnectionCount),
		UptimeSeconds:      slab.nullFloatPtr(uptimeSeconds),
	}, nil
}
