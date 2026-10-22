package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHealthEndpoint(t *testing.T) {
	handler := NewHandler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var body map[string]bool
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	if !body["ok"] {
		t.Fatalf("health ok = false, want true")
	}
}

func TestReadyEndpointChecksSQLiteStore(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/ready", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body map[string]bool
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode ready body: %v", err)
	}
	if !body["ok"] {
		t.Fatalf("ready ok = false, want true")
	}
	var readinessTableCount int
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'readiness_probe'`).Scan(&readinessTableCount); err != nil {
		t.Fatalf("inspect readiness schema: %v", err)
	}
	if readinessTableCount != 0 {
		t.Fatalf("fresh schema created retired readiness_probe table")
	}
}

func TestReadyEndpointRejectsMissingStore(t *testing.T) {
	handler := NewHandler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing-store readiness status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSecurityHeadersAndAdminNoStore(t *testing.T) {
	handler := NewHandler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/login", strings.NewReader(`{}`))

	handler.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := recorder.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}
	if got := recorder.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
	if got := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(got, "script-src 'self'") || !strings.Contains(got, "frame-ancestors 'none'") || !strings.Contains(got, "connect-src 'self'") || strings.Contains(got, "connect-src ws: wss:") {
		t.Fatalf("Content-Security-Policy = %q, want restrictive script/frame/connect policy", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store for admin API", got)
	}
}

func TestSecurityHeadersSetHSTSOnlyForTrustedHTTPS(t *testing.T) {
	handler := NewHandler()

	proxiedRecorder := httptest.NewRecorder()
	proxiedRequest := httptest.NewRequest(http.MethodGet, "/health", nil)
	proxiedRequest.RemoteAddr = "127.0.0.1:12345"
	proxiedRequest.Header.Set("X-Forwarded-Proto", "https")
	handler.ServeHTTP(proxiedRecorder, proxiedRequest)
	if got := proxiedRecorder.Header().Get("Strict-Transport-Security"); got != "max-age=31536000" {
		t.Fatalf("trusted HTTPS HSTS = %q, want max-age=31536000", got)
	}

	spoofedRecorder := httptest.NewRecorder()
	spoofedRequest := httptest.NewRequest(http.MethodGet, "/health", nil)
	spoofedRequest.RemoteAddr = "192.0.2.10:12345"
	spoofedRequest.Header.Set("X-Forwarded-Proto", "https")
	handler.ServeHTTP(spoofedRecorder, spoofedRequest)
	if got := spoofedRecorder.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("untrusted forwarded HTTPS HSTS = %q, want empty", got)
	}
}

func TestCredentialedPublicResponseIsNotCacheable(t *testing.T) {
	handler := NewHandler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/summary", nil)
	request.Header.Set("X-Admin-Token", "session-token")
	handler.ServeHTTP(recorder, request)
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store for credentialed public API", got)
	}
	if got := recorder.Header().Values("Vary"); len(got) == 0 || !strings.Contains(strings.Join(got, ","), "X-Admin-Token") {
		t.Fatalf("Vary = %q, want X-Admin-Token", got)
	}
}

func TestSummaryEndpointReturnsMockHomeCardsWithoutSecrets(t *testing.T) {
	handler := newMockHandler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/summary", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	raw := recorder.Body.String()
	var summary SummaryResponse
	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if len(summary.Nodes) != 11 {
		t.Fatalf("nodes len = %d, want 11 Kulin-style mock cards", len(summary.Nodes))
	}
	if summary.Nodes[0].DisplayName != "Mechrevo" {
		t.Fatalf("first node = %q, want Mechrevo", summary.Nodes[0].DisplayName)
	}
	if summary.Nodes[0].OS != "windows" {
		t.Fatalf("first node os = %q, want windows", summary.Nodes[0].OS)
	}
	if summary.Nodes[0].CPUCores == nil || *summary.Nodes[0].CPUCores != 16 {
		t.Fatalf("first node cpu cores = %v, want 16", summary.Nodes[0].CPUCores)
	}
	if summary.Nodes[0].CPUModel == "" || summary.Nodes[0].Virtualization == "" {
		t.Fatalf("first node details = cpu_model %q virtualization %q, want public host details", summary.Nodes[0].CPUModel, summary.Nodes[0].Virtualization)
	}
	if summary.Nodes[0].LatencySummary != nil {
		t.Fatalf("first node should omit latency summary like the reference homepage")
	}
	if summary.Nodes[0].ExpiryLabel == "" {
		t.Fatalf("first node should include a Kulin-style expiry label")
	}
	if len(summary.LatencyPoints) != 0 {
		t.Fatalf("summary latency points len = %d, want 0 because details use dedicated websocket feeds", len(summary.LatencyPoints))
	}
	if len(summary.Services) == 0 || summary.Services[0].Name == "" || summary.Services[0].AssignedNodeCount == 0 {
		t.Fatalf("summary services = %+v, want public monitor service status", summary.Services)
	}
	if strings.Contains(raw, `"address"`) || strings.Contains(raw, `"port"`) || strings.Contains(raw, "203.0.113.5") || strings.Contains(raw, "cq-unicom.example") {
		t.Fatalf("public summary leaked probe destination details: %s", raw)
	}

	if strings.Contains(strings.ToLower(raw), "token") || strings.Contains(strings.ToLower(raw), "secret") {
		t.Fatalf("public summary leaked token/secret wording: %s", raw)
	}
}

func TestSummaryWebSocketPublishesAgentStateUpdates(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	server := httptest.NewServer(NewHandler(HandlerOptions{Store: store}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/public/v1/summary/ws"
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("open summary websocket: %v", err)
	}
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	defer conn.Close()

	_, initial, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read initial websocket summary: %v", err)
	}
	if !strings.Contains(string(initial), `"nodes"`) {
		t.Fatalf("initial websocket message = %q, want summary payload", string(initial))
	}

	payload := []byte(`{"ts":` + strconv.FormatInt(time.Now().UTC().Unix(), 10) + `,"cpu_percent":12.5,"load1":0.42,"load5":0.35,"load15":0.28,"memory_used_bytes":100,"memory_total_bytes":200,"disk_used_bytes":300,"disk_total_bytes":400,"net_in_total_bytes":1000,"net_out_total_bytes":2000,"net_in_speed_bps":4321,"net_out_speed_bps":8765,"uptime_seconds":60}`)
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/agent/v1/state", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new state request: %v", err)
	}
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	stateResponse, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("post agent state: %v", err)
	}
	defer stateResponse.Body.Close()
	if stateResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("state status = %d, want 202; body=%s", stateResponse.StatusCode, readAllString(t, stateResponse.Body))
	}

	_, update, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read updated websocket summary: %v", err)
	}
	if !strings.Contains(string(update), `"net_in_speed_bps":4321`) || !strings.Contains(string(update), `"net_out_speed_bps":8765`) || !strings.Contains(string(update), `"load1":0.42`) || !strings.Contains(string(update), `"uptime_seconds":60`) {
		t.Fatalf("websocket update = %q, want latest agent speeds and live facts", string(update))
	}
}

func TestAgentHeartbeatServesStaleSummaryThenRefreshesInBackground(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	server := httptest.NewServer(NewHandler(HandlerOptions{Store: store}))
	defer server.Close()

	initialResponse, err := server.Client().Get(server.URL + "/api/public/v1/summary")
	if err != nil {
		t.Fatalf("get initial summary: %v", err)
	}
	initial := readAllString(t, initialResponse.Body)
	initialResponse.Body.Close()
	if !strings.Contains(initial, `"status":"no_data"`) {
		t.Fatalf("initial summary = %s, want cached no-data node", initial)
	}

	payload := []byte(`{"ts":` + strconv.FormatInt(time.Now().UTC().Unix(), 10) + `,"status":"online","agent_version":"agent-test"}`)
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/agent/v1/heartbeat", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new heartbeat request: %v", err)
	}
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	heartbeatResponse, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("post heartbeat: %v", err)
	}
	defer heartbeatResponse.Body.Close()
	if heartbeatResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("heartbeat status = %d, want 202; body=%s", heartbeatResponse.StatusCode, readAllString(t, heartbeatResponse.Body))
	}

	updatedResponse, err := server.Client().Get(server.URL + "/api/public/v1/summary")
	if err != nil {
		t.Fatalf("get updated summary: %v", err)
	}
	updated := readAllString(t, updatedResponse.Body)
	updatedResponse.Body.Close()
	if !strings.Contains(updated, `"status":"no_data"`) {
		t.Fatalf("immediate summary = %s, want last complete snapshot", updated)
	}
	assertSummaryStatusEventually(t, server.URL, "online")
}

func TestAgentPresenceWebSocketDoesNotOverrideSummaryLiveness(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	if err := store.RecordAgentHeartbeat(ctx, "example-node-a", time.Now().UTC(), "online", "test-agent"); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	server := httptest.NewServer(NewHandler(HandlerOptions{Store: store}))
	defer server.Close()
	assertSummaryStatusEventually(t, server.URL, "online")

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/agent/v1/presence/ws"
	header := http.Header{}
	header.Set("X-Node-ID", "example-node-a")
	header.Set("Authorization", "Bearer test-agent-token")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial presence websocket: %v", err)
	}

	assertSummaryStatusEventually(t, server.URL, "online")
	conn.Close()
	assertSummaryStatusEventually(t, server.URL, "online")
}

func TestAgentPresenceWebSocketDoesNotForceStaleSummaryOnline(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	staleSeen := time.Now().UTC().Add(-nodeHeartbeatOfflineAfter - time.Second).Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'example-node-a'`, staleSeen); err != nil {
		t.Fatalf("set stale heartbeat: %v", err)
	}
	server := httptest.NewServer(NewHandler(HandlerOptions{Store: store}))
	defer server.Close()
	assertSummaryStatusEventually(t, server.URL, "offline")

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/agent/v1/presence/ws"
	header := http.Header{}
	header.Set("X-Node-ID", "example-node-a")
	header.Set("Authorization", "Bearer test-agent-token")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial presence websocket: %v", err)
	}
	defer conn.Close()
	assertSummaryStatusEventually(t, server.URL, "offline")
}

