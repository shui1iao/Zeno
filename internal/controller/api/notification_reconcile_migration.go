package api

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// notificationReconcileSeedMigration seeds notification_states once from the
// legacy outbox so the first reconcile round after an upgrade sends nothing
// the user has already seen. The legacy tables are only read.
const notificationReconcileSeedMigration = "20261008_notification_reconcile_seed_v1"

func (s *SQLiteStore) seedNotificationStatesFromLegacy(ctx context.Context) error {
	return s.runValidatedSchemaMigration(ctx, notificationReconcileSeedMigration, nil, func(migrationCtx context.Context) error {
		return seedNotificationStatesTx(migrationCtx, s.db, time.Now().UTC())
	})
}

type legacyNotificationChannelBinding struct {
	id          string
	version     int64
	fingerprint string
}

func seedNotificationStatesTx(ctx context.Context, db *sql.DB, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	channels, err := enabledLegacyNotificationBindings(ctx, tx)
	if err != nil {
		return err
	}
	nodes, err := notificationReconcileNodes(ctx, tx)
	if err != nil {
		return err
	}
	rules, err := notificationReconcileRules(ctx, tx)
	if err != nil {
		return err
	}
	scope := notificationReconcileSnapshot{Rules: rules}
	for _, channel := range channels {
		for _, node := range nodes {
			if err := seedNodeNotificationStatesTx(ctx, tx, channel, node, scope, now); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func enabledLegacyNotificationBindings(ctx context.Context, tx *sql.Tx) ([]legacyNotificationChannelBinding, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, delivery_version, destination_fingerprint
		FROM notification_channels
		WHERE enabled = 1
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels := make([]legacyNotificationChannelBinding, 0)
	for rows.Next() {
		var channel legacyNotificationChannelBinding
		if err := rows.Scan(&channel.id, &channel.version, &channel.fingerprint); err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	return channels, rows.Err()
}

func seedNodeNotificationStatesTx(ctx context.Context, tx *sql.Tx, channel legacyNotificationChannelBinding, node notificationReconcileNode, scope notificationReconcileSnapshot, now time.Time) error {
	for _, kind := range []string{notificationKindLiveness, notificationKindResource} {
		row, ok, err := seedStatusNotificationRowTx(ctx, tx, channel, node, kind, now)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := writeSeedNotificationStateTx(ctx, tx, row, node, now); err != nil {
			return err
		}
	}
	rules := scope.rulesFor(notificationKindRenewal, node.ID)
	if len(rules) == 0 {
		return nil
	}
	key, err := seedRenewalNotificationKeyTx(ctx, tx, node, rules, now)
	if err != nil {
		return err
	}
	row := notificationStateRow{
		ChannelID: channel.id, NodeID: node.ID, Kind: notificationKindRenewal, Notified: key,
		ChannelVersion: channel.version, DestinationFingerprint: channel.fingerprint, UpdatedAt: now.Unix(),
	}
	return writeSeedNotificationStateTx(ctx, tx, row, node, now)
}

// seedStatusNotificationRowTx maps the newest delivered row bound to the
// channel's current routing to the state the user last saw. Canceled, failed
// and outcome-unknown rows never count.
func seedStatusNotificationRowTx(ctx context.Context, tx *sql.Tx, channel legacyNotificationChannelBinding, node notificationReconcileNode, kind string, now time.Time) (notificationStateRow, bool, error) {
	var status, eventTS, detail string
	err := tx.QueryRowContext(ctx, `
		SELECT status, event_ts, detail
		FROM notification_deliveries
		WHERE channel_id = ? AND channel_version = ? AND destination_fingerprint = ?
		  AND node_id = ? AND event_type = ? AND state = 'delivered'
		ORDER BY id DESC
		LIMIT 1
	`, channel.id, channel.version, channel.fingerprint, node.ID, kind).Scan(&status, &eventTS, &detail)
	if err == sql.ErrNoRows {
		return notificationStateRow{}, false, nil
	}
	if err != nil {
		return notificationStateRow{}, false, err
	}
	notified, ok := legacyDeliveredNotificationState(kind, strings.TrimSpace(status))
	if !ok {
		return notificationStateRow{}, false, nil
	}
	row := notificationStateRow{
		ChannelID: channel.id, NodeID: node.ID, Kind: kind, Notified: notified,
		ChannelVersion: channel.version, DestinationFingerprint: channel.fingerprint, UpdatedAt: now.Unix(),
	}
	if notificationStateIsAlert(kind, notified) {
		// An offline node's last_seen_at is frozen at the real incident start.
		// A resource warning has no such column (last_seen_at keeps moving), so
		// the delivered alert's event time is the best available start.
		row.IncidentFrom = parseLegacyEventTS(eventTS)
		if kind == notificationKindLiveness && node.Status == "offline" && node.LastSeenAt > 0 {
			row.IncidentFrom = node.LastSeenAt
		}
		if kind == notificationKindResource {
			row.NotifiedDetail = legacyResourceAlertNames(detail)
		}
	}
	return row, true, nil
}

// legacyResourceAlertNames recovers the rule names from a legacy warning
// detail ("CPU、内存持续占用过高" -> "CPU、内存") for the recovery text.
func legacyResourceAlertNames(detail string) string {
	detail = strings.TrimSpace(detail)
	names, found := strings.CutSuffix(detail, "持续占用过高")
	if !found {
		return ""
	}
	return joinResourceAlertNames(strings.Split(names, "、"))
}

func legacyDeliveredNotificationState(kind, status string) (string, bool) {
	switch kind {
	case notificationKindLiveness:
		switch status {
		case "offline":
			return livenessStateOffline, true
		case "online":
			return livenessStateOnline, true
		}
	case notificationKindResource:
		switch status {
		case "warning":
			return resourceStateWarning, true
		case "online":
			return resourceStateOK, true
		}
	}
	return "", false
}

func parseLegacyEventTS(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0
	}
	return parsed.Unix()
}

// seedRenewalNotificationKeyTx treats reminder points before today as already
// notified. Today's point counts only when the legacy scanner already claimed
// today's reminder ("YYYY-MM-DD:dueDate" mark).
func seedRenewalNotificationKeyTx(ctx context.Context, tx *sql.Tx, node notificationReconcileNode, rules []AdminAlertRule, now time.Time) (string, error) {
	key := renewalNotificationKeyAt(node, rules, now, false)
	todayKey := renewalNotificationKeyAt(node, rules, now, true)
	if todayKey == "" || todayKey == key {
		return key, nil
	}
	dueText, _, ok := parseRenewalNotificationKey(todayKey)
	if !ok {
		return key, nil
	}
	mark := renewalNotificationMark(dateOnlyUTC(now.UTC()).Format("2006-01-02"), dueText)
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM notification_event_marks
		WHERE event_type = 'renewal_due' AND node_id = ? AND mark = ?
	`, node.ID, mark).Scan(&exists)
	if err == sql.ErrNoRows {
		return key, nil
	}
	if err != nil {
		return "", err
	}
	return todayKey, nil
}

func writeSeedNotificationStateTx(ctx context.Context, tx *sql.Tx, row notificationStateRow, node notificationReconcileNode, now time.Time) error {
	if err := upsertNotificationStateTx(ctx, tx, row); err != nil {
		return err
	}
	return insertNotificationLogTx(ctx, tx, notificationLogEntry{
		TS: now.Unix(), ChannelID: row.ChannelID, NodeID: row.NodeID, NodeName: node.Name, Kind: row.Kind,
		To: row.Notified, Outcome: notificationOutcomeBaseline,
		Message: fmt.Sprintf("seeded from legacy notifications incident_from=%d", row.IncidentFrom),
	})
}
