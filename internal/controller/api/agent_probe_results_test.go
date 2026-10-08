package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAgentProbeResultsAcceptsSamplesAndUpdatesPublicLatency(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	ts := time.Now().UTC().Truncate(time.Second).Unix()
	body := map[string]any{
		"rounds": []map[string]any{
			{
				"target_id": "google-dns",
				"ts":        ts,
				"type":      "tcping",
				"samples": []map[string]any{
					{"seq": 1, "success": true, "latency_ms": 10.0},
					{"seq": 2, "success": false, "error": "timeout"},
					{"seq": 3, "success": true, "latency_ms": 30.0},
				},
			},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}
	var accepted struct {
		OK       bool `json:"ok"`
		Accepted int  `json:"accepted"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted response: %v", err)
	}
	if !accepted.OK || accepted.Accepted != 1 {
		t.Fatalf("accepted response = %+v, want ok=true accepted=1", accepted)
	}

	latency, err := store.NodeLatency(ctx, "example-node-a", latencyWindow{Name: "1h", Samples: 36, Step: 2 * time.Minute})
	if err != nil {
		t.Fatalf("node latency: %v", err)
	}
	if len(latency.Points) != 1 {
		t.Fatalf("latency points len = %d, want 1 posted round", len(latency.Points))
	}
	point := latency.Points[0]
	if point.TargetID != "google-dns" || point.MedianMS == nil || *point.MedianMS != 20 || math.Abs(point.LossPercent-100.0/3.0) > 0.000001 {
		t.Fatalf("latency point = %+v, want posted google-dns median=20 loss=33.333", point)
	}
	var sampleRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples`).Scan(&sampleRows); err != nil {
		t.Fatalf("count samples: %v", err)
	}
	if sampleRows != 3 {
		t.Fatalf("probe sample rows = %d, want 3 raw samples", sampleRows)
	}
}
func TestAgentProbeResultsRejectsStaleConfigVersionWithoutPartialWrite(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	version, err := store.ProbeConfigVersion(ctx)
	if err != nil {
		t.Fatalf("probe config version: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store})
	post := func(payload string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", strings.NewReader(payload))
		request.Header.Set("X-Node-ID", "example-node-a")
		request.Header.Set("Authorization", "Bearer test-agent-token")
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	now := time.Now().UTC().Truncate(time.Second).Unix()
	currentPayload := `{"config_version":` + strconv.FormatInt(version, 10) + `,"rounds":[{"round_id":"current-version-a","target_id":"google-dns","ts":` + strconv.FormatInt(now, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5}]}]}`
	if recorder := post(currentPayload); recorder.Code != http.StatusAccepted {
		t.Fatalf("current version status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}
	var roundsBefore, samplesBefore int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&roundsBefore); err != nil {
		t.Fatalf("count rounds before stale write: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples`).Scan(&samplesBefore); err != nil {
		t.Fatalf("count samples before stale write: %v", err)
	}
	if roundsBefore != 1 || samplesBefore != 1 {
		t.Fatalf("initial accepted write counts rounds/samples=%d/%d, want 1/1", roundsBefore, samplesBefore)
	}
	displayOrder := 99
	if _, err := store.UpdateAdminProbeTarget(ctx, "google-dns", AdminProbeTargetUpdateRequest{DisplayOrder: &displayOrder}); err != nil {
		t.Fatalf("commit probe config mutation: %v", err)
	}
	newVersion, err := store.ProbeConfigVersion(ctx)
	if err != nil {
		t.Fatalf("probe config version after mutation: %v", err)
	}
	if newVersion == version {
		t.Fatalf("probe config mutation committed without bumping version %d", newVersion)
	}
	stalePayload := `{"config_version":` + strconv.FormatInt(version, 10) + `,"rounds":[` +
		`{"round_id":"stale-version-a","target_id":"google-dns","ts":` + strconv.FormatInt(now+1, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":13.5}]},` +
		`{"round_id":"stale-version-b","target_id":"cloudflare-dns","ts":` + strconv.FormatInt(now+1, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":14.5}]}` +
		`]}`
	staleRecorder := post(stalePayload)
	if staleRecorder.Code != http.StatusConflict {
		t.Fatalf("stale version status = %d, want 409; body=%s", staleRecorder.Code, staleRecorder.Body.String())
	}
	if !strings.Contains(staleRecorder.Body.String(), "stale_probe_config") {
		t.Fatalf("stale response body = %s, want recognizable stale_probe_config error", staleRecorder.Body.String())
	}
	var roundsAfter, samplesAfter int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&roundsAfter); err != nil {
		t.Fatalf("count rounds after stale write: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples`).Scan(&samplesAfter); err != nil {
		t.Fatalf("count samples after stale write: %v", err)
	}
	if roundsAfter != roundsBefore || samplesAfter != samplesBefore {
		t.Fatalf("stale batch wrote partial rows rounds/samples %d/%d -> %d/%d", roundsBefore, samplesBefore, roundsAfter, samplesAfter)
	}
	// The previous Agent build used top-level "version". Keep that rolling-upgrade
	// alias version-checked too, rather than silently treating it as legacy zero.
	futurePayload := `{"version":999999,"rounds":[{"round_id":"future-version-a","target_id":"google-dns","ts":` + strconv.FormatInt(now+2, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":15.0}]}]}`
	if recorder := post(futurePayload); recorder.Code != http.StatusConflict {
		t.Fatalf("future version status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&roundsAfter); err != nil {
		t.Fatalf("count rounds after future-version write: %v", err)
	}
	if roundsAfter != roundsBefore {
		t.Fatalf("future-version batch wrote rounds %d -> %d", roundsBefore, roundsAfter)
	}
	currentAgainPayload := `{"config_version":` + strconv.FormatInt(newVersion, 10) + `,"rounds":[{"round_id":"current-version-b","target_id":"cloudflare-dns","ts":` + strconv.FormatInt(now+2, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":15.5}]}]}`
	if recorder := post(currentAgainPayload); recorder.Code != http.StatusAccepted {
		t.Fatalf("new current version status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}
}
func TestAgentProbeResultsVersionZeroUsesCurrentConfigValidationAtomically(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	count := 1
	if _, err := store.UpdateAdminProbeTarget(ctx, "google-dns", AdminProbeTargetUpdateRequest{Count: &count}); err != nil {
		t.Fatalf("shrink google-dns count: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store})
	post := func(payload string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", strings.NewReader(payload))
		request.Header.Set("X-Node-ID", "example-node-a")
		request.Header.Set("Authorization", "Bearer test-agent-token")
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	now := time.Now().UTC().Truncate(time.Second).Unix()
	rejectedLegacyPayload := `{"config_version":0,"rounds":[` +
		`{"round_id":"legacy-zero-valid-first","target_id":"cloudflare-dns","ts":` + strconv.FormatInt(now, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":11.5}]},` +
		`{"round_id":"legacy-zero-too-many","target_id":"google-dns","ts":` + strconv.FormatInt(now, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5},{"seq":2,"success":true,"latency_ms":13.5}]}` +
		`]}`
	if recorder := post(rejectedLegacyPayload); recorder.Code != http.StatusBadRequest {
		t.Fatalf("legacy config_version=0 invalid current target status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	var rounds int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&rounds); err != nil {
		t.Fatalf("count rounds after rejected legacy zero batch: %v", err)
	}
	if rounds != 0 {
		t.Fatalf("legacy config_version=0 invalid batch wrote %d rounds, want 0", rounds)
	}
	acceptedLegacyPayload := `{"config_version":0,"rounds":[{"round_id":"legacy-zero-current-valid","target_id":"google-dns","ts":` + strconv.FormatInt(now+1, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5}]}]}`
	if recorder := post(acceptedLegacyPayload); recorder.Code != http.StatusAccepted {
		t.Fatalf("legacy config_version=0 current-valid status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}
}
func TestAdminProbeTargetMutationsBumpProbeConfigVersionInStoreTransaction(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	version, err := store.ProbeConfigVersion(ctx)
	if err != nil {
		t.Fatalf("initial probe config version: %v", err)
	}
	assertBumped := func(action string) {
		t.Helper()
		current, err := store.ProbeConfigVersion(ctx)
		if err != nil {
			t.Fatalf("probe config version after %s: %v", action, err)
		}
		if current <= version {
			t.Fatalf("probe config version after %s = %d, want > %d", action, current, version)
		}
		version = current
	}
	port := 443
	if _, err := store.CreateAdminProbeTarget(ctx, AdminProbeTargetCreateRequest{
		ID:          "version-bump-target",
		Name:        "Version Bump Target",
		Type:        "tcping",
		Address:     "203.0.113.10",
		Port:        adminOptionalInt64{Set: true, Valid: true, Value: int64(port)},
		Count:       1,
		TimeoutMS:   minProbeTargetTimeoutMS,
		IntervalSec: minProbeTargetIntervalSec,
		Assignments: []AdminProbeTargetAssignmentUpdate{{NodeID: "example-node-a", Enabled: true}},
	}); err != nil {
		t.Fatalf("create probe target: %v", err)
	}
	assertBumped("create")
	displayOrder := 123
	if _, err := store.UpdateAdminProbeTarget(ctx, "version-bump-target", AdminProbeTargetUpdateRequest{DisplayOrder: &displayOrder}); err != nil {
		t.Fatalf("update probe target: %v", err)
	}
	assertBumped("update")
	if err := store.DeleteAdminProbeTarget(ctx, "version-bump-target"); err != nil {
		t.Fatalf("delete probe target: %v", err)
	}
	assertBumped("delete")
}
func TestAgentProbeResultsStoresLatencyWithoutProbeAlertNotification(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	telegram := newTelegramTestCapture(t)
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	zeroDuration := 0
	if _, err := store.UpdateAdminAlertRule(ctx, "cpu_high", AdminAlertRuleUpdateRequest{DurationSec: &zeroDuration}); err != nil {
		t.Fatalf("set cpu rule duration: %v", err)
	}

	handler := NewHandler(telegram.handlerOptions(store))
	now := time.Now().UTC().Truncate(time.Second)
	postAgentHeartbeat(t, handler, now.Unix(), "online")

	payload := []byte(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(now.Add(time.Second).Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":false,"error":"timeout"},{"seq":2,"success":false,"error":"timeout"}]}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("probe results status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'example-node-a'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "online" {
		t.Fatalf("node status = %q, want online because probe alert rules were removed", status)
	}
	paths, forms, errors := telegram.waitForCalls(t, 0)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors = %+v", errors)
	}
	if len(paths) != 0 || len(forms) != 0 {
		t.Fatalf("telegram calls paths=%+v forms=%+v, want no probe alert notification", paths, forms)
	}
}
func TestAgentProbeResultsKeepsTimeoutLossWithoutLatency(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store})
	now := time.Now().UTC().Truncate(time.Second)
	postAgentHeartbeat(t, handler, now.Unix(), "online")

	payload := []byte(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(now.Add(time.Second).Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":false,"latency_ms":2400,"error":"timeout"},{"seq":2,"success":false,"latency_ms":2600,"error":"timeout"}]}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("probe results status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var received int
	var loss float64
	var avg, median sql.NullFloat64
	if err := store.db.QueryRowContext(ctx, `SELECT received, loss_percent, avg_ms, median_ms FROM probe_rounds WHERE node_id = 'example-node-a' AND target_id = 'google-dns' ORDER BY id DESC LIMIT 1`).Scan(&received, &loss, &avg, &median); err != nil {
		t.Fatalf("query probe round: %v", err)
	}
	if received != 0 || loss != 100 || avg.Valid || median.Valid {
		t.Fatalf("round received/loss/avg/median = %d/%.2f/%+v/%+v, want 0/100/null/null", received, loss, avg, median)
	}
}
func TestAgentProbeResultsCapsOverFiveSecondSamplesAndCountsTimeoutLoss(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store})
	now := time.Now().UTC().Truncate(time.Second)
	payload := []byte(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(now.Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":7600}]}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("probe results status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var received int
	var loss float64
	var avg sql.NullFloat64
	if err := store.db.QueryRowContext(ctx, `SELECT received, loss_percent, avg_ms FROM probe_rounds WHERE target_id = 'google-dns' ORDER BY id DESC LIMIT 1`).Scan(&received, &loss, &avg); err != nil {
		t.Fatalf("query probe round: %v", err)
	}
	if received != 0 || loss != 100 || avg.Valid {
		t.Fatalf("received/loss/avg = %d/%.0f/%+v, want 0/100/null", received, loss, avg)
	}
}
func TestLocalProbeObservationUsesConfiguredTimeoutWithHardFiveSecondCap(t *testing.T) {
	if got := localLatencyObservationTimeout(12 * time.Second); got != 5*time.Second {
		t.Fatalf("observation timeout = %s, want 5s", got)
	}
	if got := localLatencyObservationTimeout(500 * time.Millisecond); got != 500*time.Millisecond {
		t.Fatalf("short target observation timeout = %s, want configured timeout", got)
	}
}
func TestAgentProbeResultsSuccessfulHighLatencyDoesNotChangeStatus(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store})
	now := time.Now().UTC().Truncate(time.Second)
	postAgentHeartbeat(t, handler, now.Unix(), "online")

	payload := []byte(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(now.Add(time.Second).Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":900},{"seq":2,"success":true,"latency_ms":950},{"seq":3,"success":true,"latency_ms":1000}]}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("probe results status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'example-node-a'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "online" {
		t.Fatalf("node status = %q, want online because probe latency alert rule was removed", status)
	}
}
func TestAgentProbeResultsFailedSamplesDoNotWarn(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store})
	now := time.Now().UTC().Truncate(time.Second)
	postAgentHeartbeat(t, handler, now.Unix(), "online")

	payload := []byte(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(now.Add(time.Second).Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":false,"error":"timeout"},{"seq":2,"success":false,"error":"timeout"}]}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("probe results status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'example-node-a'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "online" {
		t.Fatalf("node status = %q, want online when probe warning rules are removed", status)
	}
}
func TestAgentStateResourceRuleMarksWarningAndDispatchesProbeUnhealthy(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	telegram := newTelegramTestCapture(t)
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	zeroDuration := 0
	if _, err := store.UpdateAdminAlertRule(ctx, "cpu_high", AdminAlertRuleUpdateRequest{DurationSec: &zeroDuration}); err != nil {
		t.Fatalf("set cpu rule duration: %v", err)
	}

	handler := NewHandler(telegram.handlerOptions(store))
	reconcile := handler.(interface{ reconcileNotifications(context.Context) })
	reconcile.reconcileNotifications(ctx)
	now := time.Now().UTC().Truncate(time.Second)
	postAgentHeartbeat(t, handler, now.Unix(), "online")
	body := map[string]any{
		"ts":                  now.Add(time.Second).Unix(),
		"cpu_percent":         96.5,
		"memory_used_bytes":   int64(4 * 1024 * 1024 * 1024),
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
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("state status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'example-node-a'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "warning" {
		t.Fatalf("node status = %q, want warning after enabled CPU rule threshold is exceeded", status)
	}
	reconcile.reconcileNotifications(ctx)
	paths, forms, errors := telegram.waitForCalls(t, 1)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors = %+v", errors)
	}
	if len(paths) != 1 || len(forms) != 1 || !strings.Contains(forms[0], "CPU%E6%8C%81%E7%BB%AD%E5%8D%A0%E7%94%A8%E8%BF%87%E9%AB%98") {
		t.Fatalf("telegram request paths=%+v forms=%+v, want one CPU threshold notification", paths, forms)
	}
	assertTelegramFormsDoNotLeakCredential(t, forms, "telegram-bot-credential-value")
}
func TestAgentHeartbeatDoesNotClearExistingProbeWarning(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	freshSeen := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'warning', last_seen_at = ? WHERE id = 'example-node-a'`, freshSeen); err != nil {
		t.Fatalf("set warning status: %v", err)
	}

	postAgentHeartbeat(t, NewHandler(HandlerOptions{Store: store}), freshSeen+1, "online")

	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'example-node-a'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "warning" {
		t.Fatalf("node status = %q, want heartbeat online to preserve probe warning until a healthy probe clears it", status)
	}
}
func TestAgentProbeResultsDoNotClearResourceWarning(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	freshSeen := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'warning', last_seen_at = ? WHERE id = 'example-node-a'`, freshSeen); err != nil {
		t.Fatalf("set warning status: %v", err)
	}

	payload := []byte(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(freshSeen+1, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5},{"seq":2,"success":true,"latency_ms":13.5}]}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("probe results status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'example-node-a'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "warning" {
		t.Fatalf("node status = %q, want service probe results to preserve resource warning", status)
	}
}
func TestAgentProbeResultsRejectsUnknownTarget(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	payload := []byte(`{"rounds":[{"target_id":"not-enabled","ts":1782990000,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":1.2}]}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	var rounds int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&rounds); err != nil {
		t.Fatalf("count rounds: %v", err)
	}
	if rounds != 0 {
		t.Fatalf("probe rounds = %d, want no partial insert for unknown target", rounds)
	}
}
func TestAgentProbeResultsDeduplicateExactRetriesButKeepDistinctRoundsInSameSecond(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	payload := []byte(`{"rounds":[{"round_id":"round-a","target_id":"google-dns","ts":` + ts + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5},{"seq":2,"success":true,"latency_ms":13.5}]}]}`)
	post := func(payload []byte) {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
		request.Header.Set("X-Node-ID", "example-node-a")
		request.Header.Set("Authorization", "Bearer test-agent-token")
		request.Header.Set("Content-Type", "application/json")
		NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		post(payload)
	}
	distinctPayload := []byte(`{"rounds":[{"round_id":"round-b","target_id":"google-dns","ts":` + ts + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5},{"seq":2,"success":true,"latency_ms":13.5}]}]}`)
	post(distinctPayload)
	conflictingPayload := []byte(`{"rounds":[{"round_id":"round-a","target_id":"google-dns","ts":` + strconv.FormatInt(time.Now().UTC().Add(time.Second).Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":22.5}]}]}`)
	conflictRecorder := httptest.NewRecorder()
	conflictRequest := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(conflictingPayload))
	conflictRequest.Header.Set("X-Node-ID", "example-node-a")
	conflictRequest.Header.Set("Authorization", "Bearer test-agent-token")
	conflictRequest.Header.Set("Content-Type", "application/json")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(conflictRecorder, conflictRequest)
	if conflictRecorder.Code != http.StatusConflict || !strings.Contains(conflictRecorder.Body.String(), "probe_round_conflict") {
		t.Fatalf("conflicting reuse status = %d, want explicit 409; body=%s", conflictRecorder.Code, conflictRecorder.Body.String())
	}
	var rounds, samples int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&rounds); err != nil {
		t.Fatalf("count probe rounds: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples`).Scan(&samples); err != nil {
		t.Fatalf("count probe samples: %v", err)
	}
	if rounds != 2 || samples != 4 {
		t.Fatalf("probe retry/same-second distinct payload stored rounds=%d samples=%d, want 2/4", rounds, samples)
	}
}
func TestAgentProbeResultsRejectsTimestampSkewAndDuplicateSequence(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	tests := []struct {
		name    string
		payload string
	}{
		{name: "future timestamp", payload: `{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(time.Now().UTC().Add(6*time.Minute).Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5}]}]}`},
		{name: "duplicate sequence", payload: `{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(time.Now().UTC().Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5},{"seq":1,"success":true,"latency_ms":13.5}]}]}`},
		{name: "invalid round id", payload: `{"rounds":[{"round_id":"bad id!","target_id":"google-dns","ts":` + strconv.FormatInt(time.Now().UTC().Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5}]}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", strings.NewReader(tc.payload))
			request.Header.Set("X-Node-ID", "example-node-a")
			request.Header.Set("Authorization", "Bearer test-agent-token")
			request.Header.Set("Content-Type", "application/json")
			NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	var rounds int
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM probe_rounds`).Scan(&rounds); err != nil {
		t.Fatalf("count probe rounds: %v", err)
	}
	if rounds != 0 {
		t.Fatalf("probe rounds = %d, want no writes for invalid batches", rounds)
	}
}
func TestAgentProbeResultsRejectsProbeResourceLimitOverages(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store})
	post := func(payload string) int {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", strings.NewReader(payload))
		request.Header.Set("X-Node-ID", "example-node-a")
		request.Header.Set("Authorization", "Bearer test-agent-token")
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	now := time.Now().UTC().Unix()
	fourSamples := `[{"seq":1,"success":true,"latency_ms":1},{"seq":2,"success":true,"latency_ms":2},{"seq":3,"success":true,"latency_ms":3},{"seq":4,"success":true,"latency_ms":4}]`
	if got := post(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(now, 10) + `,"type":"tcping","samples":` + fourSamples + `}]}`); got != http.StatusBadRequest {
		t.Fatalf("status for samples above target count=%d, want 400", got)
	}

	count := 32
	timeoutMS := 100
	intervalSec := 5
	if _, err := store.UpdateAdminProbeTarget(ctx, "google-dns", AdminProbeTargetUpdateRequest{Count: &count, TimeoutMS: &timeoutMS, IntervalSec: &intervalSec}); err != nil {
		t.Fatalf("raise google-dns probe count for max-sample test: %v", err)
	}
	samples := make([]string, 0, maxProbeTargetCount+1)
	for seq := 1; seq <= maxProbeTargetCount+1; seq++ {
		samples = append(samples, fmt.Sprintf(`{"seq":%d,"success":true,"latency_ms":1}`, seq))
	}
	if got := post(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(now, 10) + `,"type":"tcping","samples":[` + strings.Join(samples, ",") + `]}]}`); got != http.StatusBadRequest {
		t.Fatalf("status for samples above hard count=%d, want 400", got)
	}

	rounds := make([]string, 0, maxAgentProbeRounds+1)
	for index := 0; index < maxAgentProbeRounds+1; index++ {
		rounds = append(rounds, `{"target_id":"google-dns","ts":`+strconv.FormatInt(now, 10)+`,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":1}]}`)
	}
	if got := post(`{"rounds":[` + strings.Join(rounds, ",") + `]}`); got != http.StatusBadRequest {
		t.Fatalf("status for rounds above target cap=%d, want 400", got)
	}
	var written int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&written); err != nil {
		t.Fatalf("count probe rounds after rejected overages: %v", err)
	}
	if written != 0 {
		t.Fatalf("probe rounds written after rejected overages=%d, want 0", written)
	}
}
