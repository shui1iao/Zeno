package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shui1iao/zeno/internal/controller/notifycrypto"
)

func TestNotificationCredentialEncryptionRoundTripRandomNonceAndNoEcho(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	enabled := true
	first, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-a", Name: "Ops A", Destination: "7579942307", Credential: "telegram-bot-secret-value", Enabled: &enabled})
	if err != nil {
		t.Fatalf("create first channel: %v", err)
	}
	second, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-b", Name: "Ops B", Destination: "7579942307", Credential: "telegram-bot-secret-value", Enabled: &enabled})
	if err != nil {
		t.Fatalf("create second channel: %v", err)
	}

	firstStored := storedNotificationCredential(t, store, first.ID)
	secondStored := storedNotificationCredential(t, store, second.ID)
	for _, stored := range []string{firstStored, secondStored} {
		if !strings.HasPrefix(stored, notificationCredentialCiphertextPrefix) {
			t.Fatalf("stored credential %q missing encrypted version prefix", stored)
		}
		if strings.Contains(stored, "telegram-bot-secret-value") {
			t.Fatalf("stored credential contains plaintext secret: %s", stored)
		}
	}
	if firstStored == secondStored {
		t.Fatalf("same plaintext encrypted to identical ciphertext; nonce was not random")
	}

	dispatchChannel, err := store.AdminNotificationDispatchChannel(ctx, first.ID)
	if err != nil {
		t.Fatalf("dispatch channel: %v", err)
	}
	if dispatchChannel.Credential != "telegram-bot-secret-value" {
		t.Fatalf("dispatch credential = %q, want plaintext only at dispatch", dispatchChannel.Credential)
	}
	channels, err := store.AdminNotificationChannels(ctx)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(channels) != 2 || !channels[0].CredentialSet || channels[0].Credential != "" {
		t.Fatalf("listed channels = %+v, want credential_set without credential", channels)
	}

	handler := NewHandler(HandlerOptions{Store: store, AdminPasswordHash: testAdminPasswordHash("admin-pass")})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/notification-channels", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response AdminNotificationChannelsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if strings.Contains(recorder.Body.String(), "telegram-bot-secret-value") || strings.Contains(recorder.Body.String(), firstStored) || strings.Contains(recorder.Body.String(), `"credential":`) {
		t.Fatalf("notification API leaked credential material: %s", recorder.Body.String())
	}
}

func TestNotificationCredentialAADBindsChannelIDAndType(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	enabled := true
	first, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-a", Name: "Ops A", Destination: "7579942307", Credential: "credential-a", Enabled: &enabled})
	if err != nil {
		t.Fatalf("create first channel: %v", err)
	}
	second, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-b", Name: "Ops B", Destination: "7579942307", Credential: "credential-b", Enabled: &enabled})
	if err != nil {
		t.Fatalf("create second channel: %v", err)
	}
	firstStored := storedNotificationCredential(t, store, first.ID)
	if _, err := store.db.ExecContext(ctx, `UPDATE notification_channels SET credential = ? WHERE id = ?`, firstStored, second.ID); err != nil {
		t.Fatalf("copy ciphertext across channels: %v", err)
	}
	if _, err := store.AdminNotificationDispatchChannel(ctx, second.ID); !errors.Is(err, errNotificationCredentialCiphertextInvalid) {
		t.Fatalf("dispatch with copied ciphertext error = %v, want invalid ciphertext", err)
	}

	key := testNotificationCredentialKey()
	cipher, err := notifycrypto.NewCipherWithID(notifycrypto.DerivedKeyID(key), key)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	stored, err := cipher.Encrypt("ops-a", "telegram", "type-bound-secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := cipher.Decrypt("ops-a", "email", stored); !errors.Is(err, errNotificationCredentialCiphertextInvalid) {
		t.Fatalf("decrypt with different type error = %v, want invalid ciphertext", err)
	}
}

