package api

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestProductionSnapshotFirstReconcileIsSilent validates the upgrade on a copy
// of the production database. It only runs when ZENO_SNAPSHOT_DB points at a
// writable copy (never the original export):
//
//	ZENO_SNAPSHOT_DB=/work/snapcopy.db ZENO_SNAPSHOT_OUT=/work/snapshot-result.md \
//	  go test -run TestProductionSnapshotFirstReconcileIsSilent -v ./internal/controller/api/
//
// The copy's Telegram credential is encrypted with the production keyring,
// which is not available here, so it is replaced by a plaintext fake that the
// test keyring then encrypts; delivery_version and destination are untouched.
// The reconcile clock is injected at the export instant (12:05:00 +08:00) and
// no stale scanner runs, so the stored statuses are evaluated exactly as they
// were exported instead of all turning offline because no agent reports here.
func TestProductionSnapshotFirstReconcileIsSilent(t *testing.T) {
	path := os.Getenv("ZENO_SNAPSHOT_DB")
	if path == "" {
		t.Skip("ZENO_SNAPSHOT_DB not set")
	}
	ctx := context.Background()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open snapshot copy: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `UPDATE notification_channels SET credential = 'snapshot-fake-bot-token'`); err != nil {
		t.Fatalf("replace credential with a fake: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw copy: %v", err)
	}

	store, err := OpenSQLiteStore(path)
	if err != nil {
		t.Fatalf("open and migrate snapshot copy: %v", err)
	}
	defer store.Close()
	report := &strings.Builder{}
	fmt.Fprintf(report, "## 快照验证（%s）\n\n", path)

	var migrated int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, notificationReconcileSeedMigration).Scan(&migrated); err != nil || migrated != 1 {
		t.Fatalf("seed migration recorded=%d err=%v", migrated, err)
	}
	seeded := readSnapshotStates(t, store)
	fmt.Fprintf(report, "- 迁移 %s 已记录；种子写入 %d 行：%s\n", notificationReconcileSeedMigration, len(seeded), countStatesByKind(seeded))
	var alibabaLastSeen int64
	if err := store.db.QueryRowContext(ctx, `SELECT last_seen_at FROM nodes WHERE id = 'alibaba'`).Scan(&alibabaLastSeen); err != nil {
		t.Fatalf("read alibaba last_seen_at: %v", err)
	}
	tarek := seeded[notificationStateKey{ChannelID: "telegram-d1c2ee3a", NodeID: "tarek-e97b62a6", Kind: notificationKindLiveness}]
	alibaba := seeded[notificationStateKey{ChannelID: "telegram-d1c2ee3a", NodeID: "alibaba", Kind: notificationKindLiveness}]
	if tarek.Notified != livenessStateOnline {
		t.Errorf("Tarek seeded %+v, want notified=online", tarek)
	}
	if alibaba.Notified != livenessStateOffline || alibaba.IncidentFrom != alibabaLastSeen || alibaba.IncidentFrom == 0 {
		t.Errorf("Alibaba seeded %+v, want notified=offline incident_from=%d", alibaba, alibabaLastSeen)
	}
	fmt.Fprintf(report, "- 种子 Tarek：notified=%s incident_from=%d\n", tarek.Notified, tarek.IncidentFrom)
	fmt.Fprintf(report, "- 种子 Alibaba：notified=%s incident_from=%d（%s，= nodes.last_seen_at）\n",
		alibaba.Notified, alibaba.IncidentFrom, time.Unix(alibaba.IncidentFrom, 0).In(testShanghai).Format("2006-01-02 15:04:05"))
	for _, key := range sortedStateKeys(seeded) {
		row := seeded[key]
		if key.Kind == notificationKindLiveness || row.Notified != "" {
			fmt.Fprintf(report, "  - seed %s/%s notified=%q incident_from=%d\n", key.NodeID, key.Kind, row.Notified, row.IncidentFrom)
		}
	}

	enableTestNotificationCredentialEncryption(t, store)
	telegram := newFakeTelegram(t)
	exportInstant := time.Date(2026, 10, 8, 12, 5, 0, 0, testShanghai)
	clock := newFakeNotificationClock(exportInstant)
	h := &handler{
		store:                   store,
		notificationSender:      newHTTPNotificationSender(telegram.server.Client(), telegram.server.URL),
		notificationClock:       clock.Now,
		notificationLoc:         testShanghai,
		notificationSendTimeout: time.Second,
	}
	for round := 0; round < 3; round++ {
		h.reconcileNotifications(ctx)
		clock.Advance(2 * time.Second)
	}
	after := readSnapshotStates(t, store)
	snapshot, err := store.NotificationReconcileSnapshot(ctx, clock.Now())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	expected := 0
	for _, node := range snapshot.Nodes {
		for _, kind := range notificationKinds {
			if node.Disabled || len(snapshot.rulesFor(kind, node.ID)) == 0 {
				continue
			}
			if _, known := livenessActual(node.Status); kind == notificationKindLiveness && !known {
				continue
			}
			expected++
		}
	}
	outcomes := map[string]int{}
	var outcomeRows *sql.Rows
	outcomeRows, err = store.db.QueryContext(ctx, `SELECT outcome, COUNT(*) FROM notification_log GROUP BY outcome`)
	if err != nil {
		t.Fatalf("log outcomes: %v", err)
	}
	for outcomeRows.Next() {
		var outcome string
		var count int
		if err := outcomeRows.Scan(&outcome, &count); err != nil {
			t.Fatalf("scan outcome: %v", err)
		}
		outcomes[outcome] = count
	}
	_ = outcomeRows.Close()
	received := telegram.receivedTexts()
	if len(received) != 0 || outcomes[notificationOutcomeSent] != 0 || outcomes[notificationOutcomeFailed] != 0 || outcomes[notificationOutcomeDropped] != 0 {
		t.Errorf("first reconcile rounds sent %q outcomes=%v, want nothing", received, outcomes)
	}
	if len(after) != expected {
		t.Errorf("state rows after first rounds = %d, want %d (node x kind with an enabled rule in scope and a known actual)", len(after), expected)
	}
	if after[notificationStateKey{ChannelID: "telegram-d1c2ee3a", NodeID: "tarek-e97b62a6", Kind: notificationKindLiveness}].Notified != livenessStateOnline ||
		after[notificationStateKey{ChannelID: "telegram-d1c2ee3a", NodeID: "alibaba", Kind: notificationKindLiveness}].Notified != livenessStateOffline {
		t.Errorf("Tarek/Alibaba changed during the first rounds")
	}
	pending := 0
	for _, row := range after {
		if row.PendingTarget != "" {
			pending++
		}
	}
	if pending != 0 {
		t.Errorf("pending rows after first rounds = %d, want 0", pending)
	}
	fmt.Fprintf(report, "- 注入时钟 %s 起跑 3 轮对账（2 秒间隔）：假 Telegram 收到 %d 条请求；notification_log 各结果=%v\n",
		exportInstant.Format("2006-01-02 15:04:05 -07:00"), len(received), outcomes)
	fmt.Fprintf(report, "- 首轮后 notification_states=%d 行（预期 %d = 节点×类别中有启用规则、actual 可判定的组合）：%s；pending=%d\n",
		len(after), expected, countStatesByKind(after), pending)
	t.Log("\n" + report.String())
	if out := os.Getenv("ZENO_SNAPSHOT_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(report.String()), 0o644); err != nil {
			t.Fatalf("write report: %v", err)
		}
	}
}

func readSnapshotStates(t *testing.T, store *SQLiteStore) map[notificationStateKey]notificationStateRow {
	t.Helper()
	tx, err := store.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	states, err := notificationStateRows(context.Background(), tx)
	if err != nil {
		t.Fatalf("read states: %v", err)
	}
	return states
}

func countStatesByKind(states map[notificationStateKey]notificationStateRow) string {
	counts := map[string]int{}
	for key := range states {
		counts[key.Kind]++
	}
	return fmt.Sprintf("node_offline=%d probe_unhealthy=%d renewal_due=%d",
		counts[notificationKindLiveness], counts[notificationKindResource], counts[notificationKindRenewal])
}

func sortedStateKeys(states map[notificationStateKey]notificationStateRow) []notificationStateKey {
	keys := make([]notificationStateKey, 0, len(states))
	for key := range states {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Kind != keys[j].Kind {
			return keys[i].Kind < keys[j].Kind
		}
		return keys[i].NodeID < keys[j].NodeID
	})
	return keys
}
