package api

import (
	"context"
	"testing"
	"time"
)

type legacyDeliveryRow struct {
	nodeID      string
	eventType   string
	prevStatus  string
	status      string
	eventTS     time.Time
	detail      string
	state       string
	lastError   string
	version     int64
	fingerprint string
	channelID   string
}

func insertLegacyDelivery(t *testing.T, hs *reconcileHarness, row legacyDeliveryRow) {
	t.Helper()
	channel, err := hs.store.AdminNotificationDispatchChannel(hs.ctx, "tg")
	if err != nil {
		t.Fatalf("read channel binding: %v", err)
	}
	if row.version == 0 {
		row.version = channel.DeliveryVersion
	}
	if row.fingerprint == "" {
		row.fingerprint = channel.DestinationFingerprint
	}
	if row.channelID == "" {
		row.channelID = "tg"
	}
	created := row.eventTS.Unix()
	if _, err := hs.store.db.ExecContext(hs.ctx, `
		INSERT INTO notification_deliveries (
			event_id, event_type, node_id, node_name, previous_status, status, event_ts, detail,
			channel_id, channel_name, channel_version, destination_fingerprint,
			state, attempts, next_attempt_at, last_error, created_at, updated_at, delivered_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'Telegram', ?, ?, ?, 1, ?, ?, ?, ?, ?)
	`, formatInt64(created)+row.nodeID+row.status, row.eventType, row.nodeID, row.nodeID, row.prevStatus, row.status,
		row.eventTS.UTC().Format(time.RFC3339), row.detail, row.channelID, row.version, row.fingerprint,
		row.state, created, row.lastError, created, created, created); err != nil {
		t.Fatalf("insert legacy delivery: %v", err)
	}
}

func insertLegacyMark(t *testing.T, hs *reconcileHarness, nodeID, mark string, created time.Time) {
	t.Helper()
	if _, err := hs.store.db.ExecContext(hs.ctx, `
		INSERT INTO notification_event_marks (event_type, node_id, mark, created_at) VALUES ('renewal_due', ?, ?, ?)
	`, nodeID, mark, created.Unix()); err != nil {
		t.Fatalf("insert legacy mark: %v", err)
	}
}

