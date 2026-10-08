package api

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Integration tests for the reconcile loop: real SQLite store, real
// reconcile round, local fake Telegram server and an injected clock.

func assertStateRow(t *testing.T, hs *reconcileHarness, nodeID, kind string, check func(notificationStateRow) bool, describe string) notificationStateRow {
	t.Helper()
	row, ok := hs.state(nodeID, kind)
	if !ok {
		t.Fatalf("%s/%s: no state row, want %s", nodeID, kind, describe)
	}
	if !check(row) {
		t.Fatalf("%s/%s: row %+v, want %s", nodeID, kind, row, describe)
	}
	return row
}

// (a) 2026-10-08 Tarek: the offline request reached Telegram but the response
// never arrived. That is a failure, retried; the recovery is sent after the
// hold window and carries the interval and duration.
func TestReconcileTarekTimeoutIsRetriedAndRecoveryCarriesInterval(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	hs := newReconcileHarness(t, shanghaiTime(2026, 10, 8, 3, 40, 0))
	hs.addNode("tarek", "Tarek", "203.0.113.9")
	hs.round()
	if got := hs.tg.receivedTexts(); len(got) != 0 {
		t.Fatalf("baseline sent %q", got)
	}

	lastSeen := shanghaiTime(2026, 10, 8, 3, 42, 27)
	hs.setLiveness("tarek", "offline", lastSeen.Unix())
	hs.tg.setMode("hang")
	hs.at(shanghaiTime(2026, 10, 8, 3, 43, 28))
	offlineText := "🔴[离线] Tarek(203.0.***.***)"
	if got := hs.tg.receivedTexts(); len(got) != 1 || got[0] != offlineText {
		t.Fatalf("written requests = %q, want the offline alert", got)
	}
	hs.expectAcked()
	assertStateRow(t, hs, "tarek", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOnline && row.PendingTarget == livenessStateOffline && row.PendingAttempts == 1 &&
			row.LastError == "delivery timed out" && row.IncidentFrom == lastSeen.Unix()
	}, "a retryable failed alert")
	if failed := hs.logEntries(notificationOutcomeFailed); len(failed) != 1 || failed[0].Error != "delivery timed out" || failed[0].Attempt != 1 {
		t.Fatalf("failed log = %+v", failed)
	}

	hs.tg.setMode("ok")
	hs.after(time.Second)
	if got := hs.tg.receivedTexts(); len(got) != 1 {
		t.Fatalf("retried inside the 2s channel backoff: %q", got)
	}
	hs.after(time.Second)
	hs.expectAcked(offlineText)
	assertStateRow(t, hs, "tarek", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOffline && row.PendingTarget == "" && row.PendingAttempts == 0 && row.IncidentFrom == lastSeen.Unix()
	}, "notified offline after the retry")

	recoveredAt := shanghaiTime(2026, 10, 8, 3, 43, 47)
	hs.setLiveness("tarek", "online", recoveredAt.Unix())
	hs.at(recoveredAt)
	hs.at(recoveredAt.Add(59 * time.Second))
	hs.expectAcked(offlineText)
	hs.at(recoveredAt.Add(60 * time.Second))
	hs.expectAcked(offlineText, "🟢[恢复] Tarek(203.0.***.***) 离线 03:42:27–03:43:47，共 1 分 20 秒")
	if got := hs.tg.receivedTexts(); len(got) != 3 {
		t.Fatalf("written requests = %q, want one accepted duplicate alert plus the recovery", got)
	}
	assertStateRow(t, hs, "tarek", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOnline && row.PendingTarget == "" && row.IncidentFrom == 0
	}, "settled online")
	if strings.Contains(logs.String(), "telegram-bot-secret-value") {
		t.Fatalf("container log leaked the bot credential:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "notification sent channel_id=tg node_id=tarek kind=node_offline from=offline to=online") {
		t.Fatalf("container log missing the sent line:\n%s", logs.String())
	}
}

