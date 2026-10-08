package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// testShanghai is the production Controller time zone (TZ=Asia/Shanghai)
// without depending on the host time zone database.
var testShanghai = time.FixedZone("Asia/Shanghai", 8*3600)

func formatInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}

func decodedTelegramText(form string) string {
	values, err := url.ParseQuery(form)
	if err != nil {
		return form
	}
	return values.Get("text")
}

type countingNotificationSender struct {
	mu    sync.Mutex
	count int
}

func (sender *countingNotificationSender) Send(context.Context, notificationDispatchChannel, string) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.count++
	return nil
}

type fakeNotificationClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeNotificationClock(start time.Time) *fakeNotificationClock {
	return &fakeNotificationClock{now: start.UTC()}
}

func (clock *fakeNotificationClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fakeNotificationClock) Set(value time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = value.UTC()
}

func (clock *fakeNotificationClock) Advance(delta time.Duration) time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(delta)
	return clock.now
}

// fakeTelegram is a local stand-in for api.telegram.org with switchable
// behaviour: ok, fail (HTTP 502), hang (request fully received, response never
// sent), down (connection dropped before anything is received) and
// ratelimit (HTTP 429 with parameters.retry_after).
type fakeTelegram struct {
	server     *httptest.Server
	mu         sync.Mutex
	mode       string
	retryAfter int
	received   []string
	acked      []string
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	fake := &fakeTelegram{mode: "ok"}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

func (fake *fakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	fake.mu.Lock()
	mode := fake.mode
	retryAfter := fake.retryAfter
	fake.mu.Unlock()
	if mode == "down" {
		if hijacker, ok := w.(http.Hijacker); ok {
			if conn, _, err := hijacker.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	form := r.Form.Encode()
	fake.mu.Lock()
	fake.received = append(fake.received, form)
	fake.mu.Unlock()
	switch mode {
	case "fail":
		w.WriteHeader(http.StatusBadGateway)
	case "hang":
		<-r.Context().Done()
	case "ratelimit":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after ` + strconv.Itoa(retryAfter) + `","parameters":{"retry_after":` + strconv.Itoa(retryAfter) + `}}`))
	default:
		fake.mu.Lock()
		fake.acked = append(fake.acked, form)
		fake.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func (fake *fakeTelegram) setMode(mode string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.mode = mode
}

func (fake *fakeTelegram) setRateLimit(retryAfter int) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.mode = "ratelimit"
	fake.retryAfter = retryAfter
}

// receivedTexts are all messages that reached the server, including ones
// whose response was lost or negative.
func (fake *fakeTelegram) receivedTexts() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return formTexts(fake.received)
}

// ackedTexts are messages Telegram confirmed.
func (fake *fakeTelegram) ackedTexts() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return formTexts(fake.acked)
}

func (fake *fakeTelegram) ackedForms() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]string(nil), fake.acked...)
}

func formTexts(forms []string) []string {
	texts := make([]string, 0, len(forms))
	for _, form := range forms {
		texts = append(texts, decodedTelegramText(form))
	}
	return texts
}

// reconcileHarness drives the real reconcile round against a real SQLite
// store, a fake clock and a fake Telegram server. Rounds are invoked
// explicitly so every scenario is deterministic.
type reconcileHarness struct {
	t      *testing.T
	ctx    context.Context
	dbPath string
	store  *SQLiteStore
	clock  *fakeNotificationClock
	tg     *fakeTelegram
	h      *handler
}

const reconcileTestSendTimeout = 300 * time.Millisecond

