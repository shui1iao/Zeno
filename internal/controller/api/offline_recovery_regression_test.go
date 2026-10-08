package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/shui1iao/zeno/internal/shared/probe"
)

func openOfflineRecoveryTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	enableTestNotificationCredentialEncryption(t, store)
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	return store
}

func persistActiveOfflineIncident(t *testing.T, store *SQLiteStore, storedStatus string) {
	t.Helper()
	now := time.Now().UTC().Unix()
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `
		UPDATE nodes SET status = ?, last_seen_at = ?, updated_at = ? WHERE id = 'example-node-a'
	`, storedStatus, now, now); err != nil {
		t.Fatalf("set stored node status: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO alert_rule_states (node_id, rule_id, active, first_seen_at, last_seen_at, updated_at)
		VALUES ('example-node-a', 'node_offline', 1, ?, ?, ?)
		ON CONFLICT(node_id, rule_id) DO UPDATE SET active = 1, first_seen_at = excluded.first_seen_at,
			last_seen_at = excluded.last_seen_at, updated_at = excluded.updated_at
	`, now, now, now); err != nil {
		t.Fatalf("persist offline alert state: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO notification_event_marks (event_type, node_id, mark, created_at)
		VALUES ('node_offline', 'example-node-a', 'status-active:offline', ?)
	`, now); err != nil {
		t.Fatalf("persist offline incident mark: %v", err)
	}
}

func assertOfflineIncidentRecovered(t *testing.T, store *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	var active int
	if err := store.db.QueryRowContext(ctx, `
		SELECT active FROM alert_rule_states WHERE node_id = 'example-node-a' AND rule_id = 'node_offline'
	`).Scan(&active); err != nil {
		t.Fatalf("query offline alert state: %v", err)
	}
	if active != 0 {
		t.Fatalf("offline alert state active = %d, want 0", active)
	}
}

func TestAgentStateReconcilesOfflineIncidentWithoutStandaloneRecoveryWhenAlertWasNotDelivered(t *testing.T) {
	store := openOfflineRecoveryTestStore(t)
	ctx := context.Background()
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable offline notification type: %v", err)
	}
	persistActiveOfflineIncident(t, store, "online")

	telegram := newTelegramTestCapture(t)
	h := &handler{store: store, notificationSender: newHTTPNotificationSender(telegram.server.Client(), telegram.server.URL), liveHub: newLiveUpdateHub(), presence: newAgentPresenceManager()}
	// The user was never told about this offline incident (the stored status
	// is online), so reconciling after the state report sends nothing.
	h.reconcileNotifications(ctx)
	postAgentState(t, h.handleAgentState, time.Now().UTC().Unix(), 22.5)
	h.reconcileNotifications(ctx)
	_, forms, errors := telegram.waitForCalls(t, 0)
	if len(errors) != 0 || len(forms) != 0 {
		t.Fatalf("recovery was not held for stability: forms=%+v errors=%+v", forms, errors)
	}
	if len(errors) != 0 || len(forms) != 0 {
		t.Fatalf("standalone recovery calls=%d forms=%+v errors=%+v, want none", len(forms), forms, errors)
	}
	assertOfflineIncidentRecovered(t, store)
}

func TestAgentHeartbeatReconcilesOfflineIncidentAfterStoredStatusWasSilentlyOnline(t *testing.T) {
	store := openOfflineRecoveryTestStore(t)
	persistActiveOfflineIncident(t, store, "online")

	transition, err := store.RecordAgentHeartbeatTransition(context.Background(), "example-node-a", time.Now().UTC(), "online", "agent-test")
	if err != nil {
		t.Fatalf("record heartbeat transition: %v", err)
	}
	if transition.Previous.Status != "offline" || transition.Current.Status != "online" {
		t.Fatalf("heartbeat transition = %+v, want offline -> online", transition)
	}
	var active int
	if err := store.db.QueryRow(`SELECT active FROM alert_rule_states WHERE node_id = 'example-node-a' AND rule_id = 'node_offline'`).Scan(&active); err != nil {
		t.Fatalf("query offline alert state: %v", err)
	}
	if active != 0 {
		t.Fatalf("offline alert state active = %d, want 0", active)
	}
}

func TestHostAndProbeReportsDoNotSilentlyRecoverPersistedOfflineNode(t *testing.T) {
	store := openOfflineRecoveryTestStore(t)
	ctx := context.Background()
	staleSeen := time.Now().UTC().Add(-time.Minute).Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'offline', last_seen_at = ? WHERE id = 'example-node-a'`, staleSeen); err != nil {
		t.Fatalf("set offline node: %v", err)
	}
	if err := store.UpsertAgentHost(ctx, "example-node-a", AgentHostRequest{OSName: "Linux", Arch: "amd64", AgentVersion: "agent-test"}); err != nil {
		t.Fatalf("upsert host: %v", err)
	}
	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'example-node-a'`).Scan(&status); err != nil {
		t.Fatalf("query status after host: %v", err)
	}
	if status != "offline" {
		t.Fatalf("status after host = %q, want offline until a transition-aware liveness report", status)
	}
	var hostLastSeen int64
	if err := store.db.QueryRowContext(ctx, `SELECT last_seen_at FROM nodes WHERE id = 'example-node-a'`).Scan(&hostLastSeen); err != nil {
		t.Fatalf("query last seen after host: %v", err)
	}

	targets, err := store.EnabledProbeTargets(ctx, "example-node-a")
	if err != nil || len(targets) == 0 {
		t.Fatalf("enabled probe targets = %d, err=%v", len(targets), err)
	}
	if err := store.InsertProbeRound(ctx, "example-node-a", targets[0], time.Now().UTC(), []probe.Sample{{Seq: 1, Success: true, LatencyMS: floatValuePtr(12)}}); err != nil {
		t.Fatalf("insert probe round: %v", err)
	}
	var lastSeen int64
	if err := store.db.QueryRowContext(ctx, `SELECT status, last_seen_at FROM nodes WHERE id = 'example-node-a'`).Scan(&status, &lastSeen); err != nil {
		t.Fatalf("query node after probe: %v", err)
	}
	if status != "offline" {
		t.Fatalf("status after probe = %q, want offline", status)
	}
	if lastSeen != hostLastSeen {
		t.Fatalf("last_seen_at after probe = %d, want host liveness timestamp %d", lastSeen, hostLastSeen)
	}
}

func TestStaleOfflineScannerWaitsForStartupGrace(t *testing.T) {
	store := openOfflineRecoveryTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	staleSeen := time.Now().UTC().Add(-time.Minute).Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'example-node-a'`, staleSeen); err != nil {
		t.Fatalf("set stale node: %v", err)
	}
	h := &handler{store: store, liveHub: newLiveUpdateHub(), presence: newAgentPresenceManager(), notificationSender: &countingNotificationSender{}}
	go h.runStaleAgentOfflineScannerWithGrace(ctx, 5*time.Millisecond, 80*time.Millisecond)

	time.Sleep(25 * time.Millisecond)
	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'example-node-a'`).Scan(&status); err != nil {
		t.Fatalf("query status during grace: %v", err)
	}
	if status != "online" {
		t.Fatalf("status during startup grace = %q, want online", status)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'example-node-a'`).Scan(&status); err != nil {
			t.Fatalf("query status after grace: %v", err)
		}
		if status == "offline" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("status after startup grace = %q, want offline", status)
}