// (b) Telegram is down for ten minutes while two nodes flap. Afterwards only
// the final difference is delivered.
func TestReconcileTelegramOutageDeliversOnlyTheFinalDifference(t *testing.T) {
	start := shanghaiTime(2026, 10, 5, 10, 0, 0)
	hs := newReconcileHarness(t, start)
	hs.addNode("a", "Alpha", "198.51.100.7")
	hs.addNode("b", "Beta", "")
	hs.round()
	hs.tg.setMode("down")

	type change struct {
		offset int64
		node   string
		status string
	}
	schedule := []change{
		{10, "a", "offline"}, {20, "b", "offline"}, {40, "a", "online"}, {50, "b", "online"},
		{100, "a", "offline"}, {130, "a", "online"}, {200, "b", "offline"}, {260, "b", "online"},
		{400, "a", "offline"},
	}
	next := 0
	for offset := int64(2); offset <= 600; offset += 2 {
		for next < len(schedule) && schedule[next].offset <= offset {
			lastSeen := start.Unix() + offset
			if schedule[next].status == "offline" {
				lastSeen -= 61
			}
			hs.setLiveness(schedule[next].node, schedule[next].status, lastSeen)
			next++
		}
		hs.at(start.Add(time.Duration(offset) * time.Second))
	}
	if got := hs.tg.receivedTexts(); len(got) != 0 {
		t.Fatalf("telegram received %q while down", got)
	}
	hs.tg.setMode("ok")
	for offset := int64(602); offset <= 760; offset += 2 {
		hs.at(start.Add(time.Duration(offset) * time.Second))
	}
	hs.expectAcked("🔴[离线] Alpha(198.51.***.***)")
	for _, node := range []string{"a", "b"} {
		dropped := 0
		for _, entry := range hs.logEntries(notificationOutcomeDropped) {
			if entry.NodeID == node {
				dropped++
			}
		}
		if dropped == 0 {
			t.Fatalf("node %s: no dropped log for its undelivered flaps", node)
		}
	}
	assertStateRow(t, hs, "b", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOnline && row.PendingTarget == ""
	}, "b settled online without messages")

	hs.setLiveness("a", "online", start.Unix()+800)
	hs.at(start.Add(800 * time.Second))
	hs.at(start.Add(860 * time.Second))
	hs.expectAcked("🔴[离线] Alpha(198.51.***.***)", "🟢[恢复] Alpha(198.51.***.***) 离线 10:05:39–10:13:20，共 7 分 41 秒")
}

// (c) 2026-10-03: the Controller itself lost connectivity, every node went
// offline at once, every send failed, and every node came back. Nothing is
// sent; each node gets exactly one dropped log entry.
func TestReconcileControllerOutageSendsNothingAndDropsEachNodeOnce(t *testing.T) {
	start := shanghaiTime(2026, 10, 3, 11, 40, 0)
	hs := newReconcileHarness(t, start)
	nodes := []string{"sharon", "alibaba", "hytron", "datawave-hk", "aws", "jcloud"}
	for _, node := range nodes {
		hs.addNode(node, strings.ToUpper(node[:1])+node[1:], "192.0.2.10")
	}
	hs.round()
	hs.tg.setMode("down")
	offlineAt := shanghaiTime(2026, 10, 3, 11, 41, 46)
	for _, node := range nodes {
		hs.setLiveness(node, "offline", offlineAt.Unix()-61)
	}
	hs.at(offlineAt)
	backAt := offlineAt.Add(time.Second)
	for _, node := range nodes {
		hs.setLiveness(node, "online", backAt.Unix())
	}
	hs.at(backAt)
	hs.tg.setMode("ok")
	for offset := 2; offset <= 300; offset += 2 {
		hs.at(backAt.Add(time.Duration(offset) * time.Second))
	}
	if got := hs.tg.receivedTexts(); len(got) != 0 {
		t.Fatalf("telegram received %q, want nothing", got)
	}
	perNode := map[string]int{}
	for _, entry := range hs.logEntries(notificationOutcomeDropped) {
		if entry.Kind != notificationKindLiveness || entry.From != livenessStateOnline || entry.To != livenessStateOffline {
			t.Fatalf("unexpected dropped entry %+v", entry)
		}
		if !strings.Contains(entry.Message, "未送达") || !strings.Contains(entry.Message, "离线") {
			t.Fatalf("dropped message %q does not explain the undelivered state", entry.Message)
		}
		perNode[entry.NodeID]++
	}
	for _, node := range nodes {
		if perNode[node] != 1 {
			t.Fatalf("dropped entries per node = %v, want exactly one for %s", perNode, node)
		}
		assertStateRow(t, hs, node, notificationKindLiveness, func(row notificationStateRow) bool {
			return row.Notified == livenessStateOnline && row.PendingTarget == "" && row.PendingAttempts == 0 && row.LastError == ""
		}, "settled online")
	}
}