// TestNotificationSeedFromLegacyOutbox runs the one-time seed against legacy
// outbox data shaped like the v1.0.23 production database and checks every
// rule of the seed, then proves the first reconcile round afterwards only
// does what the seeded state implies.
func TestNotificationSeedFromLegacyOutbox(t *testing.T) {
	now := shanghaiTime(2026, 10, 8, 12, 5, 30)
	hs := newReconcileHarness(t, now)
	enabled := false
	if _, err := hs.store.CreateAdminNotificationChannel(hs.ctx, AdminNotificationChannelCreateRequest{
		ID: "off", Name: "Disabled", Destination: "42", Credential: "disabled-secret", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("create disabled channel: %v", err)
	}
	for _, node := range []struct{ id, name string }{
		{"tarek", "Tarek"}, {"alibaba", "Alibaba"}, {"flaky", "Flaky"}, {"rebound", "Rebound"},
		{"failedonly", "FailedOnly"}, {"untouched", "Untouched"}, {"cpu", "CpuNode"}, {"cpuok", "CpuOk"},
		{"r-yesterday", "RYesterday"}, {"r-today-marked", "RTodayMarked"}, {"r-today", "RToday"},
	} {
		hs.addNode(node.id, node.name, "203.0.113.70")
	}
	alibabaLastSeen := shanghaiTime(2026, 10, 6, 20, 33, 27).Unix()
	hs.setLiveness("alibaba", "offline", alibabaLastSeen)
	hs.setLiveness("tarek", "online", now.Unix()-2)

	// Tarek: newest delivered row is online; the later canceled outcome-unknown
	// offline row (production row 339) does not count.
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "tarek", eventType: "node_offline", prevStatus: "online", status: "offline", eventTS: shanghaiTime(2026, 10, 2, 16, 34, 8), state: "delivered"})
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "tarek", eventType: "node_offline", prevStatus: "offline", status: "online", eventTS: shanghaiTime(2026, 10, 2, 16, 34, 20), state: "delivered"})
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "tarek", eventType: "node_offline", prevStatus: "online", status: "offline", eventTS: shanghaiTime(2026, 10, 8, 3, 43, 28), state: "canceled", lastError: "delivery outcome unknown; superseded by newer status"})
	// Alibaba: delivered offline and still offline -> incident from last_seen_at.
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "alibaba", eventType: "node_offline", prevStatus: "online", status: "offline", eventTS: shanghaiTime(2026, 10, 6, 20, 34, 28), state: "delivered"})
	// Flaky: delivered offline, now online (its recovery was never delivered)
	// -> notified offline with incident from the delivered event time.
	flakyOffline := shanghaiTime(2026, 10, 7, 9, 0, 0)
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "flaky", eventType: "node_offline", prevStatus: "online", status: "offline", eventTS: flakyOffline, state: "delivered"})
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "flaky", eventType: "node_offline", prevStatus: "offline", status: "online", eventTS: flakyOffline.Add(5 * time.Minute), state: "failed", lastError: "delivery failed"})
	// Rebound: the newest delivered row belongs to an older routing binding.
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "rebound", eventType: "node_offline", prevStatus: "online", status: "offline", eventTS: shanghaiTime(2026, 10, 1, 9, 0, 0), state: "delivered"})
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "rebound", eventType: "node_offline", prevStatus: "offline", status: "online", eventTS: shanghaiTime(2026, 10, 1, 9, 5, 0), state: "delivered"})
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "rebound", eventType: "node_offline", prevStatus: "online", status: "offline", eventTS: shanghaiTime(2026, 10, 3, 9, 0, 0), state: "delivered", fingerprint: "old-destination"})
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "rebound", eventType: "node_offline", prevStatus: "online", status: "offline", eventTS: shanghaiTime(2026, 10, 4, 9, 0, 0), state: "delivered", version: 7})
	// FailedOnly: only failed / outcome-unknown / canceled rows -> no row.
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "failedonly", eventType: "node_offline", prevStatus: "online", status: "offline", eventTS: shanghaiTime(2026, 10, 5, 9, 0, 0), state: "failed", lastError: "delivery outcome unknown; automatic retry suppressed"})
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "failedonly", eventType: "node_offline", prevStatus: "offline", status: "online", eventTS: shanghaiTime(2026, 10, 5, 9, 1, 0), state: "canceled"})
	// Resource rows.
	cpuWarn := shanghaiTime(2026, 10, 8, 11, 0, 0)
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "cpu", eventType: "probe_unhealthy", prevStatus: "online", status: "warning", eventTS: cpuWarn, detail: "CPU、内存持续占用过高", state: "delivered"})
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "cpuok", eventType: "probe_unhealthy", prevStatus: "online", status: "warning", eventTS: cpuWarn, detail: "CPU持续占用过高", state: "delivered"})
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "cpuok", eventType: "probe_unhealthy", prevStatus: "warning", status: "online", eventTS: cpuWarn.Add(10 * time.Minute), detail: "CPU恢复正常", state: "delivered"})
	hs.setResourceRule("cpu", "cpu_high", true)
	hs.setResourceRule("cpu", "memory_high", true)
	// Rows of a disabled channel never seed anything.
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "untouched", eventType: "node_offline", prevStatus: "online", status: "offline", eventTS: shanghaiTime(2026, 10, 5, 9, 0, 0), state: "delivered", channelID: "off"})

	// Renewal (UTC day 2026-10-08, rule: 3 days before).
	enableRenewalForHarness(t, hs, 3)
	setNodeExpiry(t, hs, "r-yesterday", "2026-10-10", "")
	setNodeExpiry(t, hs, "r-today-marked", "2026-10-11", "")
	setNodeExpiry(t, hs, "r-today", "2026-08-11", "月")
	insertLegacyMark(t, hs, "r-today-marked", "2026-10-08:2026-10-11", now.Add(-4*time.Hour))
	insertLegacyMark(t, hs, "r-today", "2026-10-07:2026-10-11", now.Add(-28*time.Hour))

	if _, err := hs.store.db.ExecContext(hs.ctx, `DELETE FROM notification_states; DELETE FROM notification_log`); err != nil {
		t.Fatalf("reset states: %v", err)
	}
	if err := seedNotificationStatesTx(hs.ctx, hs.store.db, now); err != nil {
		t.Fatalf("seed: %v", err)
	}

	assertStateRow(t, hs, "tarek", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOnline && row.IncidentFrom == 0
	}, "online (canceled outcome-unknown row ignored)")
	assertStateRow(t, hs, "alibaba", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOffline && row.IncidentFrom == alibabaLastSeen
	}, "offline with incident_from = last_seen_at")
	assertStateRow(t, hs, "flaky", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOffline && row.IncidentFrom == flakyOffline.Unix()
	}, "offline with incident_from = delivered event_ts")
	assertStateRow(t, hs, "rebound", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOnline
	}, "online (rows from other bindings ignored)")
	assertStateRow(t, hs, "cpu", notificationKindResource, func(row notificationStateRow) bool {
		return row.Notified == resourceStateWarning && row.IncidentFrom == cpuWarn.Unix() && row.NotifiedDetail == "CPU、内存"
	}, "warning with rule names")
	assertStateRow(t, hs, "cpuok", notificationKindResource, func(row notificationStateRow) bool {
		return row.Notified == resourceStateOK && row.IncidentFrom == 0
	}, "ok")
	for _, nodeID := range []string{"failedonly", "untouched"} {
		if _, ok := hs.state(nodeID, notificationKindLiveness); ok {
			t.Fatalf("%s got a liveness row without a delivered row", nodeID)
		}
	}
	if count := hs.stateCount("channel_id = 'off'"); count != 0 {
		t.Fatalf("disabled channel got %d rows", count)
	}
	for _, nodeID := range []string{"tarek", "failedonly"} {
		if _, ok := hs.state(nodeID, notificationKindResource); ok {
			t.Fatalf("%s got a resource row without a delivered row", nodeID)
		}
	}
	assertStateRow(t, hs, "r-yesterday", notificationKindRenewal, func(row notificationStateRow) bool { return row.Notified == "2026-10-10#3" }, "reminder before today notified")
	assertStateRow(t, hs, "r-today-marked", notificationKindRenewal, func(row notificationStateRow) bool { return row.Notified == "2026-10-11#3" }, "today's reminder already marked")
	assertStateRow(t, hs, "r-today", notificationKindRenewal, func(row notificationStateRow) bool { return row.Notified == "" }, "today's unmarked reminder still to send")
	assertStateRow(t, hs, "untouched", notificationKindRenewal, func(row notificationStateRow) bool { return row.Notified == "" }, "no expiry")
	seeded := hs.stateCount("1 = 1")
	if baselines := hs.logEntries(notificationOutcomeBaseline); len(baselines) != seeded {
		t.Fatalf("seed baseline log entries = %d, want one per seeded row (%d)", len(baselines), seeded)
	}

	// First round after the seed: only the unmarked reminder of today is sent;
	// Flaky's recovery starts its hold window; everything else is silent.
	hs.round()
	hs.expectAcked("⚠️[到期] RToday 将于 3 天后（2026-10-11）到期")
	assertStateRow(t, hs, "flaky", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOffline && row.PendingTarget == livenessStateOnline
	}, "recovery waiting for the hold window")
	hs.after(60 * time.Second)
	texts := hs.tg.ackedTexts()
	if len(texts) != 2 || texts[1] != "🟢[恢复] Flaky(203.0.***.***) 离线 10-7 09:00–10-8 12:05，共 1 天 3 小时" {
		t.Fatalf("acked = %q", texts)
	}
}

