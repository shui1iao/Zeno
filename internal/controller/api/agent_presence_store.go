package api

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func (s *sqliteAgentDomain) RecordAgentHeartbeat(ctx context.Context, nodeID string, ts time.Time, status, agentVersion string) error {
	_, err := s.RecordAgentHeartbeatTransition(ctx, nodeID, ts, status, agentVersion)
	return err
}

func (s *sqliteAgentDomain) RecordAgentHeartbeatTransition(ctx context.Context, nodeID string, ts time.Time, status, agentVersion string) (notificationStatusTransition, error) {
	var transition notificationStatusTransition
	err := s.writes.withAgentWrite(ctx, nodeID, func(ctx context.Context) error {
		var err error
		transition, err = s.recordAgentHeartbeatTransitionOnce(ctx, nodeID, ts, status, agentVersion)
		return err
	})
	return transition, err
}

func (s *sqliteAgentDomain) recordAgentHeartbeatTransitionOnce(ctx context.Context, nodeID string, ts time.Time, status, agentVersion string) (notificationStatusTransition, error) {
	now := time.Now().UTC()
	nowUnix := now.Unix()
	seenAt := nowUnix
	status = normalizeHeartbeatStatus(status)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	// Acquire SQLite's write reservation before taking the read snapshot. A
	// deferred read-then-write transaction can lose an upgrade race against a
	// concurrent state report and return SQLITE_BUSY immediately even with a
	// busy timeout configured.
	if err := lockAgentNodeWriteTx(ctx, tx, nodeID); err != nil {
		return notificationStatusTransition{}, err
	}

	var previous notificationNodeSnapshot
	var storedStatus string
	var offlineIncident int
	if err := tx.QueryRowContext(ctx, `
		SELECT n.id, n.display_name, n.status, COALESCE(n.public_ipv4, ''),
		       CASE WHEN
		         EXISTS (
		           SELECT 1 FROM alert_rule_states ars
		           WHERE ars.node_id = n.id AND ars.rule_id = 'node_offline' AND ars.active = 1
		         )
		       THEN 1 ELSE 0 END
		FROM nodes n
		WHERE n.id = ? AND n.disabled = 0
	`, nodeID).Scan(&previous.ID, &previous.DisplayName, &storedStatus, &previous.PublicIPv4, &offlineIncident); err != nil {
		if err == sql.ErrNoRows {
			return notificationStatusTransition{}, errNodeNotFound
		}
		return notificationStatusTransition{}, err
	}
	previous.Status = storedNodeStatusForNotification(storedStatus)
	livenessRecovered := offlineIncident != 0 && (status == "online" || status == "warning")
	if livenessRecovered {
		previous.Status = "offline"
	}
	current := notificationNodeSnapshot{ID: previous.ID, DisplayName: previous.DisplayName, PublicIPv4: previous.PublicIPv4}

	nextStatus := status
	if status == "online" && previous.Status == "warning" {
		nextStatus = "warning"
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET status = ?,
		    last_seen_at = CASE WHEN last_seen_at IS NULL OR last_seen_at <= ? THEN ? ELSE last_seen_at END,
		    updated_at = CASE WHEN updated_at <= ? THEN ? ELSE updated_at END
		WHERE id = ? AND disabled = 0
	`, nextStatus, seenAt, seenAt, nowUnix, nowUnix, nodeID); err != nil {
		return notificationStatusTransition{}, err
	}
	current.Status = storedNodeStatusForNotification(nextStatus)
	if livenessRecovered {
		if _, err := tx.ExecContext(ctx, `
			UPDATE alert_rule_states
			SET active = 0, last_seen_at = ?, updated_at = ?
			WHERE node_id = ? AND rule_id = 'node_offline'
		`, nowUnix, nowUnix, nodeID); err != nil {
			return notificationStatusTransition{}, err
		}
		// This transition is specifically the liveness recovery. The persisted
		// node may still be warning because of a separate resource incident.
		current.Status = "online"
	}
	if agentVersion != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO host_info (node_id, agent_version, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(node_id) DO UPDATE SET
				agent_version = excluded.agent_version,
				updated_at = excluded.updated_at
		`, nodeID, agentVersion, nowUnix); err != nil {
			return notificationStatusTransition{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return notificationStatusTransition{}, err
	}
	tx = nil
	return notificationStatusTransition{Previous: previous, Current: current}, nil
}

func (s *sqliteAgentDomain) RecordAgentPresenceOnlineTransition(ctx context.Context, nodeID string, ts time.Time) (notificationStatusTransition, error) {
	return s.recordAgentPresenceTransition(ctx, nodeID, ts, "online")
}

func (s *sqliteAgentDomain) RecordAgentPresenceOfflineTransition(ctx context.Context, nodeID string, ts time.Time) (notificationStatusTransition, error) {
	return s.recordAgentPresenceTransition(ctx, nodeID, ts, "offline")
}

func (s *sqliteAgentDomain) recordAgentPresenceTransition(ctx context.Context, nodeID string, ts time.Time, status string) (notificationStatusTransition, error) {
	return withAgentWriteResult(s.writes, ctx, nodeID, func(writeCtx context.Context) (notificationStatusTransition, error) {
		return s.recordAgentPresenceTransitionOnce(writeCtx, nodeID, ts, status)
	})
}

func (s *sqliteAgentDomain) StaleAgentOfflineNodeIDs(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.last_seen_at,
		       COALESCE((
		         SELECT MAX(ar.duration_sec)
		         FROM alert_rules ar
		         WHERE ar.notification_event_type = 'node_offline'
		           AND (
		             NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		             OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = n.id)
		           )
		       ), ?) AS offline_duration_sec
		FROM nodes n
		WHERE n.disabled = 0
		  AND n.status IN ('online', 'warning')
		ORDER BY n.display_order ASC, n.id ASC
	`, int64(nodeHeartbeatOfflineAfter/time.Second))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodeIDs := make([]string, 0)
	nowUnix := now.UTC().Unix()
	for rows.Next() {
		var nodeID string
		var lastSeenAt sql.NullInt64
		var offlineDurationSec sql.NullInt64
		if err := rows.Scan(&nodeID, &lastSeenAt, &offlineDurationSec); err != nil {
			return nil, err
		}
		offlineAfter := normalizeNodeOfflineAfter(nodeOfflineAfterFromSeconds(offlineDurationSec))
		cutoff := nowUnix - int64(offlineAfter/time.Second)
		if !lastSeenAt.Valid || lastSeenAt.Int64 <= cutoff {
			nodeIDs = append(nodeIDs, nodeID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nodeIDs, nil
}

func (s *sqliteAgentDomain) RecordStaleAgentOfflineTransition(ctx context.Context, nodeID string, now time.Time) (notificationStatusTransition, bool, error) {
	var transition notificationStatusTransition
	var changed bool
	err := s.writes.withAgentWrite(ctx, nodeID, func(ctx context.Context) error {
		var err error
		transition, changed, err = s.recordStaleAgentOfflineTransitionOnce(ctx, nodeID, now)
		return err
	})
	return transition, changed, err
}

func (s *sqliteAgentDomain) recordStaleAgentOfflineTransitionOnce(ctx context.Context, nodeID string, now time.Time) (notificationStatusTransition, bool, error) {
	var offlineDurationSec sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE((
		  SELECT MAX(ar.duration_sec)
		  FROM alert_rules ar
		  WHERE ar.notification_event_type = 'node_offline'
		    AND (
		      NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		      OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = ?)
		    )
		), ?) AS offline_duration_sec
	`, nodeID, int64(nodeHeartbeatOfflineAfter/time.Second)).Scan(&offlineDurationSec); err != nil {
		return notificationStatusTransition{}, false, err
	}
	offlineAfter := normalizeNodeOfflineAfter(nodeOfflineAfterFromSeconds(offlineDurationSec))
	cutoff := now.UTC().Add(-offlineAfter).Unix()
	nowUnix := now.UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return notificationStatusTransition{}, false, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	if err := lockAgentNodeWriteTx(ctx, tx, nodeID); err != nil {
		return notificationStatusTransition{}, false, err
	}

	var previous notificationNodeSnapshot
	var storedStatus string
	var lastSeenAt sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT id, display_name, status, last_seen_at, COALESCE(public_ipv4, '')
		FROM nodes
		WHERE id = ? AND disabled = 0
	`, nodeID).Scan(&previous.ID, &previous.DisplayName, &storedStatus, &lastSeenAt, &previous.PublicIPv4); err != nil {
		if err == sql.ErrNoRows {
			return notificationStatusTransition{}, false, errNodeNotFound
		}
		return notificationStatusTransition{}, false, err
	}
	previous.Status = storedNodeStatusForNotification(storedStatus)
	if storedStatus != "online" && storedStatus != "warning" {
		return notificationStatusTransition{}, false, nil
	}
	if lastSeenAt.Valid && lastSeenAt.Int64 > cutoff {
		return notificationStatusTransition{}, false, nil
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET status = 'offline', updated_at = ?
		WHERE id = ?
		  AND disabled = 0
		  AND status IN ('online', 'warning')
		  AND (last_seen_at IS NULL OR last_seen_at <= ?)
	`, nowUnix, nodeID, cutoff)
	if err != nil {
		return notificationStatusTransition{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return notificationStatusTransition{}, false, err
	}
	if changed == 0 {
		return notificationStatusTransition{}, false, nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO alert_rule_states (node_id, rule_id, active, first_seen_at, last_seen_at, updated_at)
		VALUES (?, 'node_offline', 1, ?, ?, ?)
		ON CONFLICT(node_id, rule_id) DO UPDATE SET
			active = 1,
			first_seen_at = CASE
				WHEN alert_rule_states.active = 1 AND alert_rule_states.first_seen_at IS NOT NULL THEN alert_rule_states.first_seen_at
				ELSE excluded.first_seen_at
			END,
			last_seen_at = excluded.last_seen_at,
			updated_at = excluded.updated_at
	`, nodeID, nowUnix, nowUnix, nowUnix); err != nil {
		return notificationStatusTransition{}, false, err
	}
	current := notificationNodeSnapshot{ID: previous.ID, DisplayName: previous.DisplayName, Status: "offline", PublicIPv4: previous.PublicIPv4}
	transition := notificationStatusTransition{Previous: previous, Current: current}
	if err := tx.Commit(); err != nil {
		return notificationStatusTransition{}, false, err
	}
	tx = nil
	return transition, true, nil
}

func (s *sqliteAgentDomain) recordAgentPresenceTransitionOnce(ctx context.Context, nodeID string, ts time.Time, status string) (notificationStatusTransition, error) {
	now := time.Now().UTC()
	nowUnix := now.Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	if err := lockAgentNodeWriteTx(ctx, tx, nodeID); err != nil {
		return notificationStatusTransition{}, err
	}

	var previous notificationNodeSnapshot
	var storedStatus string
	if err := tx.QueryRowContext(ctx, `
		SELECT id, display_name, status, COALESCE(public_ipv4, '')
		FROM nodes
		WHERE id = ? AND disabled = 0
	`, nodeID).Scan(&previous.ID, &previous.DisplayName, &storedStatus, &previous.PublicIPv4); err != nil {
		if err == sql.ErrNoRows {
			return notificationStatusTransition{}, errNodeNotFound
		}
		return notificationStatusTransition{}, err
	}
	previous.Status = storedNodeStatusForNotification(storedStatus)
	nextStatus := status
	if status == "online" && storedStatus == "warning" {
		nextStatus = "warning"
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET status = ?,
		    last_seen_at = CASE WHEN last_seen_at IS NULL OR last_seen_at <= ? THEN ? ELSE last_seen_at END,
		    updated_at = CASE WHEN updated_at <= ? THEN ? ELSE updated_at END
		WHERE id = ? AND disabled = 0
	`, nextStatus, nowUnix, nowUnix, nowUnix, nowUnix, nodeID); err != nil {
		return notificationStatusTransition{}, err
	}
	if status == "online" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE alert_rule_states
			SET active = 0, last_seen_at = ?, updated_at = ?
			WHERE node_id = ? AND rule_id = 'node_offline'
		`, nowUnix, nowUnix, nodeID); err != nil {
			return notificationStatusTransition{}, err
		}
	} else if status == "offline" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO alert_rule_states (node_id, rule_id, active, first_seen_at, last_seen_at, updated_at)
			VALUES (?, 'node_offline', 1, ?, ?, ?)
			ON CONFLICT(node_id, rule_id) DO UPDATE SET
				active = 1,
				first_seen_at = CASE
					WHEN alert_rule_states.active = 1 AND alert_rule_states.first_seen_at IS NOT NULL THEN alert_rule_states.first_seen_at
					ELSE excluded.first_seen_at
				END,
				last_seen_at = excluded.last_seen_at,
				updated_at = excluded.updated_at
		`, nodeID, nowUnix, nowUnix, nowUnix); err != nil {
			return notificationStatusTransition{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return notificationStatusTransition{}, err
	}
	tx = nil
	current := notificationNodeSnapshot{ID: previous.ID, DisplayName: previous.DisplayName, Status: storedNodeStatusForNotification(nextStatus), PublicIPv4: previous.PublicIPv4}
	return notificationStatusTransition{Previous: previous, Current: current}, nil
}

func storedNodeStatusForNotification(status string) string {
	switch strings.TrimSpace(status) {
	case "online", "warning", "offline":
		return strings.TrimSpace(status)
	default:
		return "offline"
	}
}