// (d) A Controller restart in the middle of an incident keeps notified and
// incident_from; the recovery still carries the full interval.
func TestReconcileRestartDuringIncidentKeepsIncidentAndSendsRecovery(t *testing.T) {
	hs := newReconcileHarness(t, shanghaiTime(2026, 10, 7, 23, 50, 0))
	hs.addNode("bero", "Bero", "203.0.113.20")
	hs.round()
	lastSeen := shanghaiTime(2026, 10, 7, 23, 58, 10)
	hs.setLiveness("bero", "offline", lastSeen.Unix())
	hs.at(shanghaiTime(2026, 10, 7, 23, 59, 11))
	hs.expectAcked("🔴[离线] Bero(203.0.***.***)")

	hs.reopenStore()
	assertStateRow(t, hs, "bero", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOffline && row.IncidentFrom == lastSeen.Unix()
	}, "the incident to survive the restart")
	hs.at(shanghaiTime(2026, 10, 8, 0, 1, 0))
	hs.expectAcked("🔴[离线] Bero(203.0.***.***)")

	backAt := shanghaiTime(2026, 10, 8, 0, 3, 20)
	hs.setLiveness("bero", "online", backAt.Unix())
	hs.at(backAt)
	hs.reopenStore()
	hs.at(backAt.Add(60 * time.Second))
	hs.expectAcked("🔴[离线] Bero(203.0.***.***)", "🟢[恢复] Bero(203.0.***.***) 离线 10-7 23:58–10-8 00:03，共 5 分 10 秒")
}

type crashBeforeStateWriteStore struct {
	*SQLiteStore
	crash atomic.Bool
}

func (store *crashBeforeStateWriteStore) ApplyNotificationStates(ctx context.Context, rows []notificationStateRow, logs []notificationLogEntry) error {
	for _, entry := range logs {
		if entry.Outcome == notificationOutcomeSent && store.crash.Load() {
			return errors.New("simulated process crash before the state write")
		}
	}
	return store.SQLiteStore.ApplyNotificationStates(ctx, rows, logs)
}

// (e) The send succeeded but the process died before notified was written:
// after the restart the message is sent once more (accepted duplicate).
func TestReconcileCrashAfterSendBeforeWriteResendsOnce(t *testing.T) {
	hs := newReconcileHarness(t, shanghaiTime(2026, 10, 6, 12, 0, 0))
	hs.addNode("alibaba", "Alibaba", "203.0.113.30")
	crashing := &crashBeforeStateWriteStore{SQLiteStore: hs.store}
	hs.h = hs.newHandler(crashing)
	hs.round()
	hs.setLiveness("alibaba", "offline", hs.now().Unix())
	crashing.crash.Store(true)
	hs.after(70 * time.Second)
	hs.expectAcked("🔴[离线] Alibaba(203.0.***.***)")
	assertStateRow(t, hs, "alibaba", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOnline && row.PendingTarget == livenessStateOffline
	}, "notified not advanced because the write never happened")

	hs.restart()
	hs.after(2 * time.Second)
	hs.after(2 * time.Second)
	hs.expectAcked("🔴[离线] Alibaba(203.0.***.***)", "🔴[离线] Alibaba(203.0.***.***)")
	assertStateRow(t, hs, "alibaba", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOffline && row.PendingTarget == ""
	}, "notified offline after the resend")
}