// The seed is wired through schema_migrations: it runs once when upgrading a
// database and never again.
func TestNotificationSeedRunsOnceThroughSchemaMigrations(t *testing.T) {
	hs := newReconcileHarness(t, time.Now().UTC())
	hs.addNode("alibaba", "Alibaba", "")
	hs.setLiveness("alibaba", "offline", time.Now().UTC().Add(-time.Hour).Unix())
	insertLegacyDelivery(t, hs, legacyDeliveryRow{nodeID: "alibaba", eventType: "node_offline", prevStatus: "online", status: "offline", eventTS: time.Now().UTC().Add(-time.Hour), state: "delivered"})
	// Simulate a v1.0.23 database: no reconcile state and no seed record.
	if _, err := hs.store.db.ExecContext(hs.ctx, `DELETE FROM notification_states; DELETE FROM notification_log; DELETE FROM schema_migrations WHERE name = ?`, notificationReconcileSeedMigration); err != nil {
		t.Fatalf("simulate legacy database: %v", err)
	}
	hs.reopenStore()
	assertStateRow(t, hs, "alibaba", notificationKindLiveness, func(row notificationStateRow) bool {
		return row.Notified == livenessStateOffline
	}, "seeded on upgrade")
	var recorded int
	if err := hs.store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, notificationReconcileSeedMigration).Scan(&recorded); err != nil || recorded != 1 {
		t.Fatalf("seed migration recorded=%d err=%v", recorded, err)
	}
	if _, err := hs.store.db.ExecContext(hs.ctx, `DELETE FROM notification_states`); err != nil {
		t.Fatalf("clear states: %v", err)
	}
	hs.reopenStore()
	if count := hs.stateCount("1 = 1"); count != 0 {
		t.Fatalf("seed ran again on the next start: %d rows", count)
	}
}