func assertSummaryStatusEventually(t *testing.T, baseURL, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		response, err := http.Get(baseURL + "/api/public/v1/summary")
		if err != nil {
			t.Fatalf("get summary: %v", err)
		}
		body := readAllString(t, response.Body)
		response.Body.Close()
		if strings.Contains(body, `"status":"`+want+`"`) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("summary = %s, want status %s", body, want)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestNodeStateWebSocketPublishesAgentStateUpdates(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	baseTS := time.Now().UTC().Add(-5 * time.Minute).Unix()
	for index := 0; index < 6; index++ {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO state_samples (node_id, ts, cpu_percent, memory_used_bytes, memory_total_bytes, disk_used_bytes, disk_total_bytes, net_in_total_bytes, net_out_total_bytes, net_in_speed_bps, net_out_speed_bps, uptime_seconds)
			VALUES ('example-node-a', ?, ?, 100, 200, 300, 400, 1000, 2000, 2468, 8642, 60)
		`, baseTS+int64(index*30), 10+float64(index)); err != nil {
			t.Fatalf("insert historical state sample: %v", err)
		}
	}

	server := httptest.NewServer(NewHandler(HandlerOptions{Store: store}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/public/v1/nodes/example-node-a/state/ws?range=1d"
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("open node state websocket: %v", err)
	}
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	defer conn.Close()

	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read initial node state websocket message: %v", err)
	}

	payload := []byte(`{"ts":` + strconv.FormatInt(time.Now().UTC().Unix(), 10) + `,"cpu_percent":22.5,"memory_used_bytes":100,"memory_total_bytes":200,"disk_used_bytes":300,"disk_total_bytes":400,"net_in_total_bytes":1000,"net_out_total_bytes":2000,"net_in_speed_bps":2468,"net_out_speed_bps":8642,"uptime_seconds":60}`)
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/agent/v1/state", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new state request: %v", err)
	}
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	stateResponse, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("post agent state: %v", err)
	}
	defer stateResponse.Body.Close()
	if stateResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("state status = %d, want 202; body=%s", stateResponse.StatusCode, readAllString(t, stateResponse.Body))
	}

	_, update, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read updated node state websocket message: %v", err)
	}
	var stateUpdate StateResponse
	if err := json.Unmarshal(update, &stateUpdate); err != nil {
		t.Fatalf("decode node state websocket update: %v", err)
	}
	if stateUpdate.NodeID != "example-node-a" || !statePointsContainNetOutSpeed(stateUpdate.Points, 8642) {
		t.Fatalf("node state websocket update = %q, want latest state point", string(update))
	}
	if got := len(stateUpdate.Points); got < 4 {
		t.Fatalf("node state websocket update points = %d, want 30-second state buckets rather than latency buckets", got)
	}
}

func TestLatencyWebSocketsPublishAgentProbeResults(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	server := httptest.NewServer(NewHandler(HandlerOptions{Store: store}))
	defer server.Close()

	nodeConn, nodeResponse, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/public/v1/nodes/example-node-a/latency/ws?range=1h", nil)
	if err != nil {
		t.Fatalf("open node latency websocket: %v", err)
	}
	if nodeResponse != nil && nodeResponse.Body != nil {
		defer nodeResponse.Body.Close()
	}
	defer nodeConn.Close()
	if _, _, err := nodeConn.ReadMessage(); err != nil {
		t.Fatalf("read initial node latency websocket message: %v", err)
	}

	body := map[string]any{
		"rounds": []map[string]any{{
			"target_id": "google-dns",
			"ts":        time.Now().UTC().Unix(),
			"type":      "tcping",
			"samples": []map[string]any{
				{"seq": 1, "success": true, "latency_ms": 10.0},
				{"seq": 2, "success": true, "latency_ms": 30.0},
			},
		}},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal probe results: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/agent/v1/probe-results", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new probe request: %v", err)
	}
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	probeResponse, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("post probe results: %v", err)
	}
	defer probeResponse.Body.Close()
	if probeResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("probe status = %d, want 202; body=%s", probeResponse.StatusCode, readAllString(t, probeResponse.Body))
	}

	_, nodeUpdate, err := nodeConn.ReadMessage()
	if err != nil {
		t.Fatalf("read node latency update: %v", err)
	}
	var nodeLatencyUpdate LatencyResponse
	if err := json.Unmarshal(nodeUpdate, &nodeLatencyUpdate); err != nil {
		t.Fatalf("decode node latency update: %v", err)
	}
	if !latencyPointsContainAverage(nodeLatencyUpdate.Points, "google-dns", 20) {
		t.Fatalf("node latency websocket update = %q, want posted probe average", string(nodeUpdate))
	}
}

func TestRemovedServiceLatencyEndpointsAreGone(t *testing.T) {
	handler := newMockHandler()
	for _, path := range []string{
		"/api/public/v1/services/google/latency?range=1d",
		"/api/public/v1/services/google/latency/ws?range=1h",
		"/api/public/v1/services/latency?range=1d",
		"/api/public/v1/services/latency?range=1h",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, recorder.Code)
		}
	}
}

func TestNodeLatencyEndpointReturnsKulinStyleMonitorTargets(t *testing.T) {
	handler := newMockHandler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/nodes/example-harbor/latency?range=1d", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response LatencyResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode latency response: %v", err)
	}

	seen := map[string]bool{}
	for _, point := range response.Points {
		seen[point.TargetName] = true
	}
	wantNames := []string{"重庆联通", "重庆移动", "重庆电信", "DC5", "Google", "DC2", "DC1", "Akari TW", "Akari JP", "Akari HK", "Example Node A", "Example Compute", "Example Node B"}
	for _, name := range wantNames {
		if !seen[name] {
			t.Fatalf("missing Kulin-style monitor target %q; saw=%v", name, seen)
		}
	}
	if len(seen) != len(wantNames) {
		t.Fatalf("target count = %d, want %d; saw=%v", len(seen), len(wantNames), seen)
	}
}

func TestNodeLatencyEndpointUsesRangeSpecificWindows(t *testing.T) {
	realtime := requestLatency(t, "/api/public/v1/nodes/example-harbor/latency?range=1h")
	oneDay := requestLatency(t, "/api/public/v1/nodes/example-harbor/latency?range=1d")
	sevenDays := requestLatency(t, "/api/public/v1/nodes/example-harbor/latency?range=7d")
	thirtyDays := requestLatency(t, "/api/public/v1/nodes/example-harbor/latency?range=30d")

	if got := len(uniquePointTimes(realtime.Points)); got != 20 {
		t.Fatalf("1h timestamps = %d, want 20 realtime three-minute buckets", got)
	}
	if got := len(uniquePointTimes(oneDay.Points)); got != 1440 {
		t.Fatalf("1d timestamps = %d, want 1440 one-minute samples", got)
	}
	if got := len(uniquePointTimes(sevenDays.Points)); got != 1440 {
		t.Fatalf("7d timestamps = %d, want 1440 seven-minute samples", got)
	}
	if got := len(uniquePointTimes(thirtyDays.Points)); got != 1440 {
		t.Fatalf("30d timestamps = %d, want 1440 thirty-minute samples", got)
	}

	if pointSpan(t, oneDay.Points) < 23*time.Hour {
		t.Fatalf("1d window span = %s, want roughly one day", pointSpan(t, oneDay.Points))
	}
	if pointSpan(t, sevenDays.Points) < 6*24*time.Hour {
		t.Fatalf("7d window span = %s, want roughly seven days", pointSpan(t, sevenDays.Points))
	}
	if pointSpan(t, thirtyDays.Points) < 29*24*time.Hour {
		t.Fatalf("30d window span = %s, want roughly thirty days", pointSpan(t, thirtyDays.Points))
	}
}

func TestNodeStateEndpointUsesKulinHistoricalWindows(t *testing.T) {
	oneDay := requestState(t, "/api/public/v1/nodes/example-node-a/state?range=1d")
	sevenDays := requestState(t, "/api/public/v1/nodes/example-node-a/state?range=7d")
	thirtyDays := requestState(t, "/api/public/v1/nodes/example-node-a/state?range=30d")

	if got := len(oneDay.Points); got != 2880 {
		t.Fatalf("1d state points = %d, want 2880 thirty-second Kulin samples", got)
	}
	if got := len(sevenDays.Points); got != 336 {
		t.Fatalf("7d state points = %d, want Kulin 336 thirty-minute samples", got)
	}
	if got := len(thirtyDays.Points); got != 360 {
		t.Fatalf("30d state points = %d, want Kulin 360 two-hour samples", got)
	}

	if pointSpan(t, statePointsAsLatency(oneDay.Points)) < 23*time.Hour {
		t.Fatalf("1d state span = %s, want roughly one day", pointSpan(t, statePointsAsLatency(oneDay.Points)))
	}
}

func TestNodeLatencyEndpointPreservesLossOnlyPointsAsNullLatency(t *testing.T) {
	handler := newMockHandler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/nodes/example-node-a/latency?range=1h", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response LatencyResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode latency response: %v", err)
	}
	if response.NodeID != "example-node-a" {
		t.Fatalf("node_id = %q, want example-node-a", response.NodeID)
	}
	if response.Range != "1h" {
		t.Fatalf("range = %q, want 1h", response.Range)
	}

	var sawLossOnly bool
	for _, point := range response.Points {
		if point.TargetID == "telegram-dc1" && point.LossPercent == 100 {
			sawLossOnly = true
			if point.MedianMS != nil {
				t.Fatalf("100%% loss point median = %v, want nil", *point.MedianMS)
			}
		}
	}
	if !sawLossOnly {
		t.Fatal("expected at least one telegram-dc1 100% loss point")
	}
}

func requestLatency(t *testing.T, path string) LatencyResponse {
	t.Helper()
	const adminToken = "history-test-admin"
	handler := newMockHandler(HandlerOptions{AdminPasswordHash: testAdminPasswordHash(adminToken)})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if strings.Contains(path, "range=7d") || strings.Contains(path, "range=30d") {
		request.Header.Set("X-Admin-Token", adminToken)
	}
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response LatencyResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode latency response: %v", err)
	}
	return response
}

func requestState(t *testing.T, path string) StateResponse {
	t.Helper()
	const adminToken = "history-test-admin"
	handler := newMockHandler(HandlerOptions{AdminPasswordHash: testAdminPasswordHash(adminToken)})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if strings.Contains(path, "range=7d") || strings.Contains(path, "range=30d") {
		request.Header.Set("X-Admin-Token", adminToken)
	}
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response StateResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode state response: %v", err)
	}
	return response
}

func statePointsAsLatency(points []StatePoint) []LatencyPoint {
	converted := make([]LatencyPoint, 0, len(points))
	for _, point := range points {
		converted = append(converted, LatencyPoint{TS: point.TS})
	}
	return converted
}

func uniquePointTimes(points []LatencyPoint) map[string]bool {
	times := map[string]bool{}
	for _, point := range points {
		times[point.TS] = true
	}
	return times
}

func pointSpan(t *testing.T, points []LatencyPoint) time.Duration {
	t.Helper()
	if len(points) == 0 {
		return 0
	}
	var first, last time.Time
	for index, point := range points {
		ts, err := time.Parse(time.RFC3339, point.TS)
		if err != nil {
			t.Fatalf("parse point timestamp %q: %v", point.TS, err)
		}
		if index == 0 || ts.Before(first) {
			first = ts
		}
		if index == 0 || ts.After(last) {
			last = ts
		}
	}
	return last.Sub(first)
}

func latencyPointsContainAverage(points []LatencyPoint, targetID string, average float64) bool {
	for _, point := range points {
		if point.TargetID == targetID && point.AvgMS != nil && *point.AvgMS == average {
			return true
		}
	}
	return false
}

func statePointsContainNetOutSpeed(points []StatePoint, speed float64) bool {
	for _, point := range points {
		if point.NetOutSpeedBps != nil && *point.NetOutSpeedBps == speed {
			return true
		}
	}
	return false
}

func TestStaticWebFallbackServesIndexForDashboardRoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<div id=\"root\">Zeno UI</div>"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log('asset')"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app-AbCd1234.js"), []byte("console.log('hashed')"), 0o644); err != nil {
		t.Fatalf("write hashed asset: %v", err)
	}

	handler := NewHandler(HandlerOptions{StaticDir: dir})

	assetRecorder := httptest.NewRecorder()
	handler.ServeHTTP(assetRecorder, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if assetRecorder.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want %d", assetRecorder.Code, http.StatusOK)
	}
	if !strings.Contains(assetRecorder.Body.String(), "asset") {
		t.Fatalf("asset body = %q, want asset content", assetRecorder.Body.String())
	}
	if got := assetRecorder.Header().Get("Cache-Control"); got != "public, max-age=300, must-revalidate" {
		t.Fatalf("asset cache-control = %q, want short cache for stable filename", got)
	}

	hashedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(hashedRecorder, httptest.NewRequest(http.MethodGet, "/assets/app-AbCd1234.js", nil))
	if got := hashedRecorder.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("hashed asset cache-control = %q, want immutable asset cache", got)
	}

	spaRecorder := httptest.NewRecorder()
	handler.ServeHTTP(spaRecorder, httptest.NewRequest(http.MethodGet, "/nodes/example-node-a", nil))
	if spaRecorder.Code != http.StatusOK {
		t.Fatalf("spa status = %d, want %d", spaRecorder.Code, http.StatusOK)
	}
	if !strings.Contains(spaRecorder.Body.String(), "Zeno UI") {
		t.Fatalf("spa body = %q, want index.html", spaRecorder.Body.String())
	}
	if got := spaRecorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("spa cache-control = %q, want no-store", got)
	}
}

func TestStaticWebFallbackServesOldReleaseAssets(t *testing.T) {
	installDir := t.TempDir()
	currentWeb := filepath.Join(installDir, "current", "web")
	oldAssets := filepath.Join(installDir, "releases", "zeno-old", "web", "assets")
	if err := os.MkdirAll(filepath.Join(currentWeb, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir current: %v", err)
	}
	if err := os.MkdirAll(oldAssets, 0o755); err != nil {
		t.Fatalf("mkdir old assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(currentWeb, "index.html"), []byte("current index"), 0o644); err != nil {
		t.Fatalf("write current index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldAssets, "stale.js"), []byte("console.log('old asset')"), 0o644); err != nil {
		t.Fatalf("write stale asset: %v", err)
	}

	handler := NewHandler(HandlerOptions{StaticDir: currentWeb})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/stale.js", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("stale asset status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !strings.Contains(recorder.Body.String(), "old asset") {
		t.Fatalf("stale asset body = %q, want old release asset", recorder.Body.String())
	}
}