// (f) Pause conditions: nothing is sent or advanced, pending is kept, and
// after resuming only the current difference is reconciled.
func TestReconcilePausesForChannelRuleNodeAndGlobalGate(t *testing.T) {
	t.Run("channel disabled then enabled", func(t *testing.T) {
		hs := newReconcileHarness(t, shanghaiTime(2026, 10, 6, 9, 0, 0))
		hs.addNode("n1", "Node One", "")
		hs.round()
		disabled, enabled := false, true
		if _, err := hs.store.UpdateAdminNotificationChannel(hs.ctx, "tg", AdminNotificationChannelUpdateRequest{Enabled: &disabled}); err != nil {
			t.Fatalf("disable channel: %v", err)
		}
		hs.setLiveness("n1", "offline", hs.now().Unix())
		for i := 0; i < 30; i++ {
			hs.after(2 * time.Second)
		}
		hs.expectAcked()
		assertStateRow(t, hs, "n1", notificationKindLiveness, func(row notificationStateRow) bool {
			return row.Notified == livenessStateOnline && row.PendingTarget == ""
		}, "untouched while the channel is disabled")
		if _, err := hs.store.UpdateAdminNotificationChannel(hs.ctx, "tg", AdminNotificationChannelUpdateRequest{Enabled: &enabled}); err != nil {
			t.Fatalf("enable channel: %v", err)
		}
		hs.after(2 * time.Second)
		hs.expectAcked("🔴[离线] Node One")

		if _, err := hs.store.UpdateAdminNotificationChannel(hs.ctx, "tg", AdminNotificationChannelUpdateRequest{Enabled: &disabled}); err != nil {
			t.Fatalf("disable channel: %v", err)
		}
		hs.setLiveness("n1", "online", hs.now().Unix())
		for i := 0; i < 60; i++ {
			hs.after(2 * time.Second)
		}
		hs.expectAcked("🔴[离线] Node One")
		if _, err := hs.store.UpdateAdminNotificationChannel(hs.ctx, "tg", AdminNotificationChannelUpdateRequest{Enabled: &enabled}); err != nil {
			t.Fatalf("enable channel: %v", err)
		}
		hs.after(2 * time.Second)
		hs.after(60 * time.Second)
		texts := hs.tg.ackedTexts()
		if len(texts) != 2 || !strings.HasPrefix(texts[1], "🟢[恢复] Node One 离线 ") {
			t.Fatalf("acked = %q, want the recovery reconciled after re-enabling", texts)
		}
	})

	t.Run("rule disabled then enabled keeps pending", func(t *testing.T) {
		hs := newReconcileHarness(t, shanghaiTime(2026, 10, 6, 9, 0, 0))
		hs.addNode("n1", "Node One", "")
		hs.round()
		hs.tg.setMode("fail")
		hs.setLiveness("n1", "offline", hs.now().Unix())
		hs.after(2 * time.Second)
		disabled, enabled := false, true
		if _, err := hs.store.UpdateAdminAlertRule(hs.ctx, "node_offline", AdminAlertRuleUpdateRequest{Enabled: &disabled}); err != nil {
			t.Fatalf("disable rule: %v", err)
		}
		hs.tg.setMode("ok")
		for i := 0; i < 40; i++ {
			hs.after(2 * time.Second)
		}
		hs.expectAcked()
		assertStateRow(t, hs, "n1", notificationKindLiveness, func(row notificationStateRow) bool {
			return row.Notified == livenessStateOnline && row.PendingTarget == livenessStateOffline && row.PendingAttempts == 1
		}, "pending kept while the rule is disabled")
		if _, err := hs.store.UpdateAdminAlertRule(hs.ctx, "node_offline", AdminAlertRuleUpdateRequest{Enabled: &enabled}); err != nil {
			t.Fatalf("enable rule: %v", err)
		}
		hs.after(2 * time.Second)
		hs.expectAcked("🔴[离线] Node One")

		scope := []string{"other"}
		if _, err := hs.store.CreateAdminNode(hs.ctx, AdminNodeCreateRequest{ID: "other", DisplayName: "Other"}); err != nil {
			t.Fatalf("create other node: %v", err)
		}
		if _, err := hs.store.UpdateAdminAlertRule(hs.ctx, "node_offline", AdminAlertRuleUpdateRequest{ScopeNodeIDs: &scope}); err != nil {
			t.Fatalf("scope rule: %v", err)
		}
		hs.setLiveness("n1", "online", hs.now().Unix())
		for i := 0; i < 40; i++ {
			hs.after(2 * time.Second)
		}
		hs.expectAcked("🔴[离线] Node One")
	})

	t.Run("disabled node", func(t *testing.T) {
		hs := newReconcileHarness(t, shanghaiTime(2026, 10, 6, 9, 0, 0))
		hs.addNode("n1", "Node One", "")
		hs.round()
		disabled, enabled := true, false
		if _, err := hs.store.UpdateAdminNode(hs.ctx, "n1", AdminNodeUpdateRequest{Disabled: &disabled}); err != nil {
			t.Fatalf("disable node: %v", err)
		}
		hs.setLiveness("n1", "offline", hs.now().Unix())
		for i := 0; i < 20; i++ {
			hs.after(2 * time.Second)
		}
		hs.expectAcked()
		if _, err := hs.store.UpdateAdminNode(hs.ctx, "n1", AdminNodeUpdateRequest{Disabled: &enabled}); err != nil {
			t.Fatalf("enable node: %v", err)
		}
		hs.after(2 * time.Second)
		hs.expectAcked("🔴[离线] Node One")
	})

	t.Run("notifications globally unavailable", func(t *testing.T) {
		hs := newReconcileHarness(t, shanghaiTime(2026, 10, 6, 9, 0, 0))
		hs.addNode("n1", "Node One", "")
		hs.h.notificationSender = nil
		hs.setLiveness("n1", "offline", hs.now().Unix())
		for i := 0; i < 10; i++ {
			hs.after(2 * time.Second)
		}
		if count := hs.stateCount("1 = 1"); count != 0 {
			t.Fatalf("state rows written while notifications are unavailable: %d", count)
		}
		hs.restart()
		hs.after(2 * time.Second)
		hs.expectAcked()
		assertStateRow(t, hs, "n1", notificationKindLiveness, func(row notificationStateRow) bool {
			return row.Notified == livenessStateOffline
		}, "baseline once notifications become available")
	})
}

