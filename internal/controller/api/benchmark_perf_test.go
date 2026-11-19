package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkLatencyResponseMarshal30d(b *testing.B) {
	const (
		samples = 1440
		targets = 16
	)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	points := make([]LatencyPoint, 0, samples*targets)
	for sampleIndex := 0; sampleIndex < samples; sampleIndex++ {
		ts := start.Add(time.Duration(sampleIndex) * 30 * time.Minute)
		tsStr := ts.Format(time.RFC3339)
		for targetIndex := 0; targetIndex < targets; targetIndex++ {
			med := 12.345 + float64(targetIndex)
			avg := 13.456 + float64(targetIndex)
			points = append(points, LatencyPoint{
				TS:          tsStr,
				TSUnix:      ts.Unix(),
				TargetID:    fmt.Sprintf("target-%02d", targetIndex),
				TargetName:  fmt.Sprintf("Target %02d", targetIndex),
				MedianMS:    &med,
				AvgMS:       &avg,
				LossPercent: float64(sampleIndex % 5),
			})
		}
	}
	resp := LatencyResponse{NodeID: "bench-node", Range: "30d", Points: points}

	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		payload, err := json.Marshal(resp)
		if err != nil || len(payload) == 0 {
			b.Fatalf("marshal latency 30d: %v", err)
		}
	}
}

func BenchmarkStateResponseMarshal1d(b *testing.B) {
	window := latencyWindow{Name: "1d", Samples: 2880, Step: 30 * time.Second}
	pts := mockStatePoints(window)
	for i := range pts {
		if parsed, err := time.Parse(time.RFC3339, pts[i].TS); err == nil {
			pts[i].TSUnix = parsed.Unix()
		}
	}
	resp := StateResponse{NodeID: "bench-node", Range: "1d", Points: pts}

	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		payload, err := json.Marshal(resp)
		if err != nil || len(payload) == 0 {
			b.Fatalf("marshal state 1d: %v", err)
		}
	}
}

func BenchmarkSQLiteNodeLatency30dEndToEnd(b *testing.B) {
	ctx := context.Background()
	store, err := OpenSQLiteStore(filepath.Join(b.TempDir(), "zeno.db"))
	if err != nil {
		b.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	window, ok := resolveLatencyWindow("30d")
	if !ok {
		b.Fatal("resolve 30d window")
	}
	start, _, stepSeconds := latencyGridBounds(window)
	now := time.Now().UTC().Unix()

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatalf("begin tx: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, display_order, disabled, created_at, updated_at, last_seen_at)
		VALUES ('bench-node', 'Bench Node', 'hash', 'online', 'HK', 1, 0, ?, ?, ?)
	`, now, now, now); err != nil {
		b.Fatalf("insert node: %v", err)
	}
	const targetCount = 16
	for targetIndex := 0; targetIndex < targetCount; targetIndex++ {
		targetID := fmt.Sprintf("target-%02d", targetIndex)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO probe_targets (id, name, type, address, count, timeout_ms, interval_sec, display_order, created_at, updated_at)
			VALUES (?, ?, 'tcp', '192.0.2.1', 3, 1000, 30, ?, ?, ?);
			INSERT INTO node_probe_targets (node_id, target_id, enabled) VALUES ('bench-node', ?, 1);
		`, targetID, fmt.Sprintf("Target %02d", targetIndex), targetIndex, now, now, targetID); err != nil {
			b.Fatalf("insert target: %v", err)
		}
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO latency_history_rollups (node_id, target_id, bucket_start, median_sum, median_count, avg_sum, avg_count, loss_sum, loss_count)
		VALUES ('bench-node', ?, ?, ?, 10, ?, 10, ?, 10)
	`)
	if err != nil {
		b.Fatalf("prepare rollup insert: %v", err)
	}
	for sampleIndex := 0; sampleIndex < window.Samples; sampleIndex++ {
		bucketTS := start.Unix() + int64(sampleIndex)*stepSeconds
		for targetIndex := 0; targetIndex < targetCount; targetIndex++ {
			targetID := fmt.Sprintf("target-%02d", targetIndex)
			med := float64(100 + targetIndex*10 + sampleIndex%20)
			if _, err := stmt.ExecContext(ctx, targetID, bucketTS, med, med+5, float64(sampleIndex%3)); err != nil {
				b.Fatalf("insert rollup: %v", err)
			}
		}
	}
	_ = stmt.Close()
	if err := tx.Commit(); err != nil {
		b.Fatalf("commit tx: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		resp, err := store.NodeLatency(ctx, "bench-node", window)
		if err != nil {
			b.Fatalf("NodeLatency 30d: %v", err)
		}
		payload, err := json.Marshal(resp)
		if err != nil || len(payload) == 0 {
			b.Fatalf("marshal NodeLatency 30d: %v", err)
		}
	}
}

func BenchmarkWriteRawJSONForRequestGzip1dState(b *testing.B) {
	window := latencyWindow{Name: "1d", Samples: 2880, Step: 30 * time.Second}
	payload, err := json.Marshal(StateResponse{NodeID: "bench-node", Range: "1d", Points: mockStatePoints(window)})
	if err != nil {
		b.Fatalf("marshal state 1d: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/public/v1/nodes/bench-node/state?range=1d", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	b.ReportAllocs()
	b.ResetTimer()
	var compressedBytes int
	for iteration := 0; iteration < b.N; iteration++ {
		rec := httptest.NewRecorder()
		writeRawJSONForRequest(rec, req, http.StatusOK, payload)
		compressedBytes = rec.Body.Len()
	}
	b.ReportMetric(float64(compressedBytes), "gz_bytes")
	b.ReportMetric(float64(len(payload)), "raw_bytes")
}