func newReconcileHarness(t *testing.T, start time.Time) *reconcileHarness {
	t.Helper()
	harness := &reconcileHarness{
		t:      t,
		ctx:    context.Background(),
		dbPath: filepath.Join(t.TempDir(), "zeno.db"),
		clock:  newFakeNotificationClock(start),
		tg:     newFakeTelegram(t),
	}
	harness.openStore()
	enabled := true
	if _, err := harness.store.CreateAdminNotificationChannel(harness.ctx, AdminNotificationChannelCreateRequest{
		ID: "tg", Name: "Telegram", Destination: "7579942307", Credential: "telegram-bot-secret-value", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	harness.restart()
	return harness
}

func (harness *reconcileHarness) openStore() {
	harness.t.Helper()
	store, err := OpenSQLiteStore(harness.dbPath)
	if err != nil {
		harness.t.Fatalf("open sqlite store: %v", err)
	}
	harness.t.Cleanup(func() { _ = store.Close() })
	enableTestNotificationCredentialEncryption(harness.t, store)
	harness.store = store
}

// restart simulates a Controller restart: a fresh handler with empty
// in-memory state (backoff) over the same database.
func (harness *reconcileHarness) restart() {
	harness.h = harness.newHandler(harness.store)
}

func (harness *reconcileHarness) reopenStore() {
	harness.t.Helper()
	if err := harness.store.Close(); err != nil {
		harness.t.Fatalf("close store: %v", err)
	}
	harness.openStore()
	harness.restart()
}

func (harness *reconcileHarness) newHandler(store Store) *handler {
	return &handler{
		store:                   store,
		notificationSender:      newHTTPNotificationSender(harness.tg.server.Client(), harness.tg.server.URL),
		notificationClock:       harness.clock.Now,
		notificationLoc:         testShanghai,
		notificationSendTimeout: reconcileTestSendTimeout,
	}
}

func (harness *reconcileHarness) round() {
	harness.h.reconcileNotifications(harness.ctx)
}

func (harness *reconcileHarness) at(value time.Time) {
	harness.clock.Set(value)
	harness.round()
}

func (harness *reconcileHarness) after(delta time.Duration) {
	harness.clock.Advance(delta)
	harness.round()
}

func (harness *reconcileHarness) now() time.Time {
	return harness.clock.Now()
}

func (harness *reconcileHarness) addNode(id, name, ipv4 string) {
	harness.t.Helper()
	if _, err := harness.store.CreateAdminNode(harness.ctx, AdminNodeCreateRequest{ID: id, DisplayName: name, CountryCode: "SG", PublicIPv4: ipv4}); err != nil {
		harness.t.Fatalf("create node %s: %v", id, err)
	}
	harness.setLiveness(id, "online", harness.now().Unix())
}

// setLiveness writes the authoritative stored status the way the agent
// writers and the stale scanner leave it.
func (harness *reconcileHarness) setLiveness(nodeID, status string, lastSeen int64) {
	harness.t.Helper()
	if _, err := harness.store.db.ExecContext(harness.ctx, `UPDATE nodes SET status = ?, last_seen_at = ? WHERE id = ?`, status, lastSeen, nodeID); err != nil {
		harness.t.Fatalf("set %s liveness: %v", nodeID, err)
	}
}

func (harness *reconcileHarness) setResourceRule(nodeID, ruleID string, active bool) {
	harness.t.Helper()
	nowUnix := harness.now().Unix()
	if _, err := harness.store.db.ExecContext(harness.ctx, `
		INSERT INTO alert_rule_states (node_id, rule_id, active, first_seen_at, last_seen_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id, rule_id) DO UPDATE SET active = excluded.active, last_seen_at = excluded.last_seen_at, updated_at = excluded.updated_at
	`, nodeID, ruleID, sqliteBoolInt(active), nowUnix, nowUnix, nowUnix); err != nil {
		harness.t.Fatalf("set %s %s: %v", nodeID, ruleID, err)
	}
	status := "online"
	if active {
		status = "warning"
	}
	if _, err := harness.store.db.ExecContext(harness.ctx, `UPDATE nodes SET status = ? WHERE id = ? AND status <> 'offline'`, status, nodeID); err != nil {
		harness.t.Fatalf("set %s status: %v", nodeID, err)
	}
}

func (harness *reconcileHarness) state(nodeID, kind string) (notificationStateRow, bool) {
	harness.t.Helper()
	tx, err := harness.store.db.BeginTx(harness.ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		harness.t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	states, err := notificationStateRows(harness.ctx, tx)
	if err != nil {
		harness.t.Fatalf("read states: %v", err)
	}
	row, ok := states[notificationStateKey{ChannelID: "tg", NodeID: nodeID, Kind: kind}]
	return row, ok
}

func (harness *reconcileHarness) stateCount(where string, args ...any) int {
	harness.t.Helper()
	var count int
	if err := harness.store.db.QueryRowContext(harness.ctx, `SELECT COUNT(*) FROM notification_states WHERE `+where, args...).Scan(&count); err != nil {
		harness.t.Fatalf("count states: %v", err)
	}
	return count
}

func (harness *reconcileHarness) logEntries(outcome string) []notificationLogEntry {
	harness.t.Helper()
	rows, err := harness.store.db.QueryContext(harness.ctx, `
		SELECT ts, channel_id, node_id, node_name, kind, from_state, to_state, outcome, attempt, error, message
		FROM notification_log
		WHERE outcome = ? OR ? = ''
		ORDER BY id ASC
	`, outcome, outcome)
	if err != nil {
		harness.t.Fatalf("query log: %v", err)
	}
	defer rows.Close()
	entries := make([]notificationLogEntry, 0)
	for rows.Next() {
		var entry notificationLogEntry
		if err := rows.Scan(&entry.TS, &entry.ChannelID, &entry.NodeID, &entry.NodeName, &entry.Kind, &entry.From, &entry.To,
			&entry.Outcome, &entry.Attempt, &entry.Error, &entry.Message); err != nil {
			harness.t.Fatalf("scan log: %v", err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func (harness *reconcileHarness) expectAcked(want ...string) {
	harness.t.Helper()
	got := harness.tg.ackedTexts()
	if len(got) != len(want) {
		harness.t.Fatalf("acked messages = %q, want %q", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			harness.t.Fatalf("acked message %d = %q, want %q (all: %q)", index, got[index], want[index], got)
		}
	}
}

func shanghaiTime(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, testShanghai)
}

// notificationRulesInScope lists the enabled rules of one kind that apply to
// nodeID; no rule means the reconcile loop pauses that node and kind.
func notificationRulesInScope(t *testing.T, store *SQLiteStore, kind, nodeID string) []AdminAlertRule {
	t.Helper()
	snapshot, err := store.NotificationReconcileSnapshot(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("notification snapshot: %v", err)
	}
	return snapshot.rulesFor(kind, nodeID)
}
