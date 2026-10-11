package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminNodeInstallCommandIssuesOneTimeEnrollmentWithoutRotatingActiveAgent(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "old-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	publicURL := "https://probe.example.com"
	if _, err := store.UpdateAdminSettings(ctx, AdminSettingsUpdateRequest{AgentControllerURL: &publicURL}); err != nil {
		t.Fatalf("set agent controller URL: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/example-node-a/install-command", nil)
	request.Host = "probe.example.com"
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminPasswordHash: testAdminPasswordHash("admin-pass"), AgentVersion: "testsha"}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		NodeID              string            `json:"node_id"`
		Command             string            `json:"command"`
		Commands            map[string]string `json:"commands"`
		EnrollmentExpiresAt string            `json:"enrollment_expires_at"`
		EnrollmentOneTime   bool              `json:"enrollment_one_time"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(recorder.Body.String())).Decode(&response); err != nil {
		t.Fatalf("decode install command: %v", err)
	}
	if response.NodeID != "example-node-a" {
		t.Fatalf("node_id = %q, want example-node-a", response.NodeID)
	}
	if !strings.Contains(response.Command, "https://zeno.siaoz.si/agent/install.sh") || !strings.Contains(response.Command, "bash -o pipefail") || !strings.Contains(response.Command, "ZENO_CONTROLLER_URL='https://probe.example.com'") || !strings.Contains(response.Command, "ZENO_NODE_ID='example-node-a'") || !strings.Contains(response.Command, "ZENO_AGENT_VERSION='testsha'") {
		t.Fatalf("install command missing proxied installer, pipefail, controller URL, node id, or version: %s", response.Command)
	}
	if !strings.Contains(response.Commands["macos"], "https://zeno.siaoz.si/agent/install.sh") || !strings.Contains(response.Commands["windows"], "https://zeno.siaoz.si/agent/install.ps1") || !strings.Contains(response.Commands["windows"], "$env:ZENO_AGENT_VERSION='testsha'") {
		t.Fatalf("install commands should include macOS and Windows proxied variants: %#v", response.Commands)
	}
	if !strings.Contains(response.Command, "ZENO_ENROLLMENT_TOKEN='") || strings.Contains(response.Command, "ZENO_AGENT_TOKEN='") || !strings.Contains(response.Command, "sudo env") {
		t.Fatalf("install command should use Zeno agent names and paths: %s", response.Command)
	}
	if !response.EnrollmentOneTime || response.EnrollmentExpiresAt == "" {
		t.Fatalf("install response should describe expiring one-time enrollment: %+v", response)
	}
	credential := extractQuotedInstallCredential(t, response.Command)
	if credential == "old-agent-token" || credential == "" {
		t.Fatalf("install command leaked or omitted enrollment credential: %q", credential)
	}
	allowed, err := store.AuthorizeAgent(ctx, "example-node-a", "old-agent-token")
	if err != nil || !allowed {
		t.Fatalf("existing runtime credential must remain active while enrollment is pending: allowed=%v err=%v", allowed, err)
	}
	allowed, err = store.AuthorizeAgent(ctx, "example-node-a", credential)
	if err != nil {
		t.Fatalf("authorize enrollment credential as runtime: %v", err)
	}
	if allowed {
		t.Fatal("one-time enrollment credential must not authorize Agent API")
	}

	secondRecorder := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/example-node-a/install-command", nil)
	secondRequest.Host = "probe.example.com"
	secondRequest.Header.Set("X-Forwarded-Proto", "https")
	secondRequest.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminPasswordHash: testAdminPasswordHash("admin-pass"), AgentVersion: "testsha"}).ServeHTTP(secondRecorder, secondRequest)
	if secondRecorder.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", secondRecorder.Code, secondRecorder.Body.String())
	}
	var secondResponse struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(secondRecorder.Body.String())).Decode(&secondResponse); err != nil {
		t.Fatalf("decode second install command: %v", err)
	}
	secondCredential := extractQuotedInstallCredential(t, secondResponse.Command)
	if secondCredential == credential || secondCredential == "" {
		t.Fatalf("second command must supersede the first enrollment: first=%q second=%q", credential, secondCredential)
	}
	if err := store.RedeemAgentEnrollment(ctx, "example-node-a", credential, strings.Repeat("a", 64)); !errors.Is(err, errAgentEnrollmentUnavailable) {
		t.Fatalf("superseded enrollment redemption error = %v, want unavailable", err)
	}
	newRuntimeToken := strings.Repeat("b", 64)
	if err := store.RedeemAgentEnrollment(ctx, "example-node-a", secondCredential, newRuntimeToken); err != nil {
		t.Fatalf("redeem current enrollment: %v", err)
	}
	if allowed, err := store.AuthorizeAgent(ctx, "example-node-a", newRuntimeToken); err != nil || !allowed {
		t.Fatalf("pending runtime credential should activate: allowed=%v err=%v", allowed, err)
	}
	if allowed, err := store.AuthorizeAgent(ctx, "example-node-a", "old-agent-token"); err != nil || allowed {
		t.Fatalf("old runtime credential should retire after activation: allowed=%v err=%v", allowed, err)
	}
}
func TestAdminNodeInstallCommandRejectsUnconfiguredRemoteHost(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "old-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/example-node-a/install-command", nil)
	request.Host = "attacker.example"
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminPasswordHash: testAdminPasswordHash("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "old-agent-token") || strings.Contains(recorder.Body.String(), "attacker.example") {
		t.Fatalf("rejected response leaked credential or untrusted host: %s", recorder.Body.String())
	}
}
func TestAdminNodeInstallCommandPrefersConfiguredAgentControllerURL(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "old-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	publicURL := "https://zeno.example.com"
	if _, err := store.UpdateAdminSettings(ctx, AdminSettingsUpdateRequest{AgentControllerURL: &publicURL}); err != nil {
		t.Fatalf("set agent controller URL: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/example-node-a/install-command", nil)
	request.Host = "admin.localhost:18980"
	request.Header.Set("X-Forwarded-Proto", "http")
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminPasswordHash: testAdminPasswordHash("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Command  string            `json:"command"`
		Commands map[string]string `json:"commands"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(recorder.Body.String())).Decode(&response); err != nil {
		t.Fatalf("decode install command: %v", err)
	}
	if !strings.Contains(response.Command, "ZENO_CONTROLLER_URL='https://zeno.example.com'") || !strings.Contains(response.Command, "zeno.siaoz.si/agent/install.sh") {
		t.Fatalf("install command should use configured agent controller URL and proxied installer: %s", response.Command)
	}
	if !strings.Contains(response.Commands["windows"], "$env:ZENO_CONTROLLER_URL='https://zeno.example.com'") {
		t.Fatalf("windows install command should use configured agent controller URL: %s", response.Commands["windows"])
	}
	if strings.Contains(response.Command, "admin.localhost") {
		t.Fatalf("install command should not fall back to request host when configured URL exists: %s", response.Command)
	}
}
func TestAdminNodeInstallCommandFallsBackToDirectIPAddressAndPort(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "old-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/example-node-a/install-command", nil)
	request.Host = "203.0.113.10:18980"
	request.Header.Set("X-Forwarded-Proto", "http")
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminPasswordHash: testAdminPasswordHash("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "ZENO_CONTROLLER_URL='http://203.0.113.10:18980'") {
		t.Fatalf("install command should use the direct IP and port: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "ZENO_ALLOW_INSECURE_HTTP") {
		t.Fatalf("remote HTTP install command must carry an explicit plaintext transport opt-in: %s", recorder.Body.String())
	}
}
func TestAdminNodeInstallCommandUsesAuthenticatedBrowserOriginWhenSettingIsEmpty(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "old-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/example-node-a/install-command", bytes.NewBufferString(`{"controller_url":"https://zeno.example.com"}`))
	request.Host = "attacker.example"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminPasswordHash: testAdminPasswordHash("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "ZENO_CONTROLLER_URL='https://zeno.example.com'") {
		t.Fatalf("install command should use the authenticated browser origin: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "attacker.example") {
		t.Fatalf("install command trusted the request Host instead of the explicit origin: %s", recorder.Body.String())
	}
}
func TestAdminNodeInstallCommandRequiresAdminTokenAndKnownNode(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminPasswordHash: testAdminPasswordHash("admin-pass")})

	cases := []struct {
		name       string
		nodeID     string
		adminToken string
		wantStatus int
	}{
		{name: "missing admin token", nodeID: "example-node-a", wantStatus: http.StatusUnauthorized},
		{name: "unknown node", nodeID: "missing", adminToken: "admin-pass", wantStatus: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/"+tc.nodeID+"/install-command", nil)
			if tc.adminToken != "" {
				request.Header.Set("X-Admin-Token", tc.adminToken)
			}
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
		})
	}
}
