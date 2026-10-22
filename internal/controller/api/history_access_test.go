package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestExtendedHistoryRequiresAdminToken(t *testing.T) {
	const adminToken = "history-admin-token"
	handler := newMockHandler(HandlerOptions{AdminPasswordHash: testAdminPasswordHash(adminToken)})
	paths := []string{
		"/api/public/v1/nodes/example-node-a/latency?range=7d",
		"/api/public/v1/nodes/example-node-a/state?range=30d",
	}
	for _, path := range paths {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", path, recorder.Code)
		}

		recorder = httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-Admin-Token", adminToken)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("authorized %s status = %d, want 200; body=%s", path, recorder.Code, recorder.Body.String())
		}
	}

	for _, path := range []string{
		"/api/public/v1/nodes/example-node-a/latency?range=1d",
		"/api/public/v1/nodes/example-node-a/state?range=1d",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("public %s status = %d, want 200", path, recorder.Code)
		}
	}
}

func TestExtendedHistoryRejectsExistingSessionWhenAdminIsDisabled(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	const oldSession = "old-admin-session"
	if err := store.createAdminSession(context.Background(), oldSession); err != nil {
		t.Fatalf("create old admin session: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/nodes/example-node-a/latency?range=7d", nil)
	request.Header.Set("X-Admin-Token", oldSession)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("disabled admin extended history status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestTieredHistoryKeepsExactlyThirtyDayWindow(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, created_at, updated_at)
		VALUES ('node-1', 'Node 1', 'hash', 'online', ?, ?);
		INSERT INTO probe_targets (id, name, type, address, port, count, timeout_ms, interval_sec, created_at, updated_at)
		VALUES ('target-1', 'Target 1', 'tcp', '127.0.0.1', 443, 1, 1000, 30, ?, ?);
	`, now.Unix(), now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatalf("seed retention rows: %v", err)
	}

	oldTS := now.Add(-31 * 24 * time.Hour).Unix()
	boundaryTS := now.Add(-30 * 24 * time.Hour).Unix()
	newTS := now.Add(-29 * 24 * time.Hour).Unix()
	for _, ts := range []int64{oldTS, boundaryTS, newTS} {
		if _, err := store.db.ExecContext(ctx, `INSERT INTO state_samples (node_id, ts, cpu_percent) VALUES ('node-1', ?, 10)`, ts); err != nil {
			t.Fatalf("insert state sample: %v", err)
		}
		result, err := store.db.ExecContext(ctx, `
			INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent)
			VALUES ('node-1', 'target-1', ?, 'tcp', 1, 1, 0)
		`, ts)
		if err != nil {
			t.Fatalf("insert probe round: %v", err)
		}
		roundID, _ := result.LastInsertId()
		if _, err := store.db.ExecContext(ctx, `INSERT INTO probe_samples (round_id, seq, success, latency_ms) VALUES (?, 1, 1, 10)`, roundID); err != nil {
			t.Fatalf("insert probe sample: %v", err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE history_rollup_meta SET enabled_after = 0 WHERE id = 1`); err != nil {
		t.Fatalf("enable tiered history in test: %v", err)
	}

	if err := store.MaintainHistory(ctx, now); err != nil {
		t.Fatalf("maintain history: %v", err)
	}
	for table, want := range map[string]int{"state_samples": 0, "probe_rounds": 0, "probe_samples": 0, "state_history_rollups": 2, "latency_history_rollups": 2} {
		var got int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s rows = %d, want %d", table, got, want)
		}
	}
}