// (g) A routing change re-baselines silently; deleting a node or a channel
// removes its rows.
func TestReconcileRoutingChangeRebaselinesAndDeletionsClearRows(t *testing.T) {
	hs := newReconcileHarness(t, shanghaiTime(2026, 10, 6, 9, 0, 0))
	hs.addNode("n1", "Node One", "")
	hs.addNode("n2", "Node Two", "")
	hs.round()
	hs.setLiveness("n1", "offline", hs.now().Unix())
	hs.after(2 * time.Second)
	hs.expectAcked("🔴[离线] Node One")

	destination := "new-chat"
	if _, err := hs.store.UpdateAdminNotificationChannel(hs.ctx, "tg", AdminNotificationChannelUpdateRequest{Destination: &destination}); err != nil {
		t.Fatalf("change destination: %v", err)
	}
	hs.setLiveness("n1", "online", hs.now().Unix())
	for i := 0; i < 60; i++ {
		hs.after(2 * time.Second)
	}
	hs.expectAcked("🔴[离线] Node One")
	rebaselines := hs.logEntries(notificationOutcomeRebaseline)
	if len(rebaselines) != 4 {
		t.Fatalf("rebaseline log entries = %+v, want one per node and kind", rebaselines)
	}
	assertStateRow(t, hs, "n1", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOnline && row.ChannelVersion == 2 && row.DestinationFingerprint == notificationDestinationFingerprint("telegram", destination)
	}, "re-baselined on the new binding")
	hs.setLiveness("n1", "offline", hs.now().Unix())
	hs.after(2 * time.Second)
	forms := hs.tg.ackedForms()
	if len(forms) != 2 || !strings.Contains(forms[1], "chat_id=new-chat") {
		t.Fatalf("acked forms = %q, want the next alert on the new destination", forms)
	}

	if err := hs.store.DeleteAdminNode(hs.ctx, "n1"); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	waitUntil(t, 10*time.Second, func() bool {
		var exists int
		_ = hs.store.db.QueryRowContext(hs.ctx, `SELECT COUNT(*) FROM nodes WHERE id = 'n1'`).Scan(&exists)
		return exists == 0
	})
	if count := hs.stateCount("node_id = 'n1'"); count != 0 {
		t.Fatalf("state rows for the deleted node = %d, want cascade delete", count)
	}
	hs.after(2 * time.Second)
	if count := hs.stateCount("node_id = 'n1'"); count != 0 {
		t.Fatalf("state rows recreated for the deleted node = %d", count)
	}
	if err := hs.store.DeleteAdminNotificationChannel(hs.ctx, "tg"); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	if count := hs.stateCount("channel_id = 'tg'"); count != 0 {
		t.Fatalf("state rows for the deleted channel = %d, want 0", count)
	}
}

