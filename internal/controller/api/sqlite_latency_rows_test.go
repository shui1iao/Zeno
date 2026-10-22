package api

import (
	"context"
	"testing"
	"time"
)

func TestRawLatencyRowsPreserveDimensionAndEmptyShapes(t *testing.T) {
	t.Parallel()
	store := newLatencyGridTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, display_order, disabled, created_at, updated_at)
		VALUES ('node-a', 'Node A', 'hash-a', 'online', 1, 0, ?, ?);
		INSERT INTO probe_targets (id, name, type, address, port, count, timeout_ms, interval_sec, display_order, created_at, updated_at)
		VALUES ('target-a', 'Target A', 'tcp', '127.0.0.1', 443, 1, 1000, 30, 1, ?, ?);
		INSERT INTO node_probe_targets (node_id, target_id, enabled) VALUES ('node-a', 'target-a', 1);
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, median_ms, avg_ms)
		VALUES ('node-a', 'target-a', ?, 'tcp', 1, 1, 2.5, NULL, 12.5);
	`, now.Unix(), now.Unix(), now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}

	window := latencyWindow{Name: "1h", Samples: 2, Step: 7 * time.Minute}
	nodePoints, err := store.latencyPoints(ctx, "node-a", window)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodePoints) != 1 || nodePoints[0].TS != now.Format(time.RFC3339) || nodePoints[0].TargetID != "target-a" || nodePoints[0].TargetName != "Target A" || nodePoints[0].MedianMS != nil || nodePoints[0].AvgMS == nil || *nodePoints[0].AvgMS != 12.5 || nodePoints[0].LossPercent != 2.5 {
		t.Fatalf("node points = %+v", nodePoints)
	}

	emptyNodePoints, err := store.latencyPoints(ctx, "missing-node", window)
	if err != nil || emptyNodePoints != nil {
		t.Fatalf("empty node points = (%v, %v), want (nil, nil)", emptyNodePoints, err)
	}
}
