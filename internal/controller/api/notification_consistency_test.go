package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A channel whose stored credential cannot be decrypted fails its own
// deliveries (retried, never logged in clear) without blocking a healthy
// channel.
func TestReconcileDamagedCredentialFailsOnlyItsChannelWithoutLeaking(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	hs := newReconcileHarness(t, shanghaiTime(2026, 10, 6, 9, 0, 0))
	enabled := true
	if _, err := hs.store.CreateAdminNotificationChannel(hs.ctx, AdminNotificationChannelCreateRequest{
		ID: "broken", Name: "Broken", Destination: "1000", Credential: "broken-bot-secret-value", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	hs.addNode("n1", "Node One", "")
	hs.round()
	corrupted := notificationCredentialCiphertextPrefix + base64.RawURLEncoding.EncodeToString([]byte("damaged-ciphertext-payload"))
	if _, err := hs.store.db.ExecContext(hs.ctx, `UPDATE notification_channels SET credential = ? WHERE id = 'broken'`, corrupted); err != nil {
		t.Fatalf("corrupt credential: %v", err)
	}
	hs.setLiveness("n1", "offline", hs.now().Unix())
	hs.after(2 * time.Second)
	forms := hs.tg.ackedForms()
	if len(forms) != 1 || !strings.Contains(forms[0], "chat_id=7579942307") {
		t.Fatalf("acked forms = %q, want only the healthy channel", forms)
	}
	var broken notificationStateRow
	tx, err := hs.store.db.BeginTx(hs.ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	states, err := notificationStateRows(hs.ctx, tx)
	_ = tx.Rollback()
	if err != nil {
		t.Fatalf("states: %v", err)
	}
	broken = states[notificationStateKey{ChannelID: "broken", NodeID: "n1", Kind: notificationKindLiveness}]
	if broken.Notified != livenessStateOnline || broken.PendingTarget != livenessStateOffline || broken.PendingAttempts != 1 || broken.LastError != "notification credential unavailable" {
		t.Fatalf("broken channel row = %+v, want a retryable credential failure", broken)
	}
	for _, secret := range []string{"telegram-bot-secret-value", "broken-bot-secret-value", corrupted} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("log leaked credential material %q:\n%s", secret, logs.String())
		}
	}
	if !strings.Contains(logs.String(), `notification failed channel_id=broken node_id=n1 kind=node_offline from=online to=offline attempt=1 error="notification credential unavailable"`) {
		t.Fatalf("log missing the credential failure line:\n%s", logs.String())
	}
}

func TestNotificationCredentialAndAuthorityKeyringsRotateWithoutEcho(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	oldKey := []byte("0123456789abcdef0123456789abcdef")
	newKey := []byte("abcdef0123456789abcdef0123456789")
	if err := store.ConfigureNotificationCredentialKeyring(ctx, "old", map[string][]byte{"old": oldKey}); err != nil {
		t.Fatalf("configure old key: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops", Name: "Ops", Destination: "chat", Credential: "old-secret", Enabled: &enabled}); err != nil {
		t.Fatalf("create old ciphertext: %v", err)
	}
	var oldCiphertext string
	if err := store.db.QueryRowContext(ctx, `SELECT credential FROM notification_channels WHERE id = 'ops'`).Scan(&oldCiphertext); err != nil {
		t.Fatalf("read old ciphertext: %v", err)
	}
	if !strings.HasPrefix(oldCiphertext, notificationCredentialCiphertextPrefix+"old:") {
		t.Fatalf("old ciphertext key id missing: %q", oldCiphertext)
	}
	if err := store.ConfigureNotificationCredentialKeyring(ctx, "new", map[string][]byte{"old": oldKey, "new": newKey}); err != nil {
		t.Fatalf("install rolling keyring: %v", err)
	}
	channel, err := store.AdminNotificationDispatchChannel(ctx, "ops")
	if err != nil || channel.Credential != "old-secret" {
		t.Fatalf("read old ciphertext with ring: channel=%+v err=%v", channel, err)
	}
	var newCiphertext string
	if err := store.db.QueryRowContext(ctx, `SELECT credential FROM notification_channels WHERE id = 'ops'`).Scan(&newCiphertext); err != nil {
		t.Fatalf("read new ciphertext: %v", err)
	}
	if !strings.HasPrefix(newCiphertext, notificationCredentialCiphertextPrefix+"new:") || strings.Contains(newCiphertext, "old-secret") {
		t.Fatalf("new ciphertext=%q", newCiphertext)
	}
	if err := store.ConfigureNotificationCredentialKeyring(ctx, "new", map[string][]byte{"new": newKey}); err != nil {
		t.Fatalf("drop old credential key after automatic rewrite: %v", err)
	}
	channel, err = store.AdminNotificationDispatchChannel(ctx, "ops")
	if err != nil || channel.Credential != "old-secret" {
		t.Fatalf("read automatically re-encrypted credential: channel=%+v err=%v", channel, err)
	}

	if authorized, err := store.AuthorizeNotificationAuthorityKeyring(ctx, "old", map[string]string{"old": "authority-old"}); err != nil || !authorized {
		t.Fatalf("bind old authority: authorized=%v err=%v", authorized, err)
	}
	if authorized, err := store.AuthorizeNotificationAuthorityKeyring(ctx, "new", map[string]string{"old": "authority-old", "new": "authority-new"}); err != nil || !authorized {
		t.Fatalf("rotate authority: authorized=%v err=%v", authorized, err)
	}
	if authorized, err := store.AuthorizeNotificationAuthorityKeyring(ctx, "new", map[string]string{"new": "authority-new"}); err != nil || !authorized {
		t.Fatalf("authorize with new-only ring: authorized=%v err=%v", authorized, err)
	}
	if authorized, err := store.AuthorizeNotificationAuthorityKeyring(ctx, "old", map[string]string{"old": "authority-old"}); err != nil || authorized {
		t.Fatalf("old authority remained valid: authorized=%v err=%v", authorized, err)
	}
}

func TestNotificationTypeCompatibilityWriteRollsBackBothTables(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO notification_types (event_type, enabled, updated_at)
		VALUES ('node_offline', 1, ?)
	`, time.Now().UTC().Unix()); err != nil {
		t.Fatalf("seed legacy type: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		CREATE TRIGGER reject_alert_rule_update
		BEFORE UPDATE ON alert_rules WHEN OLD.id = 'node_offline'
		BEGIN SELECT RAISE(ABORT, 'reject'); END
	`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	disabled := false
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &disabled}); err == nil {
		t.Fatal("compatibility write unexpectedly succeeded")
	}
	var legacyEnabled, ruleEnabled int
	if err := store.db.QueryRowContext(ctx, `SELECT enabled FROM notification_types WHERE event_type = 'node_offline'`).Scan(&legacyEnabled); err != nil {
		t.Fatalf("read legacy type: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT enabled FROM alert_rules WHERE id = 'node_offline'`).Scan(&ruleEnabled); err != nil {
		t.Fatalf("read rule: %v", err)
	}
	if legacyEnabled != 1 || ruleEnabled != 1 {
		t.Fatalf("partial write legacy=%d rule=%d", legacyEnabled, ruleEnabled)
	}
}

