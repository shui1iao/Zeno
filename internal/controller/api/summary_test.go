package api

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	moderncsqlite "modernc.org/sqlite"
)

func TestSummaryBatchQueriesMatchLegacySemantics(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	seedSummarySemanticsFixture(t, store)

	got, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	want, err := legacySummaryForTest(ctx, store)
	if err != nil {
		t.Fatalf("legacy summary: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("summary mismatch after batching\ngot:  %#v\nwant: %#v", got, want)
	}

	byNode := map[string]Node{}
	for _, node := range got.Nodes {
		byNode[node.ID] = node
	}
	if byNode["node-a"].LatencySummary == nil {
		t.Fatalf("node-a home latency summary is nil")
	}
	if byNode["node-a"].LatencySummary.AvgMS != nil {
		t.Fatalf("node-a home AvgMS = %v, want latest all-loss round to remain unavailable", *byNode["node-a"].LatencySummary.AvgMS)
	}
	if got, want := *byNode["node-a"].LatencySummary.LossPercent, 50.0; got != want {
		t.Fatalf("node-a home 24h loss = %v, want %v", got, want)
	}
	if byNode["node-b"].LatencySummary != nil {
		t.Fatalf("node-b disabled home assignment produced summary: %#v", byNode["node-b"].LatencySummary)
	}

	byService := map[string]ServiceTarget{}
	for _, service := range got.Services {
		byService[service.ID] = service
	}
	if got, want := byService["target-a"].ReportingNodeCount, 2; got != want {
		t.Fatalf("target-a ReportingNodeCount = %d, want %d", got, want)
	}
	if got, want := *byService["target-a"].AvgMS, 30.0; got != want {
		t.Fatalf("target-a latest AvgMS = %v, want latest non-disabled assigned node value %v", got, want)
	}
	if got, want := byService["target-empty"].AssignedNodeCount, 1; got != want {
		t.Fatalf("target-empty AssignedNodeCount = %d, want %d", got, want)
	}
	if byService["target-empty"].UpdatedAt != "" || byService["target-empty"].MedianMS != nil || byService["target-empty"].ReportingNodeCount != 0 {
		t.Fatalf("target-empty summary fields = %#v, want no latency summary", byService["target-empty"])
	}
}

func TestSummaryIncludesTwelveHourlyHomeLatencyBuckets(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	bucketStart := now.Truncate(time.Hour).Add(-3 * time.Hour)
	seedSummaryNode(t, store, "node-history", "History Node", "target-history", 1, false, now)
	seedSummaryTarget(t, store, "target-history", "History Target", "tcp", "1.1.1.1", 1, now)
	seedSummaryAssignment(t, store, "node-history", "target-history", true)
	seedSummaryRound(t, store, "node-history", "target-history", bucketStart.Add(10*time.Minute), 2, float64Ptr(40), float64Ptr(30))
	seedSummaryRound(t, store, "node-history", "target-history", bucketStart.Add(20*time.Minute), 4, float64Ptr(60), float64Ptr(50))

	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 1 || summary.Nodes[0].LatencySummary == nil {
		t.Fatalf("summary node latency = %#v", summary.Nodes)
	}
	history := summary.Nodes[0].LatencySummary.HourlyHistory
	if len(history) != 12 {
		t.Fatalf("hourly history len = %d, want 12", len(history))
	}
	var matched *HourlyLatencyPoint
	for index := range history {
		if history[index].StartedAt == bucketStart.Format(time.RFC3339) {
			matched = &history[index]
			break
		}
	}
	if matched == nil {
		t.Fatalf("missing bucket %s in %#v", bucketStart.Format(time.RFC3339), history)
	}
	if matched.LatencyMS == nil || *matched.LatencyMS != 50 {
		t.Fatalf("bucket latency = %v, want 50", matched.LatencyMS)
	}
	if matched.LossPercent == nil || *matched.LossPercent != 3 {
		t.Fatalf("bucket loss = %v, want 3", matched.LossPercent)
	}
	if history[0].LatencyMS != nil || history[0].LossPercent != nil {
		t.Fatalf("empty bucket should remain nil: %#v", history[0])
	}
}

func TestSummaryQueryCountDoesNotGrowWithNodesOrServices(t *testing.T) {
	ctx := context.Background()
	smallStore, smallCounter := openCountingSummaryStore(t)
	defer smallStore.Close()
	seedSummaryScaleFixture(t, smallStore, 2, 2)
	smallCounter.reset()
	if _, err := smallStore.Summary(ctx); err != nil {
		t.Fatalf("small summary: %v", err)
	}
	smallQueries := smallCounter.count()

	largeStore, largeCounter := openCountingSummaryStore(t)
	defer largeStore.Close()
	seedSummaryScaleFixture(t, largeStore, 9, 7)
	largeCounter.reset()
	if _, err := largeStore.Summary(ctx); err != nil {
		t.Fatalf("large summary: %v", err)
	}
	largeQueries := largeCounter.count()

	if smallQueries != largeQueries {
		t.Fatalf("summary query count grew with fixture size: small=%d large=%d", smallQueries, largeQueries)
	}
	if smallQueries != 7 {
		t.Fatalf("summary query count = %d, want fixed 7 statements (nodes, home summaries, home history, per-node summaries, service list, service summaries, exchange rates)", smallQueries)
	}

	largeCounter.reset()
	if _, err := largeStore.Summary(ctx); err != nil {
		t.Fatalf("cached large summary: %v", err)
	}
	if cachedQueries := largeCounter.count(); cachedQueries != 2 {
		t.Fatalf("cached summary query count = %d, want fresh node state and exchange rates", cachedQueries)
	}

	largeStore.summaryCache.mu.Lock()
	largeStore.summaryCache.updated = time.Now().Add(-summaryAggregateFreshFor)
	largeStore.summaryCache.mu.Unlock()
	largeCounter.reset()
	if _, err := largeStore.Summary(ctx); err != nil {
		t.Fatalf("expired large summary: %v", err)
	}
	if expiredQueries := largeCounter.count(); expiredQueries != 7 {
		t.Fatalf("expired summary query count = %d, want full 7 statements", expiredQueries)
	}
}

func TestAgentProbeResults202DoesNotWaitForInflightAggregateSQL(t *testing.T) {
	store, counter := openCountingSummaryStore(t)
	defer store.Close()
	seedSummaryScaleFixture(t, store, 1, 1)
	// The aggregate query reserves one database/sql connection before the test
	// driver pauses it. Keep a second connection available so this test isolates
	// the aggregate cache lock from SQLite connection-pool contention.
	store.db.SetMaxOpenConns(2)

	aggregateStarted := make(chan struct{})
	aggregateRelease := make(chan struct{})
	counter.blockQuery("loss_by_node AS", aggregateStarted, aggregateRelease)
	aggregateDone := make(chan error, 1)
	go func() {
		_, _, _, err := store.summaryAggregates(context.Background())
		aggregateDone <- err
	}()
	select {
	case <-aggregateStarted:
	case <-time.After(time.Second):
		t.Fatal("aggregate query did not start")
	}

	handler := NewHandler(HandlerOptions{Store: store})
	defer cleanupTestHandler(t, handler)
	payload := fmt.Sprintf(`{"rounds":[{"round_id":"nonblocking-aggregate","target_id":"target-00","ts":%d,"type":"tcp","samples":[{"seq":1,"success":true,"latency_ms":12.5}]}]}`, time.Now().UTC().Unix())
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", strings.NewReader(payload))
	request.Header.Set("X-Node-ID", "node-00")
	request.Header.Set("Authorization", "Bearer node-00-token")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	responseDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(responseDone)
	}()
	select {
	case <-responseDone:
	case <-time.After(500 * time.Millisecond):
		close(aggregateRelease)
		t.Fatal("Agent 202 waited for in-flight aggregate SQL")
	}

	close(aggregateRelease)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("probe response status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}
	select {
	case err := <-aggregateDone:
		if err != nil {
			t.Fatalf("aggregate after probe invalidation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("aggregate query did not finish after release")
	}
}

func TestSummaryAggregatesSingleflightConcurrentExpiredCache(t *testing.T) {
	store, counter := openCountingSummaryStore(t)
	defer store.Close()
	seedSummaryScaleFixture(t, store, 2, 2)
	counter.reset()

	const callers = 12
	start := make(chan struct{})
	errors := make(chan error, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for range callers {
		go func() {
			defer wait.Done()
			<-start
			_, _, _, err := store.summaryAggregates(context.Background())
			errors <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent aggregate: %v", err)
		}
	}
	if queries := counter.count(); queries != 5 {
		t.Fatalf("concurrent aggregate queries = %d, want one five-statement build", queries)
	}
}

func legacySummaryForTest(ctx context.Context, store *SQLiteStore) (SummaryResponse, error) {
	nodes, err := store.nodes(ctx)
	if err != nil {
		return SummaryResponse{}, err
	}
	now := time.Now().UTC()
	historyByNode, err := store.homeLatencyHourlyHistory(ctx, now)
	if err != nil {
		return SummaryResponse{}, err
	}
	for index := range nodes {
		summary, err := store.latestLatencySummary(ctx, nodes[index].ID)
		if err != nil {
			return SummaryResponse{}, err
		}
		if summary != nil {
			if history, ok := historyByNode[nodes[index].ID]; ok {
				summary.HourlyHistory = history
			} else {
				summary.HourlyHistory = emptyHourlyLatencyHistory(now)
			}
		}
		nodes[index].LatencySummary = summary
		latencySummaries, err := store.latestLatencySummaries(ctx, nodes[index].ID)
		if err != nil {
			return SummaryResponse{}, err
		}
		nodes[index].LatencySummaries = latencySummaries
	}
	services, err := legacyServiceTargetsForTest(ctx, store)
	if err != nil {
		return SummaryResponse{}, err
	}
	exchangeRates, err := store.exchangeRateSnapshot(ctx)
	if err != nil {
		return SummaryResponse{}, err
	}
	return SummaryResponse{Nodes: nodes, Services: services, LatencyPoints: []LatencyPoint{}, ExchangeRates: exchangeRates}, nil
}

func legacyServiceTargetsForTest(ctx context.Context, store *SQLiteStore) ([]ServiceTarget, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT pt.id, pt.name, pt.type,
		       COUNT(DISTINCT CASE WHEN n.id IS NOT NULL AND COALESCE(npt.enabled, 0) = 1 AND n.disabled = 0 THEN n.id END) AS assigned_nodes
		FROM probe_targets pt
		LEFT JOIN node_probe_targets npt ON npt.target_id = pt.id
		LEFT JOIN nodes n ON n.id = npt.node_id
		GROUP BY pt.id, pt.name, pt.type, pt.display_order
		ORDER BY pt.display_order ASC, pt.name ASC, pt.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	targets := []ServiceTarget{}
	for rows.Next() {
		var target ServiceTarget
		if err := rows.Scan(&target.ID, &target.Name, &target.Type, &target.AssignedNodeCount); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range targets {
		if err := store.legacyPopulateServiceTargetLatencySummary(ctx, &targets[index]); err != nil {
			return nil, err
		}
	}
	return targets, nil
}

func seedSummarySemanticsFixture(t *testing.T, store *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second)
	seedSummaryNode(t, store, "node-a", "Node A", "target-a", 1, false, now)
	seedSummaryNode(t, store, "node-b", "Node B", "target-b", 2, false, now)
	seedSummaryNode(t, store, "node-c", "Node C", "", 3, false, now)
	seedSummaryNode(t, store, "node-disabled", "Disabled", "target-a", 4, true, now)

	seedSummaryTarget(t, store, "target-a", "Alpha", "tcp", "1.1.1.1", 20, now)
	seedSummaryTarget(t, store, "target-b", "Beta", "tcp", "8.8.8.8", 10, now)
	seedSummaryTarget(t, store, "target-c", "Gamma", "tcp", "9.9.9.9", 30, now)
	seedSummaryTarget(t, store, "target-empty", "Empty", "tcp", "203.0.113.10", 40, now)

	seedSummaryAssignment(t, store, "node-a", "target-a", true)
	seedSummaryAssignment(t, store, "node-a", "target-b", true)
	seedSummaryAssignment(t, store, "node-a", "target-c", true)
	seedSummaryAssignment(t, store, "node-b", "target-a", true)
	seedSummaryAssignment(t, store, "node-b", "target-b", false)
	seedSummaryAssignment(t, store, "node-b", "target-c", true)
	seedSummaryAssignment(t, store, "node-c", "target-empty", true)
	seedSummaryAssignment(t, store, "node-disabled", "target-a", true)

	seedSummaryRound(t, store, "node-a", "target-a", now.Add(-10*time.Second), 100, nil, nil)
	seedSummaryRound(t, store, "node-a", "target-a", now.Add(-20*time.Second), 50, nil, float64Ptr(12))
	seedSummaryRound(t, store, "node-a", "target-a", now.Add(-30*time.Second), 0, float64Ptr(10), float64Ptr(9))
	seedSummaryRound(t, store, "node-a", "target-a", now.Add(-25*time.Hour), 75, float64Ptr(70), float64Ptr(69))
	seedSummaryRound(t, store, "node-a", "target-b", now.Add(-200*time.Second), 5, float64Ptr(20), float64Ptr(19))
	seedSummaryRound(t, store, "node-b", "target-a", now.Add(-5*time.Second), 0, float64Ptr(30), float64Ptr(29))
	seedSummaryRound(t, store, "node-b", "target-b", now.Add(-5*time.Second), 0, float64Ptr(40), float64Ptr(39))
	seedSummaryRound(t, store, "node-b", "target-c", now.Add(-50*time.Second), 0, nil, float64Ptr(55))
	seedSummaryRound(t, store, "node-disabled", "target-a", now.Add(-1*time.Second), 0, float64Ptr(1), float64Ptr(1))

	if _, err := store.db.ExecContext(ctx, `INSERT INTO state_samples (node_id, ts, cpu_percent) VALUES ('node-a', ?, 12.5)`, now.Unix()); err != nil {
		t.Fatalf("insert state sample: %v", err)
	}
}

func seedSummaryScaleFixture(t *testing.T, store *SQLiteStore, nodeCount, targetCount int) {
	t.Helper()
	now := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second)
	for targetIndex := 0; targetIndex < targetCount; targetIndex++ {
		seedSummaryTarget(t, store, fmt.Sprintf("target-%02d", targetIndex), fmt.Sprintf("Target %02d", targetIndex), "tcp", fmt.Sprintf("192.0.2.%d", targetIndex+1), targetIndex, now)
	}
	for nodeIndex := 0; nodeIndex < nodeCount; nodeIndex++ {
		nodeID := fmt.Sprintf("node-%02d", nodeIndex)
		homeTargetID := fmt.Sprintf("target-%02d", nodeIndex%targetCount)
		seedSummaryNode(t, store, nodeID, fmt.Sprintf("Node %02d", nodeIndex), homeTargetID, nodeIndex, false, now)
		for targetIndex := 0; targetIndex < targetCount; targetIndex++ {
			targetID := fmt.Sprintf("target-%02d", targetIndex)
			seedSummaryAssignment(t, store, nodeID, targetID, true)
			seedSummaryRound(t, store, nodeID, targetID, now.Add(time.Duration(-(nodeIndex*targetCount+targetIndex))*time.Second), float64((nodeIndex+targetIndex)%100), float64Ptr(float64(nodeIndex+targetIndex+1)), float64Ptr(float64(nodeIndex+targetIndex+1)))
		}
	}
}

func seedSummaryNode(t *testing.T, store *SQLiteStore, id, name, homeTargetID string, displayOrder int, disabled bool, now time.Time) {
	t.Helper()
	disabledInt := 0
	if disabled {
		disabledInt = 1
	}
	if _, err := store.db.ExecContext(context.Background(), `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, home_probe_target_id, display_order, disabled, created_at, updated_at, last_seen_at)
		VALUES (?, ?, ?, 'online', 'HK', ?, ?, ?, ?, ?, ?)
	`, id, name, HashAdminToken(id+"-token"), nullableStringValue(homeTargetID), displayOrder, disabledInt, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert node %s: %v", id, err)
	}
}

func seedSummaryTarget(t *testing.T, store *SQLiteStore, id, name, targetType, address string, displayOrder int, now time.Time) {
	t.Helper()
	if _, err := store.db.ExecContext(context.Background(), `
		INSERT INTO probe_targets (id, name, type, address, count, timeout_ms, interval_sec, display_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, 3, 1000, 30, ?, ?, ?)
	`, id, name, targetType, address, displayOrder, now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert target %s: %v", id, err)
	}
}

func seedSummaryAssignment(t *testing.T, store *SQLiteStore, nodeID, targetID string, enabled bool) {
	t.Helper()
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	if _, err := store.db.ExecContext(context.Background(), `
		INSERT INTO node_probe_targets (node_id, target_id, enabled)
		VALUES (?, ?, ?)
	`, nodeID, targetID, enabledInt); err != nil {
		t.Fatalf("insert assignment %s/%s: %v", nodeID, targetID, err)
	}
}

func seedSummaryRound(t *testing.T, store *SQLiteStore, nodeID, targetID string, ts time.Time, loss float64, avg, median *float64) {
	t.Helper()
	if _, err := store.db.ExecContext(context.Background(), `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, avg_ms, median_ms)
		VALUES (?, ?, ?, 'tcp', 3, 3, ?, ?, ?)
	`, nodeID, targetID, ts.Unix(), loss, nullableFloatValue(avg), nullableFloatValue(median)); err != nil {
		t.Fatalf("insert round %s/%s/%d: %v", nodeID, targetID, ts.Unix(), err)
	}
}

func nullableStringValue(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableFloatValue(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func float64Ptr(value float64) *float64 { return &value }

type summaryQueryBlock struct {
	contains string
	started  chan<- struct{}
	release  <-chan struct{}
}

type summaryQueryCounter struct {
	enabled atomic.Bool
	queries atomic.Int64
	mu      sync.Mutex
	blocks  []summaryQueryBlock
}

func (counter *summaryQueryCounter) reset() {
	counter.queries.Store(0)
	counter.enabled.Store(true)
}

func (counter *summaryQueryCounter) count() int64 {
	counter.enabled.Store(false)
	return counter.queries.Load()
}

func (counter *summaryQueryCounter) add(query string) {
	if counter.enabled.Load() && strings.TrimSpace(query) != "" {
		counter.queries.Add(1)
	}
	counter.mu.Lock()
	var block *summaryQueryBlock
	for index := range counter.blocks {
		if !strings.Contains(query, counter.blocks[index].contains) {
			continue
		}
		matched := counter.blocks[index]
		counter.blocks = append(counter.blocks[:index], counter.blocks[index+1:]...)
		block = &matched
		break
	}
	counter.mu.Unlock()
	if block == nil {
		return
	}
	close(block.started)
	<-block.release
}

func (counter *summaryQueryCounter) blockQuery(contains string, started chan<- struct{}, release <-chan struct{}) {
	counter.mu.Lock()
	counter.blocks = append(counter.blocks, summaryQueryBlock{contains: contains, started: started, release: release})
	counter.mu.Unlock()
}

var summaryCountingDriverSequence atomic.Uint64

func openCountingSummaryStore(t *testing.T) (*SQLiteStore, *summaryQueryCounter) {
	t.Helper()
	counter := &summaryQueryCounter{}
	driverName := fmt.Sprintf("sqlite-summary-count-%d", summaryCountingDriverSequence.Add(1))
	sql.Register(driverName, &summaryCountingDriver{counter: counter})
	db, err := sql.Open(driverName, filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open counting sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		db.Close()
		t.Fatalf("enable foreign keys: %v", err)
	}
	store := newSQLiteStore(db, nil)
	if err := store.ensureSchema(context.Background()); err != nil {
		db.Close()
		t.Fatalf("ensure schema: %v", err)
	}
	return store, counter
}

type summaryCountingDriver struct {
	counter *summaryQueryCounter
}

func (d *summaryCountingDriver) Open(name string) (driver.Conn, error) {
	conn, err := (&moderncsqlite.Driver{}).Open(name)
	if err != nil {
		return nil, err
	}
	return &summaryCountingConn{Conn: conn, counter: d.counter}, nil
}

type summaryCountingConn struct {
	driver.Conn
	counter *summaryQueryCounter
}

func (conn *summaryCountingConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := conn.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &summaryCountingStmt{Stmt: stmt, query: query, counter: conn.counter}, nil
}

func (conn *summaryCountingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if preparer, ok := conn.Conn.(driver.ConnPrepareContext); ok {
		stmt, err := preparer.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return &summaryCountingStmt{Stmt: stmt, query: query, counter: conn.counter}, nil
	}
	return conn.Prepare(query)
}

func (conn *summaryCountingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := conn.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	conn.counter.add(query)
	return execer.ExecContext(ctx, query, args)
}

func (conn *summaryCountingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	querier, ok := conn.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	conn.counter.add(query)
	return querier.QueryContext(ctx, query, args)
}

func (conn *summaryCountingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if beginner, ok := conn.Conn.(driver.ConnBeginTx); ok {
		return beginner.BeginTx(ctx, opts)
	}
	//lint:ignore SA1019 The wrapper must preserve the database/sql driver fallback.
	return conn.Conn.Begin()
}

func (conn *summaryCountingConn) Ping(ctx context.Context) error {
	if pinger, ok := conn.Conn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func (conn *summaryCountingConn) ResetSession(ctx context.Context) error {
	if resetter, ok := conn.Conn.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

func (conn *summaryCountingConn) IsValid() bool {
	if validator, ok := conn.Conn.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}

func (conn *summaryCountingConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := conn.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

type summaryCountingStmt struct {
	driver.Stmt
	query   string
	counter *summaryQueryCounter
}

func (stmt *summaryCountingStmt) Exec(args []driver.Value) (driver.Result, error) {
	stmt.counter.add(stmt.query)
	//lint:ignore SA1019 driver.Stmt requires this legacy compatibility method.
	return stmt.Stmt.Exec(args)
}

func (stmt *summaryCountingStmt) Query(args []driver.Value) (driver.Rows, error) {
	stmt.counter.add(stmt.query)
	//lint:ignore SA1019 driver.Stmt requires this legacy compatibility method.
	return stmt.Stmt.Query(args)
}

func (stmt *summaryCountingStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	stmt.counter.add(stmt.query)
	if execer, ok := stmt.Stmt.(driver.StmtExecContext); ok {
		return execer.ExecContext(ctx, args)
	}
	//lint:ignore SA1019 The wrapper must preserve the database/sql driver fallback.
	return stmt.Stmt.Exec(namedValuesToValues(args))
}

func (stmt *summaryCountingStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	stmt.counter.add(stmt.query)
	if querier, ok := stmt.Stmt.(driver.StmtQueryContext); ok {
		return querier.QueryContext(ctx, args)
	}
	//lint:ignore SA1019 The wrapper must preserve the database/sql driver fallback.
	return stmt.Stmt.Query(namedValuesToValues(args))
}

func namedValuesToValues(args []driver.NamedValue) []driver.Value {
	values := make([]driver.Value, len(args))
	for index, arg := range args {
		values[index] = arg.Value
	}
	return values
}

// legacyPopulateServiceTargetLatencySummary is the former per-target query,
// kept only as a test oracle for the batched summary query.
func (s *sqliteReadQueries) legacyPopulateServiceTargetLatencySummary(ctx context.Context, target *ServiceTarget) error {
	since := time.Now().UTC().Add(-24 * time.Hour).Unix()
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT pr.node_id)
		FROM probe_rounds pr
		JOIN nodes n ON n.id = pr.node_id
		LEFT JOIN node_probe_targets npt ON npt.node_id = pr.node_id AND npt.target_id = pr.target_id
		WHERE pr.target_id = ?
		  AND pr.ts >= ?
		  AND n.disabled = 0
		  AND COALESCE(npt.enabled, 0) = 1
	`, target.ID, since).Scan(&target.ReportingNodeCount); err != nil {
		return err
	}
	var median, avg sql.NullFloat64
	var loss sql.NullFloat64
	var ts sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT pr.median_ms, pr.avg_ms, pr.loss_percent, pr.ts
		FROM probe_rounds pr
		JOIN nodes n ON n.id = pr.node_id
		LEFT JOIN node_probe_targets npt ON npt.node_id = pr.node_id AND npt.target_id = pr.target_id
		WHERE pr.target_id = ?
		  AND n.disabled = 0
		  AND COALESCE(npt.enabled, 0) = 1
		ORDER BY pr.ts DESC, pr.id DESC
		LIMIT 1
	`, target.ID).Scan(&median, &avg, &loss, &ts)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	target.MedianMS = floatPtr(median)
	target.AvgMS = floatPtr(avg)
	target.LossPercent = floatPtr(loss)
	if ts.Valid {
		target.UpdatedAt = time.Unix(ts.Int64, 0).UTC().Format(time.RFC3339)
	}
	return nil
}
