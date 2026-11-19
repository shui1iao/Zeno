package api

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestBillingPeriodResetDayClampsAcrossMonthLengths(t *testing.T) {
	tests := []struct {
		name      string
		ts        time.Time
		resetDay  int
		wantKey   string
		wantStart string
		wantEnd   string
	}{
		{
			name:      "reset 29 before short February clamp",
			ts:        time.Date(2026, 2, 27, 12, 0, 0, 0, time.UTC),
			resetDay:  29,
			wantKey:   "2026-01",
			wantStart: "2026-01-29",
			wantEnd:   "2026-02-27",
		},
		{
			name:      "reset 29 on short February clamp",
			ts:        time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC),
			resetDay:  29,
			wantKey:   "2026-02",
			wantStart: "2026-02-28",
			wantEnd:   "2026-03-28",
		},
		{
			name:      "reset 29 leap day",
			ts:        time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC),
			resetDay:  29,
			wantKey:   "2024-02",
			wantStart: "2024-02-29",
			wantEnd:   "2024-03-28",
		},
		{
			name:      "reset 30 short February clamp",
			ts:        time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC),
			resetDay:  30,
			wantKey:   "2026-02",
			wantStart: "2026-02-28",
			wantEnd:   "2026-03-29",
		},
		{
			name:      "reset 31 short February clamp",
			ts:        time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC),
			resetDay:  31,
			wantKey:   "2026-02",
			wantStart: "2026-02-28",
			wantEnd:   "2026-03-30",
		},
		{
			name:      "reset 31 thirty day month clamp",
			ts:        time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
			resetDay:  31,
			wantKey:   "2026-04",
			wantStart: "2026-04-30",
			wantEnd:   "2026-05-30",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			period := billingPeriodFor(tt.ts, tt.resetDay)
			if period.Key != tt.wantKey || period.StartDate != tt.wantStart || period.EndDate != tt.wantEnd {
				t.Fatalf("period = %+v, want key=%s start=%s end=%s", period, tt.wantKey, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestBillingPeriodSQLiteClampExpressionMatchesGo(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	tests := []struct {
		day      string
		resetDay int
	}{
		{day: "2026-02-27", resetDay: 29},
		{day: "2026-02-28", resetDay: 29},
		{day: "2024-02-29", resetDay: 29},
		{day: "2026-02-28", resetDay: 30},
		{day: "2026-02-28", resetDay: 31},
		{day: "2026-04-30", resetDay: 31},
		{day: "2026-03-30", resetDay: 31},
		{day: "2026-03-31", resetDay: 31},
		{day: "2026-07-01", resetDay: 0},
		{day: "2026-07-01", resetDay: 32},
	}
	for _, tt := range tests {
		var got string
		if err := db.QueryRow(`
			SELECT CASE
			  WHEN CAST(strftime('%d', ?) AS INTEGER) < CASE
			    WHEN (CASE WHEN COALESCE(?, 1) BETWEEN 1 AND 31 THEN ? ELSE 1 END) > CAST(strftime('%d', date(?, 'start of month', '+1 month', '-1 day')) AS INTEGER)
			    THEN CAST(strftime('%d', date(?, 'start of month', '+1 month', '-1 day')) AS INTEGER)
			    ELSE (CASE WHEN COALESCE(?, 1) BETWEEN 1 AND 31 THEN ? ELSE 1 END)
			  END THEN strftime('%Y-%m', date(?, 'start of month', '-1 day'))
			  ELSE strftime('%Y-%m', ?)
			END
		`, tt.day, tt.resetDay, tt.resetDay, tt.day, tt.day, tt.resetDay, tt.resetDay, tt.day, tt.day).Scan(&got); err != nil {
			t.Fatalf("sqlite period key for %s/%d: %v", tt.day, tt.resetDay, err)
		}
		parsed, err := time.ParseInLocation("2006-01-02", tt.day, time.UTC)
		if err != nil {
			t.Fatalf("parse day: %v", err)
		}
		if want := billingPeriodKey(parsed, tt.resetDay); got != want {
			t.Fatalf("sqlite key for %s reset %d = %s, want Go key %s", tt.day, tt.resetDay, got, want)
		}
	}
}

func TestBillingTrafficEpochRebasesOnModeAndResetDayChange(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	state := AgentStateRequest{TS: now.Unix(), CPUPercent: 1, MemoryUsedBytes: 1, MemoryTotalBytes: 2, DiskUsedBytes: 1, DiskTotalBytes: 2, NetInSpeedBps: 1, NetOutSpeedBps: 1, UptimeSeconds: 1}
	state.NetInTotalBytes, state.NetOutTotalBytes = 100, 50
	if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
		t.Fatalf("insert baseline state: %v", err)
	}
	state.TS = now.Add(time.Second).Unix()
	state.NetInTotalBytes, state.NetOutTotalBytes = 200, 100
	if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
		t.Fatalf("insert epoch 0 delta: %v", err)
	}

	modeOut := "out"
	if _, err := store.UpdateAdminNode(ctx, "example-node-a", AdminNodeUpdateRequest{BillingMode: &modeOut}); err != nil {
		t.Fatalf("update billing mode: %v", err)
	}
	state.TS = now.Add(2 * time.Second).Unix()
	state.NetInTotalBytes, state.NetOutTotalBytes = 500, 400
	if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
		t.Fatalf("insert epoch 1 baseline: %v", err)
	}
	state.TS = now.Add(3 * time.Second).Unix()
	state.NetInTotalBytes, state.NetOutTotalBytes = 800, 900
	if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
		t.Fatalf("insert epoch 1 delta: %v", err)
	}

	resetDay := 15
	if _, err := store.UpdateAdminNode(ctx, "example-node-a", AdminNodeUpdateRequest{MonthlyResetDay: &resetDay}); err != nil {
		t.Fatalf("update monthly reset day: %v", err)
	}
	state.TS = now.Add(4 * time.Second).Unix()
	state.NetInTotalBytes, state.NetOutTotalBytes = 1000, 1200
	if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
		t.Fatalf("insert epoch 2 baseline: %v", err)
	}

	wantBillable := map[int64]int64{0: 150, 1: 500, 2: 0}
	wantMode := map[int64]string{0: "both", 1: "out", 2: "out"}
	rows, err := store.db.QueryContext(ctx, `
		SELECT billing_epoch, billing_mode, billable_bytes
		FROM traffic_monthly
		WHERE node_id = 'example-node-a'
		ORDER BY billing_epoch
	`)
	if err != nil {
		t.Fatalf("query traffic epochs: %v", err)
	}
	defer rows.Close()
	seen := map[int64]bool{}
	for rows.Next() {
		var epoch, billable int64
		var mode string
		if err := rows.Scan(&epoch, &mode, &billable); err != nil {
			t.Fatalf("scan epoch row: %v", err)
		}
		seen[epoch] = true
		if billable != wantBillable[epoch] || mode != wantMode[epoch] {
			t.Fatalf("epoch %d row mode=%s billable=%d, want mode=%s billable=%d", epoch, mode, billable, wantMode[epoch], wantBillable[epoch])
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate epoch rows: %v", err)
	}
	for epoch := range wantBillable {
		if !seen[epoch] {
			t.Fatalf("missing traffic epoch %d", epoch)
		}
	}

	var currentEpoch int64
	if err := store.db.QueryRowContext(ctx, `SELECT billing_traffic_epoch FROM nodes WHERE id = 'example-node-a'`).Scan(&currentEpoch); err != nil {
		t.Fatalf("read current epoch: %v", err)
	}
	if currentEpoch != 2 {
		t.Fatalf("current billing epoch = %d, want 2", currentEpoch)
	}
	nodes, err := store.nodes(ctx)
	if err != nil {
		t.Fatalf("read public nodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].MonthlyBillableBytes == nil || *nodes[0].MonthlyBillableBytes != 0 {
		t.Fatalf("current public monthly billable = %+v, want current epoch baseline 0", nodes)
	}
}

func TestInvalidNetworkCounterSamplesNeverAdvanceOrCreateBillingBaseline(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	valid, invalid := true, false
	tcpCount, udpCount := int64(41), int64(17)
	state := AgentStateRequest{
		TS: now.Unix(), CPUPercent: 1, MemoryUsedBytes: 1, MemoryTotalBytes: 2,
		DiskUsedBytes: 1, DiskTotalBytes: 2, UptimeSeconds: 1,
		NetInTotalBytes: 900, NetOutTotalBytes: 800, NetTotalsValid: &invalid,
		TCPConnectionCount: &tcpCount, UDPConnectionCount: &udpCount, ConnectionCountsValid: &invalid,
	}
	if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
		t.Fatalf("insert first invalid sample: %v", err)
	}
	var rows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM traffic_monthly WHERE node_id = 'example-node-a'`).Scan(&rows); err != nil {
		t.Fatalf("count traffic rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("invalid first sample created %d billing rows", rows)
	}
	var storedIn, storedOut, storedTCP, storedUDP sql.NullInt64
	if err := store.db.QueryRowContext(ctx, `
		SELECT net_in_total_bytes, net_out_total_bytes, tcp_connection_count, udp_connection_count
		FROM state_samples WHERE node_id = 'example-node-a' AND ts = ?
	`, state.TS).Scan(&storedIn, &storedOut, &storedTCP, &storedUDP); err != nil {
		t.Fatalf("read invalid state sample: %v", err)
	}
	if storedIn.Valid || storedOut.Valid || storedTCP.Valid || storedUDP.Valid {
		t.Fatalf("invalid collector groups persisted as values: in=%v out=%v tcp=%v udp=%v", storedIn, storedOut, storedTCP, storedUDP)
	}

	state.TS = now.Add(time.Second).Unix()
	state.NetInTotalBytes, state.NetOutTotalBytes = 1000, 2000
	state.NetTotalsValid = &valid
	state.ConnectionCountsValid = &valid
	if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
		t.Fatalf("insert valid baseline: %v", err)
	}
	state.TS = now.Add(2 * time.Second).Unix()
	state.NetInTotalBytes, state.NetOutTotalBytes = 0, 0
	state.NetTotalsValid = &invalid
	if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
		t.Fatalf("insert transient failure: %v", err)
	}
	state.TS = now.Add(3 * time.Second).Unix()
	state.NetInTotalBytes, state.NetOutTotalBytes = 1100, 2100
	state.NetTotalsValid = &valid
	if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
		t.Fatalf("insert recovered sample: %v", err)
	}

	var billable, lastIn, lastOut int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT billable_bytes, last_in_total_bytes, last_out_total_bytes
		FROM traffic_monthly WHERE node_id = 'example-node-a'
	`).Scan(&billable, &lastIn, &lastOut); err != nil {
		t.Fatalf("read billing row: %v", err)
	}
	if billable != 200 || lastIn != 1100 || lastOut != 2100 {
		t.Fatalf("billing after 1000/2000 -> invalid -> 1100/2100 = billable %d last %d/%d, want 200 and 1100/2100", billable, lastIn, lastOut)
	}
}