// (h) Resource warnings: the alert is immediate, the recovery needs 300s of
// stability, and a relapse inside the window sends nothing.
func TestReconcileResourceWarningHoldsRecoveryForFiveMinutes(t *testing.T) {
	start := shanghaiTime(2026, 10, 6, 14, 0, 0)
	hs := newReconcileHarness(t, start)
	hs.addNode("hk", "DataWave HK", "203.0.113.40")
	hs.round()
	hs.setResourceRule("hk", "cpu_high", true)
	hs.at(start.Add(10 * time.Second))
	hs.expectAcked("⚠️[警告] DataWave HK(203.0.***.***)CPU持续占用过高")
	assertStateRow(t, hs, "hk", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOnline
	}, "warning status counts as online for liveness")

	hs.setResourceRule("hk", "memory_high", true)
	hs.at(start.Add(20 * time.Second))
	hs.expectAcked("⚠️[警告] DataWave HK(203.0.***.***)CPU持续占用过高")

	hs.setResourceRule("hk", "cpu_high", false)
	hs.setResourceRule("hk", "memory_high", false)
	hs.at(start.Add(100 * time.Second))
	hs.setResourceRule("hk", "cpu_high", true)
	hs.at(start.Add(300 * time.Second))
	hs.setResourceRule("hk", "cpu_high", false)
	hs.at(start.Add(350 * time.Second))
	hs.at(start.Add(649 * time.Second))
	hs.expectAcked("⚠️[警告] DataWave HK(203.0.***.***)CPU持续占用过高")
	if dropped := hs.logEntries(notificationOutcomeDropped); len(dropped) != 0 {
		t.Fatalf("hold-window relapse logged as dropped: %+v", dropped)
	}
	hs.at(start.Add(650 * time.Second))
	hs.expectAcked("⚠️[警告] DataWave HK(203.0.***.***)CPU持续占用过高",
		"🟢[恢复] DataWave HK(203.0.***.***)CPU恢复正常，异常 14:00:10–14:05:50，共 5 分 40 秒")
}

func enableRenewalForHarness(t *testing.T, hs *reconcileHarness, days ...int) {
	t.Helper()
	enabled := true
	update := AdminAlertRuleUpdateRequest{Enabled: &enabled}
	if len(days) > 0 {
		update.RenewalDays = &days
	}
	if _, err := hs.store.UpdateAdminAlertRule(hs.ctx, "renewal_due", update); err != nil {
		t.Fatalf("enable renewal rule: %v", err)
	}
}

func setNodeExpiry(t *testing.T, hs *reconcileHarness, nodeID, expiry, cycle string) {
	t.Helper()
	update := AdminNodeUpdateRequest{ExpiryDate: &expiry}
	if cycle != "" {
		update.BillingCycle = &cycle
	}
	if _, err := hs.store.UpdateAdminNode(hs.ctx, nodeID, update); err != nil {
		t.Fatalf("set expiry: %v", err)
	}
}

