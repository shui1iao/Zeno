package api

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newLatencyGridTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// seedLatencyGridFixture creates two nodes (one disabled), three targets (one
// disabled for the primary node) and spreads probe rounds plus rollup rows
// across the requested grid window, including rows exactly on the first and last
// bucket boundaries and rows just outside the window on both sides.
func seedLatencyGridFixture(ctx context.Context, t *testing.T, store *SQLiteStore, window latencyWindow) {
	t.Helper()
	now := time.Now().UTC().Unix()
	start, end, step := latencyGridBounds(window)

	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, display_order, disabled, created_at, updated_at)
		VALUES ('node-a', 'Node A', 'hash-a', 'online', 1, 0, ?, ?),
		       ('node-b', 'Node B', 'hash-b', 'online', 2, 0, ?, ?),
		       ('node-off', 'Node Disabled', 'hash-c', 'online', 3, 1, ?, ?);
		INSERT INTO probe_targets (id, name, type, address, port, count, timeout_ms, interval_sec, display_order, created_at, updated_at)
		VALUES ('t-1', 'Target One', 'tcp', '127.0.0.1', 443, 1, 1000, 30, 1, ?, ?),
		       ('t-2', 'Target Two', 'tcp', '127.0.0.2', 443, 1, 1000, 30, 2, ?, ?),
		       ('t-off', 'Target Off', 'tcp', '127.0.0.3', 443, 1, 1000, 30, 3, ?, ?);
		INSERT INTO node_probe_targets (node_id, target_id, enabled) VALUES
		       ('node-a', 't-1', 1), ('node-a', 't-2', 1), ('node-a', 't-off', 0),
		       ('node-b', 't-1', 1), ('node-b', 't-2', 0),
		       ('node-off', 't-1', 1);
	`, now, now, now, now, now, now, now, now, now, now, now, now); err != nil {
		t.Fatalf("seed nodes and targets: %v", err)
	}

	insertRound := func(nodeID, targetID string, ts int64, median, avg any, loss float64) {
		t.Helper()
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, median_ms, avg_ms)
			VALUES (?, ?, ?, 'tcp', 1, 1, ?, ?, ?)
		`, nodeID, targetID, ts, loss, median, avg); err != nil {
			t.Fatalf("insert probe round %s/%s@%d: %v", nodeID, targetID, ts, err)
		}
	}
	insertRollup := func(nodeID, targetID string, bucket int64, medianSum float64, medianCount int, avgSum float64, avgCount int, lossSum float64, lossCount int) {
		t.Helper()
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO latency_history_rollups (node_id, target_id, bucket_start, median_sum, median_count, avg_sum, avg_count, loss_sum, loss_count)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, nodeID, targetID, bucket, medianSum, medianCount, avgSum, avgCount, lossSum, lossCount); err != nil {
			t.Fatalf("insert rollup %s/%s@%d: %v", nodeID, targetID, bucket, err)
		}
	}

	// First bucket boundary, inclusive.
	insertRound("node-a", "t-1", start.Unix(), 10.0, 12.0, 0)
	insertRound("node-a", "t-1", start.Unix()+1, 20.0, 24.0, 10)
	// NULL median/avg must fall through to the NULLIF/COALESCE handling.
	insertRound("node-a", "t-2", start.Unix(), nil, nil, 100)
	// avg NULL but median present exercises avgPtr's median fallback.
	insertRound("node-a", "t-2", start.Unix()+2, 30.0, nil, 5)
	// Mid-window bucket.
	mid := start.Add(time.Duration(window.Samples/2) * window.Step).Unix()
	insertRound("node-a", "t-1", mid, 40.0, 44.0, 1)
	insertRound("node-b", "t-1", mid, 50.0, 55.0, 2)
	// Last bucket boundary, inclusive.
	insertRound("node-a", "t-1", end.Unix(), 60.0, 66.0, 3)
	insertRound("node-b", "t-1", end.Unix()+step-1, 70.0, 77.0, 4)
	// Outside the window on both sides: must be excluded.
	insertRound("node-a", "t-1", start.Unix()-1, 999.0, 999.0, 99)
	insertRound("node-a", "t-1", end.Unix()+step, 888.0, 888.0, 88)
	// Rows on disabled node/target links must be filtered out.
	insertRound("node-a", "t-off", mid, 777.0, 777.0, 77)
	insertRound("node-off", "t-1", mid, 666.0, 666.0, 66)
	insertRound("node-b", "t-2", mid, 555.0, 555.0, 55)
	// Rollups share the same bucket as raw rounds so the UNION ALL aggregation is covered.
	insertRollup("node-a", "t-1", mid, 100.0, 2, 120.0, 2, 8.0, 2)
	insertRollup("node-b", "t-1", start.Unix(), 90.0, 1, 99.0, 1, 6.0, 1)
	insertRollup("node-a", "t-1", start.Unix()-int64(step), 999.0, 1, 999.0, 1, 99.0, 1)
}