func TestLifetimeTrafficRebasesWhenCounterSourceChanges(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	state := AgentStateRequest{
		TS: now.Unix(), CPUPercent: 1, MemoryUsedBytes: 1, MemoryTotalBytes: 2,
		DiskUsedBytes: 1, DiskTotalBytes: 2, UptimeSeconds: 1,
		NetInTotalBytes: 100, NetOutTotalBytes: 100, NetCounterSource: "source-a",
	}
	for index, totals := range [][2]int64{{100, 100}, {150, 160}, {1000, 2000}, {1010, 2020}} {
		state.TS = now.Add(time.Duration(index) * time.Second).Unix()
		state.NetInTotalBytes, state.NetOutTotalBytes = totals[0], totals[1]
		if index >= 2 {
			state.NetCounterSource = "source-b"
		}
		if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
			t.Fatalf("insert state %d: %v", index, err)
		}
	}
	var lifetimeIn, lifetimeOut int64
	var source string
	if err := store.db.QueryRowContext(ctx, `SELECT in_bytes, out_bytes, counter_source FROM traffic_lifetime WHERE node_id = 'example-node-a'`).Scan(&lifetimeIn, &lifetimeOut, &source); err != nil {
		t.Fatalf("read lifetime row: %v", err)
	}
	if lifetimeIn != 160 || lifetimeOut != 180 || source != "source-b" {
		t.Fatalf("lifetime after source rebase = %d/%d source=%q, want 160/180 source-b", lifetimeIn, lifetimeOut, source)
	}
	var monthlyIn, monthlyOut, billable int64
	if err := store.db.QueryRowContext(ctx, `SELECT in_bytes, out_bytes, billable_bytes FROM traffic_monthly WHERE node_id = 'example-node-a'`).Scan(&monthlyIn, &monthlyOut, &billable); err != nil {
		t.Fatalf("read monthly row: %v", err)
	}
	if monthlyIn != 60 || monthlyOut != 80 || billable != 140 {
		t.Fatalf("monthly traffic after source rebase = %d/%d billable=%d, want 60/80 billable=140", monthlyIn, monthlyOut, billable)
	}
}