// (i) Renewal reminders.
func TestReconcileRenewalReminders(t *testing.T) {
	t.Run("sent on the reminder day and never repeated for the same key", func(t *testing.T) {
		hs := newReconcileHarness(t, shanghaiTime(2026, 10, 6, 10, 0, 0))
		hs.addNode("sharon", "Sharon", "203.0.113.50")
		setNodeExpiry(t, hs, "sharon", "2026-10-10", "")
		enableRenewalForHarness(t, hs)
		hs.round()
		hs.at(shanghaiTime(2026, 10, 7, 7, 59, 0))
		hs.expectAcked()
		hs.at(shanghaiTime(2026, 10, 7, 8, 0, 30))
		want := "⚠️[到期] Sharon 将于 3 天后（2026-10-10）到期"
		hs.expectAcked(want)
		for _, at := range []time.Time{shanghaiTime(2026, 10, 7, 20, 0, 0), shanghaiTime(2026, 10, 9, 9, 0, 0), shanghaiTime(2026, 10, 12, 9, 0, 0)} {
			hs.at(at)
		}
		hs.expectAcked(want)
		assertStateRow(t, hs, "sharon", notificationKindRenewal, func(row notificationStateRow) bool {
			return row.Notified == "2026-10-10#3"
		}, "renewal key notified")
	})

	t.Run("controller down on the reminder day catches up once the next day", func(t *testing.T) {
		hs := newReconcileHarness(t, shanghaiTime(2026, 10, 6, 10, 0, 0))
		hs.addNode("sharon", "Sharon", "")
		setNodeExpiry(t, hs, "sharon", "2026-10-10", "")
		enableRenewalForHarness(t, hs)
		hs.round()
		hs.at(shanghaiTime(2026, 10, 8, 9, 15, 0))
		want := "⚠️[到期] Sharon 将于 2 天后（2026-10-10）到期"
		hs.expectAcked(want)
		hs.at(shanghaiTime(2026, 10, 9, 9, 15, 0))
		hs.expectAcked(want)
	})

	t.Run("first baseline on the reminder day still sends today's reminder", func(t *testing.T) {
		hs := newReconcileHarness(t, shanghaiTime(2026, 10, 7, 10, 0, 0))
		hs.addNode("aws", "AWS", "")
		setNodeExpiry(t, hs, "aws", "2026-08-10", "月")
		enableRenewalForHarness(t, hs)
		hs.round()
		hs.expectAcked("⚠️[到期] AWS 将于 3 天后（2026-10-10）到期")
		if baselines := hs.logEntries(notificationOutcomeBaseline); len(baselines) == 0 {
			t.Fatal("first sight was not logged as a baseline")
		}
	})

	t.Run("only the newest due reminder is caught up and a cleared key is silent", func(t *testing.T) {
		hs := newReconcileHarness(t, shanghaiTime(2026, 10, 1, 10, 0, 0))
		hs.addNode("jcloud", "JCloud", "")
		setNodeExpiry(t, hs, "jcloud", "2026-10-15", "")
		enableRenewalForHarness(t, hs, 1, 3, 7)
		hs.round()
		hs.at(shanghaiTime(2026, 10, 8, 9, 0, 0))
		hs.expectAcked("⚠️[到期] JCloud 将于 7 天后（2026-10-15）到期")
		hs.at(shanghaiTime(2026, 10, 13, 9, 0, 0))
		hs.expectAcked("⚠️[到期] JCloud 将于 7 天后（2026-10-15）到期", "⚠️[到期] JCloud 将于 2 天后（2026-10-15）到期")
		permanent := true
		if _, err := hs.store.UpdateAdminNode(hs.ctx, "jcloud", AdminNodeUpdateRequest{ExpiryPermanent: &permanent}); err != nil {
			t.Fatalf("set permanent: %v", err)
		}
		hs.at(shanghaiTime(2026, 10, 14, 9, 0, 0))
		hs.expectAcked("⚠️[到期] JCloud 将于 7 天后（2026-10-15）到期", "⚠️[到期] JCloud 将于 2 天后（2026-10-15）到期")
		assertStateRow(t, hs, "jcloud", notificationKindRenewal, func(row notificationStateRow) bool {
			return row.Notified == ""
		}, "cleared key recorded silently")
	})
}