func TestNotificationCredentialLegacyPlaintextMigrationIsTransactionalAndIdempotent(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	insertLegacyNotificationCredential(t, store, "legacy", "Legacy", "legacy-telegram-token")

	if err := store.ConfigureNotificationCredentialEncryption(ctx, testNotificationCredentialKey()); err != nil {
		t.Fatalf("migrate legacy credential: %v", err)
	}
	migrated := storedNotificationCredential(t, store, "legacy")
	if !strings.HasPrefix(migrated, notificationCredentialCiphertextPrefix) || strings.Contains(migrated, "legacy-telegram-token") {
		t.Fatalf("legacy credential was not encrypted safely: %s", migrated)
	}
	dispatchChannel, err := store.AdminNotificationDispatchChannel(ctx, "legacy")
	if err != nil {
		t.Fatalf("dispatch migrated channel: %v", err)
	}
	if dispatchChannel.Credential != "legacy-telegram-token" {
		t.Fatalf("migrated credential = %q, want legacy token", dispatchChannel.Credential)
	}
	if err := store.ConfigureNotificationCredentialEncryption(ctx, testNotificationCredentialKey()); err != nil {
		t.Fatalf("second migration pass: %v", err)
	}
	if again := storedNotificationCredential(t, store, "legacy"); again != migrated {
		t.Fatalf("second migration rewrote ciphertext: before=%q after=%q", migrated, again)
	}

	failingStore, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open failing sqlite store: %v", err)
	}
	defer failingStore.Close()
	insertLegacyNotificationCredential(t, failingStore, "plain", "Plain", "plain-token")
	insertLegacyNotificationCredential(t, failingStore, "broken", "Broken", notificationCredentialCiphertextPrefix+"not-valid-base64")
	if err := failingStore.ConfigureNotificationCredentialEncryption(ctx, testNotificationCredentialKey()); !errors.Is(err, errNotificationCredentialCiphertextInvalid) {
		t.Fatalf("migration with damaged ciphertext error = %v, want invalid ciphertext", err)
	}
	if plain := storedNotificationCredential(t, failingStore, "plain"); plain != "plain-token" {
		t.Fatalf("failed migration was not transactional; plaintext row became %q", plain)
	}
}

func TestNotificationCredentialMissingAndWrongKeyFailClosed(t *testing.T) {
	emptyStore, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open empty store: %v", err)
	}
	if err := emptyStore.RequireNotificationCredentialKeyForExistingCredentials(context.Background()); err != nil {
		t.Fatalf("empty install without notification credentials should not require key: %v", err)
	}
	_ = emptyStore.Close()

	dbPath := filepath.Join(t.TempDir(), "zeno.db")
	store, err := OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	enableTestNotificationCredentialEncryption(t, store)
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(context.Background(), AdminNotificationChannelCreateRequest{ID: "ops", Name: "Ops", Destination: "7579942307", Credential: "telegram-bot-secret-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	_ = store.Close()

	missingKeyStore, err := OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen missing-key store: %v", err)
	}
	if err := missingKeyStore.RequireNotificationCredentialKeyForExistingCredentials(context.Background()); !errors.Is(err, errNotificationCredentialKeyRequired) {
		t.Fatalf("missing key check error = %v, want key required", err)
	}
	if _, err := missingKeyStore.AdminNotificationDispatchChannel(context.Background(), "ops"); !errors.Is(err, errNotificationCredentialKeyRequired) {
		t.Fatalf("dispatch without key error = %v, want key required", err)
	}
	_ = missingKeyStore.Close()

	wrongKeyStore, err := OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen wrong-key store: %v", err)
	}
	defer wrongKeyStore.Close()
	if err := wrongKeyStore.ConfigureNotificationCredentialEncryption(context.Background(), alternateTestNotificationCredentialKey()); !errors.Is(err, errNotificationCredentialCiphertextInvalid) {
		t.Fatalf("wrong key configure error = %v, want invalid ciphertext", err)
	}

	legacyStore, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open legacy store: %v", err)
	}
	defer legacyStore.Close()
	insertLegacyNotificationCredential(t, legacyStore, "legacy", "Legacy", "legacy-token")
	if err := legacyStore.RequireNotificationCredentialKeyForExistingCredentials(context.Background()); !errors.Is(err, errNotificationCredentialKeyRequired) {
		t.Fatalf("legacy plaintext without key error = %v, want key required", err)
	}
}

func TestNotificationCredentialCreateRequiresConfiguredKey(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(context.Background(), AdminNotificationChannelCreateRequest{ID: "ops", Name: "Ops", Destination: "7579942307", Credential: "telegram-bot-secret-value", Enabled: &enabled}); !errors.Is(err, errNotificationCredentialKeyRequired) {
		t.Fatalf("create without key error = %v, want key required", err)
	}
}

func storedNotificationCredential(t *testing.T, store *SQLiteStore, channelID string) string {
	t.Helper()
	var stored string
	if err := store.db.QueryRowContext(context.Background(), `SELECT credential FROM notification_channels WHERE id = ?`, channelID).Scan(&stored); err != nil {
		t.Fatalf("query stored notification credential for %s: %v", channelID, err)
	}
	return stored
}

func insertLegacyNotificationCredential(t *testing.T, store *SQLiteStore, channelID, name, credential string) {
	t.Helper()
	now := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(context.Background(), `
		INSERT INTO notification_channels (id, name, destination, credential, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?)
	`, channelID, name, "7579942307", credential, now, now); err != nil {
		t.Fatalf("insert legacy notification credential: %v", err)
	}
}