func TestTrafficPreservesKnownCounterSourceAcrossLegacyEmptySample(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	source := strings.Repeat("a", 64)
	state := AgentStateRequest{
		CPUPercent: 1, MemoryUsedBytes: 1, MemoryTotalBytes: 2,
		DiskUsedBytes: 1, DiskTotalBytes: 2, UptimeSeconds: 1,
	}
	for index, sample := range []struct {
		total  int64
		source string
	}{{100, source}, {150, ""}, {200, source}} {
		state.TS = now.Add(time.Duration(index) * time.Second).Unix()
		state.NetInTotalBytes, state.NetOutTotalBytes = sample.total, sample.total
		state.NetCounterSource = sample.source
		if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
			t.Fatalf("insert state %d: %v", index, err)
		}
	}
	var lifetimeIn, lifetimeOut, monthlyIn, monthlyOut, billable int64
	var lifetimeSource, monthlySource string
	if err := store.db.QueryRowContext(ctx, `SELECT in_bytes, out_bytes, counter_source FROM traffic_lifetime WHERE node_id = 'example-node-a'`).Scan(&lifetimeIn, &lifetimeOut, &lifetimeSource); err != nil {
		t.Fatalf("read lifetime: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT in_bytes, out_bytes, billable_bytes, counter_source FROM traffic_monthly WHERE node_id = 'example-node-a'`).Scan(&monthlyIn, &monthlyOut, &billable, &monthlySource); err != nil {
		t.Fatalf("read monthly: %v", err)
	}
	if lifetimeIn != 200 || lifetimeOut != 200 || monthlyIn != 100 || monthlyOut != 100 || billable != 200 {
		t.Fatalf("traffic lost across empty source: lifetime=%d/%d monthly=%d/%d billable=%d", lifetimeIn, lifetimeOut, monthlyIn, monthlyOut, billable)
	}
	if lifetimeSource != source || monthlySource != source {
		t.Fatalf("known source was cleared: lifetime=%q monthly=%q", lifetimeSource, monthlySource)
	}
}