func TestConcurrentSparseSettingsPatchesDoNotLoseDisjointFields(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	for iteration := 0; iteration < 20; iteration++ {
		title := fmt.Sprintf("title-%d", iteration)
		logoURL := fmt.Sprintf("/assets/logo/custom-%d.png", iteration)
		start := make(chan struct{})
		errorsByWorker := make(chan error, 2)
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			_, err := store.UpdateAdminSettings(ctx, AdminSettingsUpdateRequest{SiteTitle: &title})
			errorsByWorker <- err
		}()
		go func() {
			defer workers.Done()
			<-start
			_, err := store.UpdateAdminSettings(ctx, AdminSettingsUpdateRequest{LogoURL: &logoURL})
			errorsByWorker <- err
		}()
		close(start)
		workers.Wait()
		close(errorsByWorker)
		for err := range errorsByWorker {
			if err != nil {
				t.Fatalf("concurrent patch: %v", err)
			}
		}
		settings, err := store.AdminSettings(ctx)
		if err != nil {
			t.Fatalf("read settings: %v", err)
		}
		if settings.SiteTitle != title || settings.LogoURL != logoURL {
			t.Fatalf("iteration %d lost update: title=%q logo=%q", iteration, settings.SiteTitle, settings.LogoURL)
		}
	}
}