func TestLatencyGridPointsStayDenseAcrossWindowsAndDimensions(t *testing.T) {
	ctx := context.Background()
	for _, rangeName := range []string{"1h", "7d"} {
		t.Run("node/"+rangeName, func(t *testing.T) {
			store := newLatencyGridTestStore(t)
			window, ok := resolveLatencyGridWindow(rangeName)
			if !ok {
				t.Fatalf("resolve grid window %q", rangeName)
			}
			seedLatencyGridFixture(ctx, t, store, window)

			got, err := store.latencyGridPoints(ctx, "node-a", window)
			if err != nil {
				t.Fatalf("latency grid: %v", err)
			}
			if len(got) != window.Samples*2 {
				t.Fatalf("point count = %d, want %d (samples × enabled targets)", len(got), window.Samples*2)
			}
			if got[0].TargetID != "t-1" || got[1].TargetID != "t-2" {
				t.Fatalf("first bucket target order = %s,%s, want t-1,t-2", got[0].TargetID, got[1].TargetID)
			}
		})

		t.Run("service/"+rangeName, func(t *testing.T) {
			store := newLatencyGridTestStore(t)
			window, ok := resolveLatencyGridWindow(rangeName)
			if !ok {
				t.Fatalf("resolve grid window %q", rangeName)
			}
			seedLatencyGridFixture(ctx, t, store, window)

			got, err := store.serviceLatencyGridPoints(ctx, "t-1", window)
			if err != nil {
				t.Fatalf("service latency grid: %v", err)
			}
			if len(got) != window.Samples*2 {
				t.Fatalf("point count = %d, want %d (samples × enabled nodes)", len(got), window.Samples*2)
			}
			if got[0].NodeID != "node-a" || got[1].NodeID != "node-b" {
				t.Fatalf("first bucket node order = %s,%s, want node-a,node-b", got[0].NodeID, got[1].NodeID)
			}
		})
	}
}

func TestLatencyGridPointsWindowBoundaryAndValues(t *testing.T) {
	ctx := context.Background()
	store := newLatencyGridTestStore(t)
	window, _ := resolveLatencyGridWindow("1h")
	seedLatencyGridFixture(ctx, t, store, window)
	start, end, step := latencyGridBounds(window)

	points, err := store.latencyGridPoints(ctx, "node-a", window)
	if err != nil {
		t.Fatalf("latency grid: %v", err)
	}

	byKey := map[string]LatencyPoint{}
	for _, point := range points {
		byKey[point.TargetID+"@"+point.TS] = point
		if point.TargetID == "t-off" {
			t.Fatalf("disabled target leaked into grid: %+v", point)
		}
	}
	tsOf := func(unix int64) string { return time.Unix(unix, 0).UTC().Format(time.RFC3339) }

	// First bucket: raw 10/20 median and 12/24 avg, loss 0/10.
	first := byKey["t-1@"+tsOf(start.Unix())]
	assertFloatPtr(t, "first median", first.MedianMS, 15)
	assertFloatPtr(t, "first avg", first.AvgMS, 18)
	assertFloat(t, "first loss", first.LossPercent, 5)

	// NULL median and avg collapse to nil while loss still averages.
	nullPoint := byKey["t-2@"+tsOf(start.Unix())]
	if nullPoint.MedianMS == nil {
		t.Fatalf("t-2 first bucket median should come from the non-null round")
	}
	assertFloatPtr(t, "t-2 median", nullPoint.MedianMS, 30)
	assertFloatPtr(t, "t-2 avg falls back to median", nullPoint.AvgMS, 30)
	assertFloat(t, "t-2 loss", nullPoint.LossPercent, 52.5)

	// Mid bucket mixes raw rounds with a rollup row.
	mid := start.Add(time.Duration(window.Samples/2) * window.Step).Unix()
	midPoint := byKey["t-1@"+tsOf(mid)]
	assertFloatPtr(t, "mid median", midPoint.MedianMS, 140.0/3.0)
	assertFloatPtr(t, "mid avg", midPoint.AvgMS, 164.0/3.0)
	assertFloat(t, "mid loss", midPoint.LossPercent, 3)

	// Last bucket boundary is inclusive and absorbs the sub-step row.
	last := byKey["t-1@"+tsOf(end.Unix())]
	assertFloatPtr(t, "last median", last.MedianMS, 60)
	assertFloatPtr(t, "last avg", last.AvgMS, 66)
	assertFloat(t, "last loss", last.LossPercent, 3)

	// Rows before start and at end+step are outside the window.
	for _, unix := range []int64{start.Unix() - int64(step), end.Unix() + int64(step)} {
		if point, ok := byKey["t-1@"+tsOf(unix)]; ok {
			t.Fatalf("out-of-window bucket %d present: %+v", unix, point)
		}
	}
	for _, point := range points {
		if point.MedianMS != nil && (*point.MedianMS > 900 || *point.MedianMS == 777) {
			t.Fatalf("excluded row leaked into grid: %+v", point)
		}
	}
}

