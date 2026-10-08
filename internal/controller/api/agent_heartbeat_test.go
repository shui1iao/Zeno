package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAgentHeartbeatUpdatesNodeStatusAndLastSeen(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	before := time.Now().UTC().Unix()
	ts := time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Second).Unix()
	payload := []byte(`{"ts":` + strconv.FormatInt(ts, 10) + `,"status":"online","agent_version":"agent-test"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/heartbeat", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}
	var status, agentVersion string
	var lastSeen int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT n.status, n.last_seen_at, h.agent_version
		FROM nodes n LEFT JOIN host_info h ON h.node_id = n.id
		WHERE n.id = 'example-node-a'
	`).Scan(&status, &lastSeen, &agentVersion); err != nil {
		t.Fatalf("query heartbeat state: %v", err)
	}
	after := time.Now().UTC().Unix()
	if status != "online" || lastSeen < before || lastSeen > after || agentVersion != "agent-test" {
		t.Fatalf("heartbeat persisted status=%q last_seen=%d agent_version=%q, want online/received-at %d..%d/agent-test", status, lastSeen, agentVersion, before, after)
	}
}
func TestAgentHeartbeatRejectsTimestampSkewWithoutPoisoningLastSeen(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	baseline := time.Now().UTC().Add(-time.Minute).Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET last_seen_at = ? WHERE id = 'example-node-a'`, baseline); err != nil {
		t.Fatalf("set baseline last_seen_at: %v", err)
	}
	var original int64
	if err := store.db.QueryRowContext(ctx, `SELECT last_seen_at FROM nodes WHERE id = 'example-node-a'`).Scan(&original); err != nil {
		t.Fatalf("query original last_seen_at: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store})
	for name, ts := range map[string]int64{
		"future": time.Now().UTC().Add(maxAgentTimestampFutureSkew + time.Minute).Unix(),
		"past":   time.Now().UTC().Add(-maxAgentTimestampPastSkew - time.Minute).Unix(),
	} {
		t.Run(name, func(t *testing.T) {
			payload := []byte(`{"ts":` + strconv.FormatInt(ts, 10) + `,"status":"online"}`)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/heartbeat", bytes.NewReader(payload))
			request.Header.Set("X-Node-ID", "example-node-a")
			request.Header.Set("Authorization", "Bearer test-agent-token")
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			var got int64
			if err := store.db.QueryRowContext(ctx, `SELECT last_seen_at FROM nodes WHERE id = 'example-node-a'`).Scan(&got); err != nil {
				t.Fatalf("query last_seen_at: %v", err)
			}
			if got != original {
				t.Fatalf("last_seen_at changed from %d to %d after rejected heartbeat", original, got)
			}
		})
	}
}
func TestAgentHeartbeatOfflineStatusIsFreshLivenessNotOfflineTransition(t *testing.T) {
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
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "ops-telegram",
		Name:        "Ops Telegram",
		Destination: "7579942307",
		Credential:  "telegram-bot-credential-value",
		Enabled:     &enabled,
	}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	handler := NewHandler(telegram.handlerOptions(store))
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'example-node-a'`, now.Unix()); err != nil {
		t.Fatalf("set fresh heartbeat: %v", err)
	}
	postAgentHeartbeat(t, handler, now.Unix(), "offline")

	paths, forms, errors := telegram.waitForCalls(t, 0)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors = %+v", errors)
	}
	if len(paths) != 0 || len(forms) != 0 {
		t.Fatalf("telegram calls paths=%+v forms=%+v, want no offline notification from a received heartbeat", paths, forms)
	}
	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'example-node-a'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "online" {
		t.Fatalf("status after offline heartbeat = %q, want online liveness", status)
	}
}
func TestAgentHeartbeatOfflineStatusDoesNotDrainTelegramChannel(t *testing.T) {
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

	var captureMu sync.Mutex
	var telegramPaths []string
	var telegramForms []string
	var telegramErrors []error
	telegramAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			captureMu.Lock()
			telegramErrors = append(telegramErrors, err)
			captureMu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		captureMu.Lock()
		telegramPaths = append(telegramPaths, r.URL.Path)
		telegramForms = append(telegramForms, r.Form.Encode())
		captureMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	defer telegramAPI.Close()

	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "home-telegram",
		Name:        "Home Telegram",
		Destination: "7579942307",
		Credential:  "telegram-bot-credential-value",
		Enabled:     &enabled,
	}); err != nil {
		t.Fatalf("create telegram channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store, TelegramAPIBaseURL: telegramAPI.URL})
	defer cleanupTestHandler(t, handler)
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'example-node-a'`, now.Unix()); err != nil {
		t.Fatalf("set fresh heartbeat: %v", err)
	}
	postAgentHeartbeat(t, handler, now.Unix(), "offline")

	waitUntil(t, time.Second, func() bool {
		captureMu.Lock()
		defer captureMu.Unlock()
		return len(telegramPaths)+len(telegramErrors) == 0
	})
	captureMu.Lock()
	capturedPaths := append([]string(nil), telegramPaths...)
	capturedForms := append([]string(nil), telegramForms...)
	capturedErrors := append([]error(nil), telegramErrors...)
	captureMu.Unlock()
	if len(capturedErrors) != 0 {
		t.Fatalf("telegram handler errors = %+v", capturedErrors)
	}
	if len(capturedPaths) != 0 || len(capturedForms) != 0 {
		t.Fatalf("telegram calls paths=%+v forms=%+v, want none", capturedPaths, capturedForms)
	}
}
func TestStaleOfflineNotificationDeliveryDoesNotBlockScanner(t *testing.T) {
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

	called := make(chan struct{}, 1)
	release := make(chan struct{})
	slowTelegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- struct{}{}
		<-release
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	defer slowTelegram.Close()
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "slow-telegram", Name: "Slow Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	staleSeen := time.Now().UTC().Add(-nodeHeartbeatOfflineAfter - time.Second)
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'example-node-a'`, staleSeen.Unix()); err != nil {
		t.Fatalf("set stale heartbeat: %v", err)
	}
	httpHandler := NewHandler(HandlerOptions{Store: store, NotificationClient: slowTelegram.Client(), TelegramAPIBaseURL: slowTelegram.URL, NotificationReconcileInterval: time.Hour})
	defer cleanupTestHandler(t, httpHandler)
	defer close(release)
	h := httpHandler.(*handler)
	// Record the baseline before the stale scan so the scan's wake produces
	// the offline alert on the reconcile goroutine.
	h.reconcileNotifications(ctx)
	started := time.Now()
	h.dispatchStaleAgentOfflineChecks(ctx)
	elapsed := time.Since(started)
	if elapsed > 150*time.Millisecond {
		t.Fatalf("stale scan took %s, want notification delivery to be non-blocking", elapsed)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatalf("slow telegram send was not attempted")
	}
}
func TestStaleAgentOfflineCheckDispatchesWhenPublicStatusExpires(t *testing.T) {
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
	staleSeen := time.Now().UTC().Add(-nodeHeartbeatOfflineAfter - time.Second).Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'example-node-a'`, staleSeen); err != nil {
		t.Fatalf("set stale heartbeat: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	telegram := newTelegramTestCapture(t)
	h := &handler{store: store, notificationSender: newHTTPNotificationSender(telegram.server.Client(), telegram.server.URL), liveHub: newLiveUpdateHub(), presence: newAgentPresenceManager()}
	h.reconcileNotifications(ctx)
	h.dispatchStaleAgentOfflineChecks(ctx)
	h.reconcileNotifications(ctx)

	paths, forms, errors := telegram.waitForCalls(t, 1)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors = %+v", errors)
	}
	if len(paths) != 1 || len(forms) != 1 || !strings.Contains(forms[0], "%E7%A6%BB%E7%BA%BF") {
		t.Fatalf("telegram calls paths=%+v forms=%+v, want stale offline notification", paths, forms)
	}
	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'example-node-a'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "offline" {
		t.Fatalf("stored status = %q, want stale check to persist offline", status)
	}
	var storedLastSeen int64
	if err := store.db.QueryRowContext(ctx, `SELECT last_seen_at FROM nodes WHERE id = 'example-node-a'`).Scan(&storedLastSeen); err != nil {
		t.Fatalf("query node last_seen_at: %v", err)
	}
	if storedLastSeen != staleSeen {
		t.Fatalf("last_seen_at = %d, want original agent heartbeat time %d", storedLastSeen, staleSeen)
	}
}
func TestStaleAgentOfflineCheckSkipsFreshHeartbeatRace(t *testing.T) {
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
	nodeIDs, err := store.StaleAgentOfflineNodeIDs(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("stale node ids: %v", err)
	}
	if len(nodeIDs) != 1 || nodeIDs[0] != "example-node-a" {
		t.Fatalf("stale node ids = %+v, want example-node-a", nodeIDs)
	}
	freshSeen := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'example-node-a'`, freshSeen); err != nil {
		t.Fatalf("set fresh heartbeat: %v", err)
	}
	transition, ok, err := store.RecordStaleAgentOfflineTransition(ctx, "example-node-a", time.Now().UTC())
	if err != nil {
		t.Fatalf("record stale offline transition: %v", err)
	}
	if ok || transition.Current.Status != "" {
		t.Fatalf("transition = %+v ok=%v, want skipped stale update after fresh heartbeat", transition, ok)
	}
	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'example-node-a'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "online" {
		t.Fatalf("stored status = %q, want online after fresh heartbeat wins race", status)
	}
}
func TestAgentHeartbeatDoesNotDispatchRecoveryAfterStaleHeartbeatOffline(t *testing.T) {
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
	staleSeen := time.Now().UTC().Add(-nodeHeartbeatOfflineAfter - time.Minute).Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'example-node-a'`, staleSeen); err != nil {
		t.Fatalf("set stale heartbeat: %v", err)
	}

	telegram := newTelegramTestCapture(t)
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	httpHandler := NewHandler(telegram.handlerOptions(store))
	h := httpHandler.(*handler)
	h.reconcileNotifications(ctx)
	postAgentHeartbeat(t, httpHandler, time.Now().UTC().Unix(), "online")
	h.reconcileNotifications(ctx)
	paths, forms, errors := telegram.waitForCalls(t, 0)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors = %+v", errors)
	}
	if len(paths) != 0 || len(forms) != 0 {
		t.Fatalf("telegram calls paths=%+v forms=%+v, want no recovery notification", paths, forms)
	}
}
func TestAgentStateDispatchesRecoveryAfterPersistedOffline(t *testing.T) {
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
	staleSeen := time.Now().UTC().Add(-nodeHeartbeatOfflineAfter - time.Second).Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'example-node-a'`, staleSeen); err != nil {
		t.Fatalf("set stale heartbeat: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegr…alue", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	telegram := newTelegramTestCapture(t)
	clock := newFakeNotificationClock(time.Now().UTC())
	h := &handler{store: store, notificationSender: newHTTPNotificationSender(telegram.server.Client(), telegram.server.URL), liveHub: newLiveUpdateHub(), presence: newAgentPresenceManager(), notificationClock: clock.Now}
	h.reconcileNotifications(ctx)
	if _, ok, err := store.RecordStaleAgentOfflineTransition(ctx, "example-node-a", time.Now().UTC()); err != nil {
		t.Fatalf("record stale offline transition: %v", err)
	} else if !ok {
		t.Fatalf("stale offline transition skipped, want persisted offline")
	}
	h.reconcileNotifications(ctx)
	paths, forms, errors := telegram.waitForCalls(t, 1)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors after offline = %+v", errors)
	}
	if len(paths) != 1 || len(forms) != 1 || !strings.Contains(forms[0], "%E7%A6%BB%E7%BA%BF") {
		t.Fatalf("telegram calls paths=%+v forms=%+v, want offline notification", paths, forms)
	}

	postAgentState(t, h.handleAgentState, time.Now().UTC().Unix(), 22.5)
	clock.Advance(2 * time.Second)
	h.reconcileNotifications(ctx)
	paths, forms, errors = telegram.waitForCalls(t, 1)
	if len(errors) != 0 || len(paths) != 1 || len(forms) != 1 {
		t.Fatalf("recovery was not held for stability: paths=%+v forms=%+v errors=%+v", paths, forms, errors)
	}
	clock.Advance(nodeHeartbeatOfflineAfter)
	h.reconcileNotifications(ctx)
	paths, forms, errors = telegram.waitForCalls(t, 2)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors after recovery = %+v", errors)
	}
	if len(paths) != 2 || len(forms) != 2 || !strings.Contains(forms[1], "%E6%81%A2%E5%A4%8D") {
		t.Fatalf("telegram calls paths=%+v forms=%+v, want state-triggered recovery notification", paths, forms)
	}
}
func TestAgentHeartbeatTransitionTreatsReceivedHeartbeatAsFreshLiveness(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	staleSeen := time.Now().UTC().Add(-nodeHeartbeatOfflineAfter - time.Minute).Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'example-node-a'`, staleSeen); err != nil {
		t.Fatalf("set stale heartbeat: %v", err)
	}

	transition, err := store.RecordAgentHeartbeatTransition(ctx, "example-node-a", time.Unix(staleSeen+1, 0).UTC(), "online", "agent-test")
	if err != nil {
		t.Fatalf("record heartbeat transition: %v", err)
	}
	if transition.Previous.Status != "online" || transition.Current.Status != "online" {
		t.Fatalf("transition = %+v, want stored online -> online so stale public state does not send recovery-only notifications", transition)
	}
}
func TestAgentHeartbeatTransitionTreatsExplicitOfflineAsOnlineLiveness(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'example-node-a'`, now.Unix()); err != nil {
		t.Fatalf("set fresh heartbeat: %v", err)
	}

	transition, err := store.RecordAgentHeartbeatTransition(ctx, "example-node-a", now, "offline", "agent-test")
	if err != nil {
		t.Fatalf("record heartbeat transition: %v", err)
	}
	if transition.Previous.Status != "online" || transition.Current.Status != "online" {
		t.Fatalf("transition = %+v, want online -> online liveness", transition)
	}
}
func TestAgentHeartbeatOfflineCompatibilityDoesNotRejectHeartbeat(t *testing.T) {
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

	closedTelegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closedTelegram.URL
	closedTelegram.Close()
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "broken-telegram",
		Name:        "Broken Telegram",
		Destination: "7579942307",
		Credential:  "telegram-bot-credential-value",
		Enabled:     &enabled,
	}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'example-node-a'`, now.Unix()); err != nil {
		t.Fatalf("set fresh heartbeat: %v", err)
	}
	httpHandler := NewHandler(HandlerOptions{Store: store, TelegramAPIBaseURL: closedURL})
	defer cleanupTestHandler(t, httpHandler)
	recorder := postAgentHeartbeat(t, httpHandler, now.Unix(), "offline")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("heartbeat status = %d, want 202 for legacy offline status; body=%s", recorder.Code, recorder.Body.String())
	}
	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'example-node-a'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "online" {
		t.Fatalf("stored node status = %q, want legacy offline heartbeat normalized to online", status)
	}
}