// (j) Telegram 429: the channel waits exactly parameters.retry_after.
func TestReconcileTelegramRateLimitHonoursRetryAfter(t *testing.T) {
	hs := newReconcileHarness(t, shanghaiTime(2026, 10, 6, 9, 0, 0))
	hs.addNode("n1", "Node One", "")
	hs.round()
	hs.tg.setRateLimit(7)
	hs.setLiveness("n1", "offline", hs.now().Unix())
	hs.after(time.Second)
	if got := hs.tg.receivedTexts(); len(got) != 1 {
		t.Fatalf("requests = %q, want the rate-limited attempt", got)
	}
	failed := hs.logEntries(notificationOutcomeFailed)
	if len(failed) != 1 || failed[0].Error != "telegram rate limited; retry after 7s" {
		t.Fatalf("failed log = %+v", failed)
	}
	hs.tg.setMode("ok")
	hs.after(2 * time.Second)
	hs.after(2 * time.Second)
	hs.after(2 * time.Second)
	if got := hs.tg.receivedTexts(); len(got) != 1 {
		t.Fatalf("retried before retry_after elapsed: %q", got)
	}
	hs.after(time.Second)
	hs.expectAcked("🔴[离线] Node One")
}

// (k) The very first round only records baselines.
func TestReconcileFirstStartIsSilentBaseline(t *testing.T) {
	hs := newReconcileHarness(t, shanghaiTime(2026, 10, 8, 12, 5, 0))
	hs.addNode("online", "Online", "")
	hs.addNode("offline", "Offline", "")
	hs.addNode("warning", "Warning", "")
	hs.addNode("nodata", "No Data", "")
	hs.addNode("renewal", "Renewal", "")
	offlineSeen := hs.now().Add(-26 * time.Hour).Unix()
	hs.setLiveness("offline", "offline", offlineSeen)
	hs.setResourceRule("warning", "disk_high", true)
	if _, err := hs.store.db.ExecContext(hs.ctx, `UPDATE nodes SET status = 'no_data', last_seen_at = NULL WHERE id = 'nodata'`); err != nil {
		t.Fatalf("set no_data: %v", err)
	}
	setNodeExpiry(t, hs, "renewal", "2026-10-10", "")
	enableRenewalForHarness(t, hs)
	hs.round()
	hs.after(2 * time.Second)
	if got := hs.tg.receivedTexts(); len(got) != 0 {
		t.Fatalf("first start sent %q", got)
	}
	// 5 nodes x 3 kinds minus liveness for the no_data node.
	if count := hs.stateCount("1 = 1"); count != 14 {
		t.Fatalf("state rows = %d, want 14", count)
	}
	if baselines := hs.logEntries(notificationOutcomeBaseline); len(baselines) != 14 {
		t.Fatalf("baseline log entries = %d, want 14", len(baselines))
	}
	assertStateRow(t, hs, "offline", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOffline && row.IncidentFrom == offlineSeen
	}, "offline baseline with incident start")
	assertStateRow(t, hs, "warning", notificationKindResource, func(row notificationStateRow) bool {
		return row.Notified == resourceStateWarning && row.NotifiedDetail == "硬盘"
	}, "warning baseline with rule names")
	assertStateRow(t, hs, "renewal", notificationKindRenewal, func(row notificationStateRow) bool {
		return row.Notified == "2026-10-10#3"
	}, "renewal reminder before today counted as notified")
	if _, ok := hs.state("nodata", notificationKindLiveness); ok {
		t.Fatal("no_data node got a liveness row")
	}
}

func TestReconcileWakeIsNonBlockingAndLoopRunsOnWake(t *testing.T) {
	hs := newReconcileHarness(t, shanghaiTime(2026, 10, 6, 9, 0, 0))
	hs.addNode("n1", "Node One", "")
	hs.round()
	for i := 0; i < 100; i++ {
		hs.h.wakeNotificationReconcile()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		hs.h.runNotificationReconcileLoop(ctx, time.Hour)
	}()
	hs.setLiveness("n1", "offline", hs.now().Unix())
	hs.h.wakeNotificationReconcile()
	waitUntil(t, 3*time.Second, func() bool { return len(hs.tg.ackedTexts()) == 1 })
	cancel()
	<-done
	hs.expectAcked("🔴[离线] Node One")
}