func TestServiceLatencyGridPointsHonoursNodeDimension(t *testing.T) {
	ctx := context.Background()
	store := newLatencyGridTestStore(t)
	window, _ := resolveLatencyGridWindow("1h")
	seedLatencyGridFixture(ctx, t, store, window)
	start, _, _ := latencyGridBounds(window)

	points, err := store.serviceLatencyGridPoints(ctx, "t-1", window)
	if err != nil {
		t.Fatalf("service latency grid: %v", err)
	}

	seen := map[string]bool{}
	for _, point := range points {
		seen[point.NodeID] = true
		if point.NodeID == "node-off" {
			t.Fatalf("disabled node leaked into service grid: %+v", point)
		}
	}
	if !seen["node-a"] || !seen["node-b"] {
		t.Fatalf("expected node-a and node-b series, got %v", seen)
	}
	// Series order follows display_order, so node-a precedes node-b in every bucket.
	if points[0].NodeID != "node-a" || points[1].NodeID != "node-b" {
		t.Fatalf("series order = %s,%s, want node-a,node-b", points[0].NodeID, points[1].NodeID)
	}

	tsOf := func(unix int64) string { return time.Unix(unix, 0).UTC().Format(time.RFC3339) }
	var firstNodeB *ServiceLatencyPoint
	for index := range points {
		if points[index].NodeID == "node-b" && points[index].TS == tsOf(start.Unix()) {
			firstNodeB = &points[index]
			break
		}
	}
	if firstNodeB == nil {
		t.Fatal("missing node-b first bucket")
	}
	// node-b's first bucket comes only from the rollup row (90/1, 99/1, 6/1).
	assertFloatPtr(t, "node-b rollup median", firstNodeB.MedianMS, 90)
	assertFloatPtr(t, "node-b rollup avg", firstNodeB.AvgMS, 99)
	assertFloat(t, "node-b rollup loss", firstNodeB.LossPercent, 6)
}

func TestLatencyGridPointsEmptyResults(t *testing.T) {
	ctx := context.Background()
	store := newLatencyGridTestStore(t)
	window, _ := resolveLatencyGridWindow("1h")

	// No configured series at all: both dimensions return non-nil empty slices.
	points, err := store.latencyGridPoints(ctx, "missing-node", window)
	if err != nil {
		t.Fatalf("latency grid on empty db: %v", err)
	}
	if points == nil || len(points) != 0 {
		t.Fatalf("latency grid = %#v, want empty non-nil slice", points)
	}
	servicePoints, err := store.serviceLatencyGridPoints(ctx, "missing-target", window)
	if err != nil {
		t.Fatalf("service latency grid on empty db: %v", err)
	}
	if servicePoints == nil || len(servicePoints) != 0 {
		t.Fatalf("service latency grid = %#v, want empty non-nil slice", servicePoints)
	}

	// Series configured but no measurements: dense grid of empty buckets.
	seedLatencyGridFixture(ctx, t, store, window)
	if _, err := store.db.ExecContext(ctx, `DELETE FROM probe_rounds; DELETE FROM latency_history_rollups;`); err != nil {
		t.Fatalf("clear measurements: %v", err)
	}
	points, err = store.latencyGridPoints(ctx, "node-a", window)
	if err != nil {
		t.Fatalf("latency grid without measurements: %v", err)
	}
	if len(points) != window.Samples*2 {
		t.Fatalf("point count = %d, want %d", len(points), window.Samples*2)
	}
	for _, point := range points {
		if point.MedianMS != nil || point.AvgMS != nil || point.LossPercent != 0 {
			t.Fatalf("empty bucket carries data: %+v", point)
		}
	}

	// Unknown range names short-circuit to (nil, nil) on both dimensions.
	if points, err := store.latencyGridPoints(ctx, "node-a", latencyWindow{Name: "nope", Samples: 5, Step: time.Minute}); err != nil || points != nil {
		t.Fatalf("unknown window = (%#v, %v), want (nil, nil)", points, err)
	}
	if points, err := store.serviceLatencyGridPoints(ctx, "t-1", latencyWindow{Name: "nope", Samples: 5, Step: time.Minute}); err != nil || points != nil {
		t.Fatalf("unknown window = (%#v, %v), want (nil, nil)", points, err)
	}
}

