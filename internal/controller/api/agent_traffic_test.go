package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAgentStateSamplesDrivePublicSummaryAndMonthlyTrafficDeltas(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	postState := func(ts int64, inTotal, outTotal int64, cpu float64) {
		t.Helper()
		body := map[string]any{
			"ts":                   ts,
			"cpu_percent":          cpu,
			"load1":                0.42,
			"load5":                0.35,
			"load15":               0.28,
			"memory_used_bytes":    int64(3 * 1024 * 1024 * 1024),
			"memory_total_bytes":   int64(8 * 1024 * 1024 * 1024),
			"swap_used_bytes":      int64(512 * 1024 * 1024),
			"swap_total_bytes":     int64(2 * 1024 * 1024 * 1024),
			"disk_used_bytes":      int64(40 * 1024 * 1024 * 1024),
			"disk_total_bytes":     int64(160 * 1024 * 1024 * 1024),
			"net_in_total_bytes":   inTotal,
			"net_out_total_bytes":  outTotal,
			"net_in_speed_bps":     2048.5,
			"net_out_speed_bps":    1024.25,
			"process_count":        88,
			"tcp_connection_count": 34,
			"udp_connection_count": 12,
			"uptime_seconds":       int64(3600),
		}
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal state body: %v", err)
		}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/state", bytes.NewReader(payload))
		request.Header.Set("X-Node-ID", "example-node-a")
		request.Header.Set("Authorization", "Bearer test-agent-token")
		request.Header.Set("Content-Type", "application/json")
		NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
		}
	}

	ts := time.Now().UTC().Truncate(time.Second).Unix()
	postState(ts, 1_000_000, 2_000_000, 12.5)
	postState(ts+200, 1_400_000, 2_600_000, 22.5)

	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(summary.Nodes))
	}
	node := summary.Nodes[0]
	if node.Status != "online" || node.CPUPercent == nil || *node.CPUPercent != 22.5 || node.NetInSpeedBps == nil || *node.NetInSpeedBps != 2048.5 || node.NetOutSpeedBps == nil || *node.NetOutSpeedBps != 1024.25 {
		t.Fatalf("summary node after state = %+v, want latest state values", node)
	}
	if node.MonthlyBillableBytes == nil || *node.MonthlyBillableBytes != 1_000_000 {
		t.Fatalf("monthly billable = %v, want second sample delta in+out = 1000000", node.MonthlyBillableBytes)
	}
	state, err := store.NodeState(ctx, "example-node-a", latencyWindow{Name: "1h", Samples: 36, Step: 2 * time.Minute})
	if err != nil {
		t.Fatalf("node state: %v", err)
	}
	if len(state.Points) != 2 {
		t.Fatalf("state points = %d, want 2", len(state.Points))
	}
	latest := state.Points[1]
	if latest.Load1 == nil || *latest.Load1 != 0.42 || latest.Load5 == nil || *latest.Load5 != 0.35 || latest.Load15 == nil || *latest.Load15 != 0.28 {
		t.Fatalf("load averages = %+v, want persisted load1/load5/load15", latest)
	}
	if latest.SwapUsedBytes == nil || *latest.SwapUsedBytes != float64(512*1024*1024) || latest.SwapTotalBytes == nil || *latest.SwapTotalBytes != float64(2*1024*1024*1024) {
		t.Fatalf("swap fields = %+v, want persisted swap usage", latest)
	}
	if latest.ProcessCount == nil || *latest.ProcessCount != 88 || latest.TCPConnectionCount == nil || *latest.TCPConnectionCount != 34 || latest.UDPConnectionCount == nil || *latest.UDPConnectionCount != 12 {
		t.Fatalf("process/tcp/udp counts = %+v, want persisted process, tcp and udp connection counts", latest)
	}
	var samples int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_samples`).Scan(&samples); err != nil {
		t.Fatalf("count state samples: %v", err)
	}
	if samples != 2 {
		t.Fatalf("state samples = %d, want 2", samples)
	}
}
func TestLifetimeTrafficContinuesAcrossNetworkCounterReset(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	state := AgentStateRequest{
		CPUPercent:       1,
		MemoryUsedBytes:  1,
		MemoryTotalBytes: 2,
		DiskUsedBytes:    1,
		DiskTotalBytes:   2,
		NetInSpeedBps:    1,
		NetOutSpeedBps:   1,
		UptimeSeconds:    1,
	}
	post := func(offset time.Duration, inTotal, outTotal int64) {
		t.Helper()
		state.TS = now.Add(offset).Unix()
		state.NetInTotalBytes = inTotal
		state.NetOutTotalBytes = outTotal
		if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
			t.Fatalf("insert state at %s: %v", offset, err)
		}
	}

	post(0, 1_000, 2_000)
	post(time.Second, 1_500, 2_600)
	post(2*time.Second, 100, 200)
	post(3*time.Second, 300, 500)

	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(summary.Nodes))
	}
	node := summary.Nodes[0]
	if node.NetInLifetimeBytes == nil || *node.NetInLifetimeBytes != 1_800 {
		t.Fatalf("lifetime receive = %v, want 1800 after counter reset", node.NetInLifetimeBytes)
	}
	if node.NetOutLifetimeBytes == nil || *node.NetOutLifetimeBytes != 3_100 {
		t.Fatalf("lifetime send = %v, want 3100 after counter reset", node.NetOutLifetimeBytes)
	}
	if node.NetInTotalBytes == nil || *node.NetInTotalBytes != 300 || node.NetOutTotalBytes == nil || *node.NetOutTotalBytes != 500 {
		t.Fatalf("raw network totals = in:%v out:%v, want latest counters 300/500", node.NetInTotalBytes, node.NetOutTotalBytes)
	}
	var monthlyIn, monthlyOut int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT in_bytes, out_bytes FROM traffic_monthly WHERE node_id = 'example-node-a'
	`).Scan(&monthlyIn, &monthlyOut); err != nil {
		t.Fatalf("query monthly traffic: %v", err)
	}
	if monthlyIn != 700 || monthlyOut != 900 {
		t.Fatalf("monthly traffic = %d/%d, want legacy reset semantics 700/900", monthlyIn, monthlyOut)
	}
}
func TestLifetimeTrafficIgnoresEqualTimestampAndInvalidCounters(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	valid := true
	invalid := false
	post := func(ts int64, inTotal, outTotal int64, totalsValid *bool) {
		t.Helper()
		state := AgentStateRequest{
			TS:               ts,
			CPUPercent:       1,
			MemoryUsedBytes:  1,
			MemoryTotalBytes: 2,
			DiskUsedBytes:    1,
			DiskTotalBytes:   2,
			NetInTotalBytes:  inTotal,
			NetOutTotalBytes: outTotal,
			NetInSpeedBps:    1,
			NetOutSpeedBps:   1,
			NetTotalsValid:   totalsValid,
			UptimeSeconds:    1,
		}
		if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
			t.Fatalf("insert state at %d: %v", ts, err)
		}
	}

	post(now.Unix(), 1_000, 2_000, &valid)
	post(now.Unix(), 9_000, 9_000, &valid)
	post(now.Add(time.Second).Unix(), 50, 60, &invalid)
	post(now.Add(2*time.Second).Unix(), 1_100, 2_200, &valid)

	var lifetimeIn, lifetimeOut, previousIn, previousOut, lastSampleTS int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT in_bytes, out_bytes, last_in_total_bytes, last_out_total_bytes, last_sample_ts
		FROM traffic_lifetime WHERE node_id = 'example-node-a'
	`).Scan(&lifetimeIn, &lifetimeOut, &previousIn, &previousOut, &lastSampleTS); err != nil {
		t.Fatalf("query lifetime traffic: %v", err)
	}
	if lifetimeIn != 1_100 || lifetimeOut != 2_200 || previousIn != 1_100 || previousOut != 2_200 || lastSampleTS != now.Add(2*time.Second).Unix() {
		t.Fatalf("lifetime state = %d/%d baseline=%d/%d ts=%d, want 1100/2200 with only the final valid newer sample applied", lifetimeIn, lifetimeOut, previousIn, previousOut, lastSampleTS)
	}
	var invalidIn, invalidOut sql.NullInt64
	if err := store.db.QueryRowContext(ctx, `
		SELECT net_in_total_bytes, net_out_total_bytes
		FROM state_samples WHERE ts = ?
	`, now.Add(time.Second).Unix()).Scan(&invalidIn, &invalidOut); err != nil {
		t.Fatalf("query invalid state sample: %v", err)
	}
	if invalidIn.Valid || invalidOut.Valid {
		t.Fatalf("invalid totals persisted as real counters: in=%v out=%v", invalidIn, invalidOut)
	}
}
func TestLifetimeTrafficBackfillStartsFromLatestRawCounters(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	for index, totals := range [][2]int64{{1_000, 2_000}, {1_500, 2_600}, {100, 200}, {300, 500}} {
		state := AgentStateRequest{
			TS:               now.Add(time.Duration(index) * time.Second).Unix(),
			CPUPercent:       1,
			MemoryUsedBytes:  1,
			MemoryTotalBytes: 2,
			DiskUsedBytes:    1,
			DiskTotalBytes:   2,
			NetInTotalBytes:  totals[0],
			NetOutTotalBytes: totals[1],
			NetInSpeedBps:    1,
			NetOutSpeedBps:   1,
			UptimeSeconds:    1,
		}
		if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
			t.Fatalf("insert historical state %d: %v", index, err)
		}
	}
	invalid := false
	if err := store.InsertAgentState(ctx, "example-node-a", AgentStateRequest{
		TS:               now.Add(4 * time.Second).Unix(),
		CPUPercent:       1,
		MemoryUsedBytes:  1,
		MemoryTotalBytes: 2,
		DiskUsedBytes:    1,
		DiskTotalBytes:   2,
		NetInTotalBytes:  9_999,
		NetOutTotalBytes: 9_999,
		NetInSpeedBps:    1,
		NetOutSpeedBps:   1,
		NetTotalsValid:   &invalid,
		UptimeSeconds:    1,
	}); err != nil {
		t.Fatalf("insert latest invalid state: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TABLE traffic_lifetime`); err != nil {
		t.Fatalf("drop lifetime table to simulate an existing database: %v", err)
	}
	if err := store.ensureSchema(ctx); err != nil {
		t.Fatalf("migrate existing database: %v", err)
	}
	if err := store.ensureSchema(ctx); err != nil {
		t.Fatalf("rerun lifetime migration: %v", err)
	}

	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	node := summary.Nodes[0]
	if node.NetInLifetimeBytes == nil || *node.NetInLifetimeBytes != 300 || node.NetOutLifetimeBytes == nil || *node.NetOutLifetimeBytes != 500 {
		t.Fatalf("backfilled lifetime totals = in:%v out:%v, want latest raw counters 300/500", node.NetInLifetimeBytes, node.NetOutLifetimeBytes)
	}
}
func TestSummaryLeavesLifetimeTrafficUnknownBeforeFirstValidSample(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(summary.Nodes))
	}
	if summary.Nodes[0].NetInLifetimeBytes != nil || summary.Nodes[0].NetOutLifetimeBytes != nil {
		t.Fatalf("lifetime totals before first valid sample = in:%v out:%v, want unknown", summary.Nodes[0].NetInLifetimeBytes, summary.Nodes[0].NetOutLifetimeBytes)
	}
}
func TestConcurrentDuplicateStateDoesNotDoubleCountLifetimeTraffic(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	state := AgentStateRequest{
		TS:               now.Unix(),
		CPUPercent:       1,
		MemoryUsedBytes:  1,
		MemoryTotalBytes: 2,
		DiskUsedBytes:    1,
		DiskTotalBytes:   2,
		NetInTotalBytes:  1_000,
		NetOutTotalBytes: 2_000,
		NetInSpeedBps:    1,
		NetOutSpeedBps:   1,
		UptimeSeconds:    1,
	}
	if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
		t.Fatalf("insert baseline state: %v", err)
	}
	state.TS = now.Add(time.Minute).Unix()
	state.SampleID = "same-lifetime-sample"
	state.NetInTotalBytes = 1_500
	state.NetOutTotalBytes = 2_600

	const workers = 16
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.InsertAgentState(ctx, "example-node-a", state)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("insert concurrent duplicate: %v", err)
		}
	}

	var lifetimeIn, lifetimeOut int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT in_bytes, out_bytes FROM traffic_lifetime WHERE node_id = 'example-node-a'
	`).Scan(&lifetimeIn, &lifetimeOut); err != nil {
		t.Fatalf("query lifetime traffic: %v", err)
	}
	if lifetimeIn != 1_500 || lifetimeOut != 2_600 {
		t.Fatalf("lifetime traffic = %d/%d, want one application of concurrent duplicate", lifetimeIn, lifetimeOut)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM state_samples WHERE node_id = 'example-node-a' AND sample_id = 'same-lifetime-sample'
	`).Scan(&count); err != nil {
		t.Fatalf("count duplicate samples: %v", err)
	}
	if count != 1 {
		t.Fatalf("duplicate state rows = %d, want 1", count)
	}
}
func TestLifetimeTrafficSaturatesBeforeSQLiteIntegerOverflow(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	for index, totals := range [][2]int64{
		{math.MaxInt64 - 1, math.MaxInt64 - 1},
		{math.MaxInt64, math.MaxInt64},
		{0, 0},
		{math.MaxInt64, math.MaxInt64},
	} {
		state := AgentStateRequest{
			TS:               now.Add(time.Duration(index) * time.Second).Unix(),
			CPUPercent:       1,
			MemoryUsedBytes:  1,
			MemoryTotalBytes: 2,
			DiskUsedBytes:    1,
			DiskTotalBytes:   2,
			NetInTotalBytes:  totals[0],
			NetOutTotalBytes: totals[1],
			NetInSpeedBps:    1,
			NetOutSpeedBps:   1,
			UptimeSeconds:    1,
		}
		if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
			t.Fatalf("insert state %d: %v", index, err)
		}
	}

	var inBytes, outBytes int64
	var inType, outType string
	if err := store.db.QueryRowContext(ctx, `
		SELECT in_bytes, out_bytes, typeof(in_bytes), typeof(out_bytes)
		FROM traffic_lifetime WHERE node_id = 'example-node-a'
	`).Scan(&inBytes, &outBytes, &inType, &outType); err != nil {
		t.Fatalf("query lifetime traffic: %v", err)
	}
	if inBytes != math.MaxInt64 || outBytes != math.MaxInt64 || inType != "integer" || outType != "integer" {
		t.Fatalf("lifetime totals = %d/%d (%s/%s), want saturated int64 values", inBytes, outBytes, inType, outType)
	}
	if _, err := store.Summary(ctx); err != nil {
		t.Fatalf("summary after saturated lifetime totals: %v", err)
	}
}
func TestAgentStateRejectsLargeClockSkewAndIgnoresOutOfOrderTrafficBaseline(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	postState := func(ts int64, inTotal, outTotal int64, want int) {
		t.Helper()
		body := map[string]any{
			"ts":                  ts,
			"cpu_percent":         12.5,
			"memory_used_bytes":   int64(3 * 1024 * 1024 * 1024),
			"memory_total_bytes":  int64(8 * 1024 * 1024 * 1024),
			"disk_used_bytes":     int64(40 * 1024 * 1024 * 1024),
			"disk_total_bytes":    int64(160 * 1024 * 1024 * 1024),
			"net_in_total_bytes":  inTotal,
			"net_out_total_bytes": outTotal,
			"net_in_speed_bps":    128.0,
			"net_out_speed_bps":   256.0,
			"uptime_seconds":      int64(3600),
		}
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal state body: %v", err)
		}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/state", bytes.NewReader(payload))
		request.Header.Set("X-Node-ID", "example-node-a")
		request.Header.Set("Authorization", "Bearer test-agent-token")
		request.Header.Set("Content-Type", "application/json")
		NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
		if recorder.Code != want {
			t.Fatalf("state status = %d, want %d; body=%s", recorder.Code, want, recorder.Body.String())
		}
	}

	now := time.Now().UTC().Truncate(time.Second)
	postState(now.Add(10*time.Minute).Unix(), 1_000, 1_000, http.StatusBadRequest)
	postState(now.Unix(), 1_000, 1_000, http.StatusAccepted)
	postState(now.Add(100*time.Second).Unix(), 2_000, 2_000, http.StatusAccepted)
	postState(now.Add(50*time.Second).Unix(), 100, 100, http.StatusAccepted)
	postState(now.Add(101*time.Second).Unix(), 2_100, 2_100, http.StatusAccepted)

	var billable int64
	if err := store.db.QueryRowContext(ctx, `SELECT billable_bytes FROM traffic_monthly WHERE node_id = 'example-node-a'`).Scan(&billable); err != nil {
		t.Fatalf("query monthly billable: %v", err)
	}
	if billable != 2200 {
		t.Fatalf("billable bytes = %d, want 2200 with out-of-order sample ignored as baseline", billable)
	}
	var lifetimeIn, lifetimeOut int64
	if err := store.db.QueryRowContext(ctx, `SELECT in_bytes, out_bytes FROM traffic_lifetime WHERE node_id = 'example-node-a'`).Scan(&lifetimeIn, &lifetimeOut); err != nil {
		t.Fatalf("query lifetime traffic: %v", err)
	}
	if lifetimeIn != 2_100 || lifetimeOut != 2_100 {
		t.Fatalf("lifetime traffic = %d/%d, want 2100/2100 with out-of-order sample ignored as baseline", lifetimeIn, lifetimeOut)
	}
}
func TestTrafficLastSampleMigrationBackfillsLatestStateTimestamp(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	now := time.Now().Truncate(time.Second)
	for _, ts := range []int64{now.Add(-time.Minute).Unix(), now.Unix()} {
		if _, err := store.db.ExecContext(ctx, `INSERT INTO state_samples (node_id, ts, cpu_percent) VALUES ('example-node-a', ?, 10)`, ts); err != nil {
			t.Fatalf("insert historical state sample: %v", err)
		}
	}
	month := billingPeriodKey(now, 1)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO traffic_monthly (
			node_id, month, in_bytes, out_bytes, billable_bytes,
			last_in_total_bytes, last_out_total_bytes, last_sample_ts, updated_at
		) VALUES ('example-node-a', ?, 250, 250, 500, 1000, 1000, NULL, ?)
	`, month, now.Add(-2*time.Minute).Unix()); err != nil {
		t.Fatalf("insert legacy traffic baseline: %v", err)
	}
	if err := store.ensureSchema(ctx); err != nil {
		t.Fatalf("rerun schema migration: %v", err)
	}
	var lastSampleTS int64
	if err := store.db.QueryRowContext(ctx, `SELECT last_sample_ts FROM traffic_monthly WHERE node_id = 'example-node-a' AND month = ?`, month).Scan(&lastSampleTS); err != nil {
		t.Fatalf("read migrated last sample timestamp: %v", err)
	}
	if lastSampleTS != now.Unix() {
		t.Fatalf("last_sample_ts = %d, want latest state timestamp %d", lastSampleTS, now.Unix())
	}
	baseState := AgentStateRequest{
		CPUPercent:       10,
		MemoryUsedBytes:  1,
		MemoryTotalBytes: 2,
		DiskUsedBytes:    1,
		DiskTotalBytes:   2,
		NetInTotalBytes:  100,
		NetOutTotalBytes: 100,
		NetInSpeedBps:    1,
		NetOutSpeedBps:   1,
		UptimeSeconds:    1,
	}
	baseState.TS = now.Add(-30 * time.Second).Unix()
	if err := store.InsertAgentState(ctx, "example-node-a", baseState); err != nil {
		t.Fatalf("insert delayed state: %v", err)
	}
	baseState.TS = now.Add(time.Second).Unix()
	baseState.NetInTotalBytes = 1100
	baseState.NetOutTotalBytes = 1100
	if err := store.InsertAgentState(ctx, "example-node-a", baseState); err != nil {
		t.Fatalf("insert current state: %v", err)
	}
	var billable int64
	if err := store.db.QueryRowContext(ctx, `SELECT billable_bytes FROM traffic_monthly WHERE node_id = 'example-node-a' AND month = ?`, month).Scan(&billable); err != nil {
		t.Fatalf("read billable traffic: %v", err)
	}
	if billable != 700 {
		t.Fatalf("billable bytes = %d, want 700 after ignoring delayed baseline", billable)
	}
}
func TestBillingTrafficModeAndResetPeriodHelpers(t *testing.T) {
	if got := billableTrafficDelta("in", 100, 400); got != 100 {
		t.Fatalf("in billable = %d, want 100", got)
	}
	if got := billableTrafficDelta("out", 100, 400); got != 400 {
		t.Fatalf("out billable = %d, want 400", got)
	}
	if got := billableTrafficDelta("max", 100, 400); got != 400 {
		t.Fatalf("max billable = %d, want 400", got)
	}
	if got := billableTrafficDelta("both", 100, 400); got != 500 {
		t.Fatalf("both billable = %d, want 500", got)
	}
	if got := billingPeriodKey(time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), 15); got != "2026-06" {
		t.Fatalf("billing period before reset = %s, want 2026-06", got)
	}
	if got := billingPeriodKey(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), 15); got != "2026-07" {
		t.Fatalf("billing period on reset = %s, want 2026-07", got)
	}
	period := billingPeriodFor(time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), 15)
	if period.Key != "2026-06" || period.StartDate != "2026-06-15" || period.EndDate != "2026-07-14" {
		t.Fatalf("billing period window = %+v, want 2026-06 2026-06-15..2026-07-14", period)
	}
	clamped := billingPeriodFor(time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC), 31)
	if clamped.Key != "2026-02" || clamped.StartDate != "2026-02-28" || clamped.EndDate != "2026-03-30" {
		t.Fatalf("clamped billing period window = %+v, want reset day clamped to month end", clamped)
	}
}
func TestAgentStateRejectsNegativeUDPConnectionCount(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	body := map[string]any{
		"ts":                      time.Now().UTC().Unix(),
		"cpu_percent":             1,
		"memory_used_bytes":       1,
		"memory_total_bytes":      2,
		"disk_used_bytes":         1,
		"disk_total_bytes":        2,
		"net_in_total_bytes":      1,
		"net_out_total_bytes":     1,
		"net_in_speed_bps":        1,
		"net_out_speed_bps":       1,
		"udp_connection_count":    -1,
		"connection_counts_valid": true,
		"uptime_seconds":          1,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/state", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for negative UDP connection count; body=%s", recorder.Code, recorder.Body.String())
	}
	var samples int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_samples`).Scan(&samples); err != nil {
		t.Fatalf("count state samples: %v", err)
	}
	if samples != 0 {
		t.Fatalf("negative UDP connection count persisted %d samples", samples)
	}
}
func TestAgentStateLegacyPayloadKeepsExtraMetricsNull(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	body := map[string]any{
		"ts":                  time.Now().UTC().Unix(),
		"cpu_percent":         18.75,
		"memory_used_bytes":   int64(3 * 1024 * 1024 * 1024),
		"memory_total_bytes":  int64(8 * 1024 * 1024 * 1024),
		"disk_used_bytes":     int64(40 * 1024 * 1024 * 1024),
		"disk_total_bytes":    int64(160 * 1024 * 1024 * 1024),
		"net_in_total_bytes":  int64(1_000_000),
		"net_out_total_bytes": int64(2_000_000),
		"net_in_speed_bps":    2048.5,
		"net_out_speed_bps":   1024.25,
		"uptime_seconds":      int64(3600),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal state body: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/state", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 for legacy state payload; body=%s", recorder.Code, recorder.Body.String())
	}

	state, err := store.NodeState(ctx, "example-node-a", latencyWindow{Name: "1h", Samples: 36, Step: 2 * time.Minute})
	if err != nil {
		t.Fatalf("node state: %v", err)
	}
	if len(state.Points) != 1 {
		t.Fatalf("state points = %d, want 1", len(state.Points))
	}
	point := state.Points[0]
	if point.CPUPercent == nil || *point.CPUPercent != 18.75 {
		t.Fatalf("cpu percent = %v, want persisted legacy metric", point.CPUPercent)
	}
	if point.Load1 != nil || point.Load5 != nil || point.Load15 != nil || point.SwapUsedBytes != nil || point.SwapTotalBytes != nil || point.ProcessCount != nil || point.TCPConnectionCount != nil || point.UDPConnectionCount != nil {
		t.Fatalf("legacy payload should keep extra metrics null, got %+v", point)
	}
}
func TestAgentStateSchemaMigratesExtraMetricColumnsAsNullable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "zeno.db")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite db: %v", err)
	}
	if _, err := legacyDB.Exec(`
		CREATE TABLE state_samples (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL,
			ts INTEGER NOT NULL,
			cpu_percent REAL,
			memory_used_bytes INTEGER,
			memory_total_bytes INTEGER,
			disk_used_bytes INTEGER,
			disk_total_bytes INTEGER,
			net_in_total_bytes INTEGER,
			net_out_total_bytes INTEGER,
			net_in_speed_bps REAL,
			net_out_speed_bps REAL,
			uptime_seconds INTEGER
		);
	`); err != nil {
		_ = legacyDB.Close()
		t.Fatalf("create legacy state_samples table: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open migrated sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	columns := map[string]bool{}
	rows, err := store.db.QueryContext(ctx, `PRAGMA table_info(state_samples)`)
	if err != nil {
		t.Fatalf("query migrated state_samples schema: %v", err)
	}
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			t.Fatalf("scan schema row: %v", err)
		}
		columns[name] = true
		if (name == "load1" || name == "load5" || name == "load15" || name == "swap_used_bytes" || name == "swap_total_bytes" || name == "process_count" || name == "tcp_connection_count" || name == "udp_connection_count") && notNull != 0 {
			_ = rows.Close()
			t.Fatalf("migrated column %s should be nullable", name)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close schema rows: %v", err)
	}
	for _, column := range []string{"load1", "load5", "load15", "swap_used_bytes", "swap_total_bytes", "process_count", "tcp_connection_count", "udp_connection_count"} {
		if !columns[column] {
			t.Fatalf("migrated state_samples missing column %s", column)
		}
	}
}
