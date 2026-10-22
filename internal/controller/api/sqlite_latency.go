package api

import (
	"context"
	"database/sql"
	"time"

	"github.com/shui1iao/zeno/internal/controller/history"
)

type sqliteLatencyQueries struct {
	db *sql.DB
}

func (s *sqliteLatencyQueries) latencyPoints(ctx context.Context, nodeID string, window latencyWindow) ([]LatencyPoint, error) {
	if useLatencyGrid(window) {
		return s.latencyGridPoints(ctx, nodeID, window)
	}
	since := time.Now().UTC().Add(-time.Duration(window.Samples) * window.Step).Unix()
	rows, err := s.db.QueryContext(ctx, `
		SELECT pr.ts, pr.target_id, pt.name, pr.median_ms, pr.avg_ms, pr.loss_percent
		FROM probe_rounds pr
		JOIN probe_targets pt ON pt.id = pr.target_id
		LEFT JOIN node_probe_targets npt ON npt.node_id = pr.node_id AND npt.target_id = pr.target_id
		WHERE pr.node_id = ?
		  AND pr.ts >= ?
		  AND COALESCE(npt.enabled, 0) = 1
		ORDER BY pr.ts ASC, pt.display_order ASC, pt.name ASC, pr.id ASC
	`, nodeID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanLatencyRows(rows, []LatencyPoint(nil), func(ts, targetID, targetName string, median, avg *float64, loss float64) LatencyPoint {
		return LatencyPoint{TS: ts, TargetID: targetID, TargetName: targetName, MedianMS: median, AvgMS: avg, LossPercent: loss}
	})
}

func scanLatencyRows[T any](rows *sql.Rows, points []T, point func(ts, dimensionID, dimensionName string, median, avg *float64, loss float64) T) ([]T, error) {
	for rows.Next() {
		var ts int64
		var dimensionID, dimensionName string
		var median, avg sql.NullFloat64
		var loss float64
		if err := rows.Scan(&ts, &dimensionID, &dimensionName, &median, &avg, &loss); err != nil {
			return nil, err
		}
		points = append(points, point(time.Unix(ts, 0).UTC().Format(time.RFC3339), dimensionID, dimensionName, floatPtr(median), floatPtr(avg), loss))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return points, nil
}

func useLatencyGrid(window latencyWindow) bool {
	gridWindow, ok := resolveLatencyGridWindow(window.Name)
	if !ok {
		return false
	}
	// Some unit tests pass a custom 1h latencyWindow directly to the store to
	// assert raw round storage. Public 1h requests use resolveLatencyWindow's
	// canonical 20 × 3m realtime grid and should stay bucketed for fast initial
	// chart paint.
	if window.Name == "1h" && (window.Samples != gridWindow.Samples || window.Step != gridWindow.Step) {
		return false
	}
	return true
}

type latencyGridTarget struct {
	ID   string
	Name string
}

type latencyGridPointValue struct {
	TS          string
	Series      latencyGridTarget
	MedianMS    *float64
	AvgMS       *float64
	LossPercent float64
}

type latencyGridBucket struct {
	medianSum   float64
	medianCount int
	avgSum      float64
	avgCount    int
	lossSum     float64
	lossCount   int
}

func (bucket *latencyGridBucket) add(median, avg sql.NullFloat64, loss float64) {
	if median.Valid {
		bucket.medianSum += median.Float64
		bucket.medianCount++
	}
	if avg.Valid {
		bucket.avgSum += avg.Float64
		bucket.avgCount++
	}
	bucket.lossSum += loss
	bucket.lossCount++
}

func (bucket latencyGridBucket) medianPtr() *float64 {
	if bucket.medianCount == 0 {
		return nil
	}
	value := bucket.medianSum / float64(bucket.medianCount)
	return &value
}

func (bucket latencyGridBucket) avgPtr() *float64 {
	if bucket.avgCount > 0 {
		value := bucket.avgSum / float64(bucket.avgCount)
		return &value
	}
	if bucket.medianCount > 0 {
		value := bucket.medianSum / float64(bucket.medianCount)
		return &value
	}
	return nil
}

func (bucket latencyGridBucket) lossPercent() float64 {
	if bucket.lossCount == 0 {
		return 0
	}
	return bucket.lossSum / float64(bucket.lossCount)
}

// latencyGridDimension captures the only differences between the node-oriented
// and target-oriented latency grids: which column selects the requested row set,
// which column identifies each returned series, and the dimension-specific joins
// and filters. Everything else (bucketing, window bounds, aggregation) is shared.
type latencyGridDimension struct {
	filterColumn       string
	seriesColumn       string
	sourceFilter       string
	extraJoins         string
	extraFilter        string
	seriesQuery        string
	sampleRoundFilter  string
	sampleRollupFilter string
}

var latencyGridByNode = latencyGridDimension{
	filterColumn: "node_id",
	seriesColumn: "target_id",
	// Constrain each source branch to the enabled target ids before SQLite
	// aggregates it. Besides skipping disabled history, this lets the existing
	// (node_id, target_id, ts/bucket_start) indexes seek each target's requested
	// time range instead of scanning the node's entire retained history.
	sourceFilter: " AND target_id IN (SELECT target_id FROM node_probe_targets WHERE node_id = ? AND enabled = 1)",
	seriesQuery: `
		SELECT pt.id AS series_id, pt.name AS series_name,
		       ROW_NUMBER() OVER (ORDER BY pt.display_order ASC, pt.name ASC, pt.id ASC) AS series_order
		FROM probe_targets pt
		LEFT JOIN node_probe_targets npt ON npt.target_id = pt.id AND npt.node_id = ?
		WHERE COALESCE(npt.enabled, 0) = 1
		ORDER BY pt.display_order ASC, pt.name ASC, pt.id ASC
	`,
	sampleRoundFilter:  "rounds.node_id = ? AND rounds.target_id = grid.series_id",
	sampleRollupFilter: "rollup_rows.node_id = ? AND rollup_rows.target_id = grid.series_id",
}

func latencyGridSampleQuery(dimension latencyGridDimension) string {
	return `
		WITH RECURSIVE buckets(bucket_ts, sample_index) AS (
			SELECT ?, 0
			UNION ALL
			SELECT bucket_ts + ?, sample_index + 1
			FROM buckets
			WHERE sample_index + 1 < ?
		),
		series AS (` + dimension.seriesQuery + `),
		grid AS (
			SELECT buckets.bucket_ts, series.series_id, series.series_name, series.series_order
			FROM buckets CROSS JOIN series
		)
		SELECT grid.bucket_ts, grid.series_id, grid.series_name,
		       CASE WHEN recent.ts IS NOT NULL AND (historical.bucket_start IS NULL OR recent.ts >= historical.bucket_start)
		            THEN recent.median_ms ELSE historical.median_sum / NULLIF(historical.median_count, 0) END,
		       CASE WHEN recent.ts IS NOT NULL AND (historical.bucket_start IS NULL OR recent.ts >= historical.bucket_start)
		            THEN COALESCE(recent.avg_ms, recent.median_ms)
		            ELSE COALESCE(historical.avg_sum / NULLIF(historical.avg_count, 0), historical.median_sum / NULLIF(historical.median_count, 0)) END,
		       CASE WHEN recent.ts IS NOT NULL AND (historical.bucket_start IS NULL OR recent.ts >= historical.bucket_start)
		            THEN recent.loss_percent ELSE historical.loss_sum / NULLIF(historical.loss_count, 0) END
		FROM grid
		LEFT JOIN probe_rounds AS recent ON recent.id = (
			SELECT rounds.id
			FROM probe_rounds AS rounds INDEXED BY idx_probe_rounds_node_target_ts
			WHERE ` + dimension.sampleRoundFilter + `
			  AND rounds.ts >= grid.bucket_ts AND rounds.ts < grid.bucket_ts + ?
			ORDER BY rounds.ts DESC, rounds.id DESC
			LIMIT 1
		)
		LEFT JOIN latency_history_rollups AS historical ON historical.rowid = (
			SELECT rollup_rows.rowid
			FROM latency_history_rollups AS rollup_rows
			WHERE ` + dimension.sampleRollupFilter + `
			  AND rollup_rows.bucket_start >= grid.bucket_ts AND rollup_rows.bucket_start < grid.bucket_ts + ?
			ORDER BY rollup_rows.bucket_start DESC
			LIMIT 1
		)
		ORDER BY grid.bucket_ts ASC, grid.series_order ASC
	`
}

func latencyGridSampleValuesFor(ctx context.Context, s *sqliteLatencyQueries, id string, window latencyWindow, dimension latencyGridDimension) ([]latencyGridPointValue, error) {
	start, _, stepSeconds := latencyGridBounds(window)
	rows, err := s.db.QueryContext(ctx, latencyGridSampleQuery(dimension),
		start.Unix(), stepSeconds, window.Samples,
		id, id, stepSeconds, id, stepSeconds,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := make([]latencyGridPointValue, 0, window.Samples)
	for rows.Next() {
		var bucketTS int64
		var series latencyGridTarget
		var median, avg, loss sql.NullFloat64
		if err := rows.Scan(&bucketTS, &series.ID, &series.Name, &median, &avg, &loss); err != nil {
			return nil, err
		}
		points = append(points, latencyGridPointValue{
			TS:          time.Unix(bucketTS, 0).UTC().Format(time.RFC3339),
			Series:      series,
			MedianMS:    floatPtr(median),
			AvgMS:       floatPtr(avg),
			LossPercent: nullFloatOr(loss, 0),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return points, nil
}

func latencyGridQuery(dimension latencyGridDimension) string {
	return `
		WITH measurements AS (
			SELECT ts, node_id, target_id,
			       COALESCE(median_ms, 0) AS median_sum, CASE WHEN median_ms IS NULL THEN 0 ELSE 1 END AS median_count,
			       COALESCE(avg_ms, 0) AS avg_sum, CASE WHEN avg_ms IS NULL THEN 0 ELSE 1 END AS avg_count,
			       loss_percent AS loss_sum, 1 AS loss_count
			FROM probe_rounds WHERE ` + dimension.filterColumn + ` = ? AND ts >= ? AND ts < ?` + dimension.sourceFilter + `
			UNION ALL
			SELECT bucket_start AS ts, node_id, target_id,
			       median_sum, median_count, avg_sum, avg_count, loss_sum, loss_count
			FROM latency_history_rollups WHERE ` + dimension.filterColumn + ` = ? AND bucket_start >= ? AND bucket_start < ?` + dimension.sourceFilter + `
		)
		SELECT (measurements.ts / ?) * ? AS bucket_ts, measurements.` + dimension.seriesColumn + `,
		       SUM(median_sum) / NULLIF(SUM(median_count), 0),
		       SUM(avg_sum) / NULLIF(SUM(avg_count), 0),
		       SUM(loss_sum) / NULLIF(SUM(loss_count), 0)
		FROM measurements` + dimension.extraJoins + `
		JOIN probe_targets pt ON pt.id = measurements.target_id
		LEFT JOIN node_probe_targets npt ON npt.node_id = measurements.node_id AND npt.target_id = measurements.target_id
		WHERE measurements.` + dimension.filterColumn + ` = ?` + dimension.extraFilter + `
		  AND COALESCE(npt.enabled, 0) = 1
		GROUP BY bucket_ts, measurements.` + dimension.seriesColumn + `
	`
}

// latencyGridValuesFor renders one neutral bucketed grid: it lists the enabled
// series for the requested id, aggregates raw rounds plus rollups into
// fixed-width buckets, and emits a dense value per (bucket, series) pair.
func latencyGridValuesFor(
	ctx context.Context,
	s *sqliteLatencyQueries,
	id string,
	window latencyWindow,
	dimension latencyGridDimension,
) ([]latencyGridPointValue, error) {
	gridWindow, ok := resolveLatencyGridWindow(window.Name)
	if !ok {
		return nil, nil
	}
	if extendedHistoryWindow(gridWindow) {
		return latencyGridSampleValuesFor(ctx, s, id, gridWindow, dimension)
	}
	series, err := s.latencyGridSeries(ctx, dimension.seriesQuery, id)
	if err != nil {
		return nil, err
	}
	if len(series) == 0 {
		return []latencyGridPointValue{}, nil
	}

	start, end, stepSeconds := latencyGridBounds(gridWindow)
	rangeEnd := end.Add(gridWindow.Step).Unix()
	buckets := make(map[string]map[int64]*latencyGridBucket, len(series))
	queryArgs := []any{id, start.Unix(), rangeEnd}
	if dimension.sourceFilter != "" {
		queryArgs = append(queryArgs, id)
	}
	queryArgs = append(queryArgs, id, start.Unix(), rangeEnd)
	if dimension.sourceFilter != "" {
		queryArgs = append(queryArgs, id)
	}
	queryArgs = append(queryArgs, stepSeconds, stepSeconds, id)
	rows, err := s.db.QueryContext(ctx, latencyGridQuery(dimension), queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var bucketTS int64
		var seriesID string
		var median, avg, loss sql.NullFloat64
		if err := rows.Scan(&bucketTS, &seriesID, &median, &avg, &loss); err != nil {
			return nil, err
		}
		if bucketTS < start.Unix() || bucketTS > end.Unix() {
			continue
		}
		if buckets[seriesID] == nil {
			buckets[seriesID] = map[int64]*latencyGridBucket{}
		}
		if buckets[seriesID][bucketTS] == nil {
			buckets[seriesID][bucketTS] = &latencyGridBucket{}
		}
		buckets[seriesID][bucketTS].add(median, avg, nullFloatOr(loss, 0))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	points := make([]latencyGridPointValue, 0, gridWindow.Samples*len(series))
	for index := 0; index < gridWindow.Samples; index++ {
		bucketTS := start.Add(time.Duration(index) * gridWindow.Step).Unix()
		ts := time.Unix(bucketTS, 0).UTC().Format(time.RFC3339)
		for _, item := range series {
			var median, avg *float64
			loss := float64(0)
			if bucket := buckets[item.ID][bucketTS]; bucket != nil {
				median = bucket.medianPtr()
				avg = bucket.avgPtr()
				loss = bucket.lossPercent()
			}
			points = append(points, latencyGridPointValue{
				TS: ts, Series: item, MedianMS: median, AvgMS: avg, LossPercent: loss,
			})
		}
	}
	return points, nil
}

func latencyGridPointsFor[T any](ctx context.Context, s *sqliteLatencyQueries, id string, window latencyWindow, dimension latencyGridDimension, convert func(latencyGridPointValue) T) ([]T, error) {
	values, err := latencyGridValuesFor(ctx, s, id, window, dimension)
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, nil
	}
	points := make([]T, 0, len(values))
	for _, value := range values {
		points = append(points, convert(value))
	}
	return points, nil
}

func (s *sqliteLatencyQueries) latencyGridPoints(ctx context.Context, nodeID string, window latencyWindow) ([]LatencyPoint, error) {
	return latencyGridPointsFor(ctx, s, nodeID, window, latencyGridByNode, latencyPointFromGridValue)
}

func latencyPointFromGridValue(point latencyGridPointValue) LatencyPoint {
	return LatencyPoint{TS: point.TS, TargetID: point.Series.ID, TargetName: point.Series.Name, MedianMS: point.MedianMS, AvgMS: point.AvgMS, LossPercent: point.LossPercent}
}

func (s *sqliteLatencyQueries) latencyGridSeries(ctx context.Context, query, id string) ([]latencyGridTarget, error) {
	rows, err := s.db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var series []latencyGridTarget
	for rows.Next() {
		var item latencyGridTarget
		var order int
		if err := rows.Scan(&item.ID, &item.Name, &order); err != nil {
			return nil, err
		}
		series = append(series, item)
	}
	return series, rows.Err()
}

func latencyGridBounds(window latencyWindow) (time.Time, time.Time, int64) {
	step := window.Step
	if step <= 0 {
		step = time.Minute
	}
	stepSeconds := int64(step.Seconds())
	if stepSeconds <= 0 {
		stepSeconds = 1
	}
	endUnix := (time.Now().UTC().Unix() / stepSeconds) * stepSeconds
	end := time.Unix(endUnix, 0).UTC()
	start := end.Add(-time.Duration(window.Samples-1) * step)
	return start, end, stepSeconds
}

func (s *sqliteLatencyQueries) statePoints(ctx context.Context, nodeID string, window latencyWindow) ([]StatePoint, error) {
	if window.Samples <= 0 {
		return []StatePoint{}, nil
	}
	if !useHistoricalStateSampling(window) {
		return s.aggregatedStatePoints(ctx, nodeID, window)
	}
	start, _, stepSeconds := latencyGridBounds(window)
	rows, err := s.db.QueryContext(ctx, history.StateLatestGridQuery,
		start.Unix(), stepSeconds, window.Samples,
		nodeID, stepSeconds,
		nodeID, stepSeconds,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStateHistoryRows(rows)
}

func useHistoricalStateSampling(window latencyWindow) bool {
	return window.Name == "1d" || window.Name == "7d" || window.Name == "30d"
}

func (s *sqliteLatencyQueries) aggregatedStatePoints(ctx context.Context, nodeID string, window latencyWindow) ([]StatePoint, error) {
	since := time.Now().UTC().Add(-time.Duration(window.Samples) * window.Step).Unix()
	stepSeconds := int64(window.Step.Seconds())
	if stepSeconds <= 0 {
		stepSeconds = 1
	}
	query := `WITH measurements AS (` + history.StateSourceQuery + `)
		SELECT (ts / ?) * ? AS bucket_ts, ` + history.StateAverageSelect + `
		FROM measurements
		GROUP BY bucket_ts
		ORDER BY bucket_ts ASC`
	rows, err := s.db.QueryContext(ctx, query, nodeID, since, nodeID, since, stepSeconds, stepSeconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStateHistoryRows(rows)
}

func scanStateHistoryRows(rows *sql.Rows) ([]StatePoint, error) {
	var points []StatePoint
	for rows.Next() {
		point, err := scanStateHistoryPoint(rows)
		if err != nil {
			return nil, err
		}
		points = append(points, point)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return points, nil
}