func TestLatencyGridPointsPropagatesQueryErrors(t *testing.T) {
	ctx := context.Background()
	window, _ := resolveLatencyGridWindow("1h")

	// Series lookup failure: the whole schema is gone.
	seriesStore := newLatencyGridTestStore(t)
	seedLatencyGridFixture(ctx, t, seriesStore, window)
	if _, err := seriesStore.db.ExecContext(ctx, `DROP TABLE node_probe_targets;`); err != nil {
		t.Fatalf("drop node_probe_targets: %v", err)
	}
	if _, err := seriesStore.latencyGridPoints(ctx, "node-a", window); err == nil {
		t.Fatal("expected series lookup error for latency grid")
	}
	if _, err := seriesStore.serviceLatencyGridPoints(ctx, "t-1", window); err == nil {
		t.Fatal("expected series lookup error for service latency grid")
	}

	// Measurement query failure: series resolve fine, the aggregation source is gone.
	measurementStore := newLatencyGridTestStore(t)
	seedLatencyGridFixture(ctx, t, measurementStore, window)
	if _, err := measurementStore.db.ExecContext(ctx, `DROP TABLE latency_history_rollups;`); err != nil {
		t.Fatalf("drop latency_history_rollups: %v", err)
	}
	if _, err := measurementStore.latencyGridPoints(ctx, "node-a", window); err == nil {
		t.Fatal("expected measurement query error for latency grid")
	}
	if _, err := measurementStore.serviceLatencyGridPoints(ctx, "t-1", window); err == nil {
		t.Fatal("expected measurement query error for service latency grid")
	}

	// Closed database: both dimensions surface the driver error.
	closedStore, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	seedLatencyGridFixture(ctx, t, closedStore, window)
	if err := closedStore.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, err := closedStore.latencyGridPoints(ctx, "node-a", window); err == nil {
		t.Fatal("expected error from closed db on latency grid")
	}
	if _, err := closedStore.serviceLatencyGridPoints(ctx, "t-1", window); err == nil {
		t.Fatal("expected error from closed db on service latency grid")
	}
}

func TestLatencyGridQueryMatchesPreMergeSQL(t *testing.T) {
	nodeQuery := latencyGridQuery(latencyGridByNode)
	for _, want := range []string{
		"FROM probe_rounds WHERE node_id = ? AND ts >= ? AND ts < ?",
		"FROM latency_history_rollups WHERE node_id = ? AND bucket_start >= ? AND bucket_start < ?",
		"SELECT (measurements.ts / ?) * ? AS bucket_ts, measurements.target_id,",
		"WHERE measurements.node_id = ?",
		"GROUP BY bucket_ts, measurements.target_id",
	} {
		if !strings.Contains(nodeQuery, want) {
			t.Fatalf("node grid query missing %q\n%s", want, nodeQuery)
		}
	}
	if strings.Contains(nodeQuery, "JOIN nodes n") || strings.Contains(nodeQuery, "n.disabled = 0") {
		t.Fatalf("node grid query must not join nodes:\n%s", nodeQuery)
	}

	serviceQuery := latencyGridQuery(latencyGridByTarget)
	for _, want := range []string{
		"FROM probe_rounds WHERE target_id = ? AND ts >= ? AND ts < ?",
		"FROM latency_history_rollups WHERE target_id = ? AND bucket_start >= ? AND bucket_start < ?",
		"SELECT (measurements.ts / ?) * ? AS bucket_ts, measurements.node_id,",
		"JOIN nodes n ON n.id = measurements.node_id",
		"WHERE measurements.target_id = ?",
		"AND n.disabled = 0",
		"GROUP BY bucket_ts, measurements.node_id",
	} {
		if !strings.Contains(serviceQuery, want) {
			t.Fatalf("service grid query missing %q\n%s", want, serviceQuery)
		}
	}
	// Both dimensions keep the enabled-link join and the probe_targets join.
	for name, query := range map[string]string{"node": nodeQuery, "service": serviceQuery} {
		for _, want := range []string{
			"JOIN probe_targets pt ON pt.id = measurements.target_id",
			"LEFT JOIN node_probe_targets npt ON npt.node_id = measurements.node_id AND npt.target_id = measurements.target_id",
			"AND COALESCE(npt.enabled, 0) = 1",
		} {
			if !strings.Contains(query, want) {
				t.Fatalf("%s grid query missing %q", name, want)
			}
		}
	}
}

func assertFloatPtr(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %.6f", name, want)
	}
	assertFloat(t, name, *got, want)
}

func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if diff := got - want; diff > 0.000001 || diff < -0.000001 {
		t.Fatalf("%s = %.6f, want %.6f", name, got, want)
	}
}