func TestTrafficAggregateMigrationSaturatesCorruptRealAndNegativeValues(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "zeno.db")
	store, err := OpenSQLiteStore(databasePath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	now := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO traffic_lifetime (node_id, in_bytes, out_bytes, updated_at) VALUES ('example-node-a', 0, 0, ?);
		INSERT INTO traffic_monthly (node_id, month, billing_epoch, reset_day, billing_mode, in_bytes, out_bytes, billable_bytes, updated_at)
		VALUES ('example-node-a', '2026-07', 0, 1, 'both', 0, 0, 0, ?)
	`, now, now); err != nil {
		t.Fatalf("seed traffic rows: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE traffic_lifetime SET in_bytes = 1.0e30, out_bytes = -1;
		UPDATE traffic_monthly SET in_bytes = 1.0e30, out_bytes = -1, billable_bytes = -2
	`); err != nil {
		t.Fatalf("corrupt traffic rows: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}
	store, err = OpenSQLiteStore(databasePath)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	defer store.Close()
	var lifetimeIn, lifetimeOut, monthlyIn, monthlyOut, billable int64
	var lifetimeInType, lifetimeOutType, monthlyInType, monthlyOutType, billableType string
	if err := store.db.QueryRow(`SELECT in_bytes, out_bytes, typeof(in_bytes), typeof(out_bytes) FROM traffic_lifetime WHERE node_id='example-node-a'`).Scan(&lifetimeIn, &lifetimeOut, &lifetimeInType, &lifetimeOutType); err != nil {
		t.Fatalf("read lifetime: %v", err)
	}
	if err := store.db.QueryRow(`SELECT in_bytes, out_bytes, billable_bytes, typeof(in_bytes), typeof(out_bytes), typeof(billable_bytes) FROM traffic_monthly WHERE node_id='example-node-a'`).Scan(&monthlyIn, &monthlyOut, &billable, &monthlyInType, &monthlyOutType, &billableType); err != nil {
		t.Fatalf("read monthly: %v", err)
	}
	if lifetimeIn != math.MaxInt64 || lifetimeOut != math.MaxInt64 || monthlyIn != math.MaxInt64 || monthlyOut != math.MaxInt64 || billable != math.MaxInt64 {
		t.Fatalf("corrupt traffic not saturated: lifetime=%d/%d monthly=%d/%d billable=%d", lifetimeIn, lifetimeOut, monthlyIn, monthlyOut, billable)
	}
	if lifetimeInType != "integer" || lifetimeOutType != "integer" || monthlyInType != "integer" || monthlyOutType != "integer" || billableType != "integer" {
		t.Fatalf("corrupt traffic types not repaired: lifetime=%s/%s monthly=%s/%s/%s", lifetimeInType, lifetimeOutType, monthlyInType, monthlyOutType, billableType)
	}
}

func TestTrafficMonthlyEpochMigrationPreservesLegacyRows(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "zeno.db")
	db, err := sql.Open("sqlite", sqliteURLForTest(t, databasePath))
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	now := time.Now().UTC().Unix()
	if _, err := db.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL,
			token_hash TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'no_data',
			country_code TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE traffic_monthly (
			node_id TEXT NOT NULL REFERENCES nodes(id),
			month TEXT NOT NULL,
			in_bytes INTEGER NOT NULL DEFAULT 0,
			out_bytes INTEGER NOT NULL DEFAULT 0,
			billable_bytes INTEGER NOT NULL DEFAULT 0,
			last_in_total_bytes INTEGER,
			last_out_total_bytes INTEGER,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (node_id, month)
		);
	`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO nodes (id, display_name, token_hash, status, country_code, created_at, updated_at) VALUES ('example-node-a', 'Example Node A', 'hash', 'online', 'HK', ?, ?)`, now, now); err != nil {
		t.Fatalf("insert legacy node: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO traffic_monthly (node_id, month, in_bytes, out_bytes, billable_bytes, last_in_total_bytes, last_out_total_bytes, updated_at) VALUES ('example-node-a', '2026-06', 10, 20, 30, 100, 200, ?)`, now); err != nil {
		t.Fatalf("insert legacy traffic: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := OpenSQLiteStore(databasePath)
	if err != nil {
		t.Fatalf("open migrated sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	pkIncludesEpoch, err := store.primaryKeyIncludes(ctx, "traffic_monthly", "billing_epoch")
	if err != nil {
		t.Fatalf("inspect migrated primary key: %v", err)
	}
	if !pkIncludesEpoch {
		t.Fatal("traffic_monthly primary key does not include billing_epoch after migration")
	}
	var epoch, resetDay, inBytes, outBytes, billable int64
	var mode string
	if err := store.db.QueryRowContext(ctx, `
		SELECT billing_epoch, reset_day, billing_mode, in_bytes, out_bytes, billable_bytes
		FROM traffic_monthly
		WHERE node_id = 'example-node-a' AND month = '2026-06'
	`).Scan(&epoch, &resetDay, &mode, &inBytes, &outBytes, &billable); err != nil {
		t.Fatalf("read migrated legacy row: %v", err)
	}
	if epoch != 0 || resetDay != 1 || mode != "both" || inBytes != 10 || outBytes != 20 || billable != 30 {
		t.Fatalf("migrated row = epoch %d reset %d mode %s in %d out %d billable %d", epoch, resetDay, mode, inBytes, outBytes, billable)
	}
}

func TestBillingModeMaxUsesPeriodAggregateMaxAndHealsDriftedRows(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "zeno.db")
	store, err := OpenSQLiteStore(databasePath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	modeMax := "max"
	if _, err := store.UpdateAdminNode(ctx, "example-node-a", AdminNodeUpdateRequest{BillingMode: &modeMax}); err != nil {
		t.Fatalf("set billing_mode=max: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	state := AgentStateRequest{
		CPUPercent: 1, MemoryUsedBytes: 1, MemoryTotalBytes: 2,
		DiskUsedBytes: 1, DiskTotalBytes: 2, UptimeSeconds: 1,
	}
	// Slice 0: baseline (1000, 1000)
	// Slice 1: +300 in, +0 out -> cumulative (300, 0), max = 300
	// Slice 2: +0 in, +400 out -> cumulative (300, 400), max = 400 (NOT 300+400=700!)
	// Slice 3: +250 in, +50 out -> cumulative (550, 450), max = 550 (NOT 700+250=950!)
	for idx, totals := range [][2]int64{
		{1000, 1000},
		{1300, 1000},
		{1300, 1400},
		{1550, 1450},
	} {
		state.TS = now.Add(time.Duration(idx) * 3 * time.Second).Unix()
		state.NetInTotalBytes = totals[0]
		state.NetOutTotalBytes = totals[1]
		if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
			t.Fatalf("insert state %d: %v", idx, err)
		}
	}

	var inBytes, outBytes, billable int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT in_bytes, out_bytes, billable_bytes
		FROM traffic_monthly
		WHERE node_id = 'example-node-a' AND billing_mode = 'max'
	`).Scan(&inBytes, &outBytes, &billable); err != nil {
		t.Fatalf("query max row: %v", err)
	}
	if inBytes != 550 || outBytes != 450 || billable != 550 {
		t.Fatalf("max row = in=%d out=%d billable=%d, want 550/450/550", inBytes, outBytes, billable)
	}

	// Simulate legacy drifted sum-of-slice-maxes on disk and verify startup self-healing.
	if _, err := store.db.ExecContext(ctx, `
		UPDATE traffic_monthly SET billable_bytes = 950 WHERE node_id = 'example-node-a' AND billing_mode = 'max'
	`); err != nil {
		t.Fatalf("inject drifted billable_bytes: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := OpenSQLiteStore(databasePath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()

	if err := reopened.db.QueryRowContext(ctx, `
		SELECT in_bytes, out_bytes, billable_bytes
		FROM traffic_monthly
		WHERE node_id = 'example-node-a' AND billing_mode = 'max'
	`).Scan(&inBytes, &outBytes, &billable); err != nil {
		t.Fatalf("query healed row: %v", err)
	}
	if inBytes != 550 || outBytes != 450 || billable != 550 {
		t.Fatalf("healed max row = in=%d out=%d billable=%d, want 550/450/550", inBytes, outBytes, billable)
	}
}

func TestBillingPeriodFollowsCallerTimezoneAtMidnightWindow(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*3600)
	// 2026-11-15 00:30:00 +0800 is 2026-11-14 16:30:00 UTC.
	// With resetDay=15, Shanghai local time has ALREADY entered the 2026-11 period,
	// whereas a UTC-anchored caller is still in 2026-10.
	localAfterMidnight := time.Date(2026, 11, 15, 0, 30, 0, 0, shanghai)
	shanghaiPeriod := billingPeriodFor(localAfterMidnight, 15)
	if shanghaiPeriod.Key != "2026-11" || shanghaiPeriod.StartDate != "2026-11-15" || shanghaiPeriod.EndDate != "2026-12-14" {
		t.Fatalf("Shanghai 00:30 period = %+v, want 2026-11 (2026-11-15..2026-12-14)", shanghaiPeriod)
	}

	utcEquivalent := localAfterMidnight.UTC()
	utcPeriod := billingPeriodFor(utcEquivalent, 15)
	if utcPeriod.Key != "2026-10" || utcPeriod.StartDate != "2026-10-15" || utcPeriod.EndDate != "2026-11-14" {
		t.Fatalf("UTC 16:30 period = %+v, want 2026-10 (2026-10-15..2026-11-14)", utcPeriod)
	}
}

func sqliteURLForTest(t *testing.T, path string) string {
	t.Helper()
	dsn, err := sqliteDSN(path)
	if err != nil {
		t.Fatalf("build sqlite dsn: %v", err)
	}
	return dsn
}
