package api

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

var errNotificationCredentialUnavailable = errors.New("notification credential unavailable")

type notificationReconcileStore interface {
	NotificationReconcileSnapshot(ctx context.Context, now time.Time) (notificationReconcileSnapshot, error)
	ApplyNotificationStates(ctx context.Context, rows []notificationStateRow, logs []notificationLogEntry) error
}

type notificationStateKey struct {
	ChannelID string
	NodeID    string
	Kind      string
}

type notificationReconcileChannel struct {
	notificationDispatchChannel
	Enabled       bool
	CredentialErr error
}

type notificationReconcileNode struct {
	ID              string
	Name            string
	IPv4            string
	Status          string
	LastSeenAt      int64
	Disabled        bool
	ExpiryDate      string
	ExpiryPermanent bool
	BillingCycle    sql.NullString
}

type notificationReconcileSnapshot struct {
	Channels []notificationReconcileChannel
	Nodes    []notificationReconcileNode
	Rules    []AdminAlertRule
	// ResourceActive lists, per node, the labels of the resource rules that
	// currently make aggregateAlertRuleStatus report warning.
	ResourceActive map[string][]string
	States         map[notificationStateKey]notificationStateRow
}

type notificationLogEntry struct {
	TS        int64
	ChannelID string
	NodeID    string
	NodeName  string
	Kind      string
	From      string
	To        string
	Outcome   string
	Attempt   int
	Error     string
	Message   string
}

const (
	notificationOutcomeSent       = "sent"
	notificationOutcomeFailed     = "failed"
	notificationOutcomeDropped    = "dropped"
	notificationOutcomeBaseline   = "baseline"
	notificationOutcomeRebaseline = "rebaseline"
)

// rulesFor returns the enabled rules of one kind whose scope includes nodeID.
// An empty scope means every node.
func (snapshot notificationReconcileSnapshot) rulesFor(kind, nodeID string) []AdminAlertRule {
	rules := make([]AdminAlertRule, 0, 1)
	for _, rule := range snapshot.Rules {
		if !rule.Enabled || rule.NotificationEventType != kind {
			continue
		}
		if len(rule.ScopeNodeIDs) > 0 && !containsString(rule.ScopeNodeIDs, nodeID) {
			continue
		}
		rules = append(rules, rule)
	}
	return rules
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// notificationHoldSeconds is the recovery stability window: the largest
// duration of the enabled rules for this kind and node, as before.
func notificationHoldSeconds(rules []AdminAlertRule) int64 {
	var hold int64
	for _, rule := range rules {
		if int64(rule.DurationSec) > hold {
			hold = int64(rule.DurationSec)
		}
	}
	return hold
}

func (s *sqliteNotificationDomain) NotificationReconcileSnapshot(ctx context.Context, now time.Time) (notificationReconcileSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return notificationReconcileSnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var snapshot notificationReconcileSnapshot
	if snapshot.Channels, err = s.notificationReconcileChannels(ctx, tx); err != nil {
		return notificationReconcileSnapshot{}, err
	}
	if snapshot.Nodes, err = notificationReconcileNodes(ctx, tx); err != nil {
		return notificationReconcileSnapshot{}, err
	}
	if snapshot.Rules, err = notificationReconcileRules(ctx, tx); err != nil {
		return notificationReconcileSnapshot{}, err
	}
	if snapshot.ResourceActive, err = activeResourceAlertLabels(ctx, tx, now); err != nil {
		return notificationReconcileSnapshot{}, err
	}
	if snapshot.States, err = notificationStateRows(ctx, tx); err != nil {
		return notificationReconcileSnapshot{}, err
	}
	return snapshot, nil
}

func (s *sqliteNotificationDomain) notificationReconcileChannels(ctx context.Context, tx *sql.Tx) ([]notificationReconcileChannel, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, name, destination, credential, enabled, delivery_version, destination_fingerprint
		FROM notification_channels
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels := make([]notificationReconcileChannel, 0)
	for rows.Next() {
		var channel notificationReconcileChannel
		var storedCredential string
		var enabled int
		if err := rows.Scan(&channel.ID, &channel.Name, &channel.Destination, &storedCredential, &enabled,
			&channel.DeliveryVersion, &channel.DestinationFingerprint); err != nil {
			return nil, err
		}
		channel.Type = "telegram"
		channel.Enabled = enabled != 0 && strings.TrimSpace(channel.Destination) != "" && strings.TrimSpace(storedCredential) != ""
		if channel.Enabled {
			credential, err := s.decryptNotificationCredentialFromStorage(channel.ID, "telegram", storedCredential)
			if err != nil || strings.TrimSpace(credential) == "" {
				channel.CredentialErr = errNotificationCredentialUnavailable
			} else {
				channel.Credential = credential
			}
		}
		channels = append(channels, channel)
	}
	return channels, rows.Err()
}

func notificationReconcileNodes(ctx context.Context, tx *sql.Tx) ([]notificationReconcileNode, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, display_name, status, last_seen_at, COALESCE(public_ipv4, ''), disabled,
		       COALESCE(expiry_date, ''), expiry_permanent, billing_cycle
		FROM nodes
		ORDER BY display_order ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := make([]notificationReconcileNode, 0)
	for rows.Next() {
		var node notificationReconcileNode
		var lastSeenAt sql.NullInt64
		var disabled, permanent int
		if err := rows.Scan(&node.ID, &node.Name, &node.Status, &lastSeenAt, &node.IPv4, &disabled,
			&node.ExpiryDate, &permanent, &node.BillingCycle); err != nil {
			return nil, err
		}
		if lastSeenAt.Valid {
			node.LastSeenAt = lastSeenAt.Int64
		}
		node.Disabled = disabled != 0
		node.ExpiryPermanent = permanent != 0
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func notificationReconcileRules(ctx context.Context, tx *sql.Tx) ([]AdminAlertRule, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, name, category, metric, comparator, threshold, threshold_unit, duration_sec,
		       enabled, notification_event_type, description, created_at, updated_at
		FROM alert_rules
		ORDER BY sort_order ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	rules := make([]AdminAlertRule, 0)
	for rows.Next() {
		rule, err := scanAdminAlertRule(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	scopes, err := alertRuleScopes(ctx, tx)
	if err != nil {
		return nil, err
	}
	for index := range rules {
		rules[index].ScopeNodeIDs = scopes[rules[index].ID]
		if rules[index].Metric != "expiry_days" {
			continue
		}
		days, err := loadAlertRuleRenewalDays(ctx, tx, rules[index])
		if err != nil {
			return nil, err
		}
		rules[index].RenewalDays = days
	}
	return rules, nil
}

func alertRuleScopes(ctx context.Context, tx *sql.Tx) (map[string][]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT rule_id, node_id FROM alert_rule_node_scopes ORDER BY rule_id, node_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scopes := map[string][]string{}
	for rows.Next() {
		var ruleID, nodeID string
		if err := rows.Scan(&ruleID, &nodeID); err != nil {
			return nil, err
		}
		scopes[ruleID] = append(scopes[ruleID], nodeID)
	}
	return scopes, rows.Err()
}

// activeResourceAlertLabels applies exactly the conditions of
// aggregateAlertRuleStatus to every node at once and returns the labels of the
// rules that make a node warning, in rule order.
func activeResourceAlertLabels(ctx context.Context, tx *sql.Tx, now time.Time) (map[string][]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT ars.node_id, ar.name, ar.metric
		FROM alert_rule_states ars
		JOIN alert_rules ar ON ar.id = ars.rule_id
		WHERE ars.active = 1
		  AND ar.enabled = 1
		  AND ar.notification_event_type = 'probe_unhealthy'
		  AND (ar.category = 'resource' OR ar.duration_sec <= 0 OR (ars.first_seen_at IS NOT NULL AND ars.first_seen_at <= ? - ar.duration_sec))
		  AND (
		    NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		    OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = ars.node_id)
		  )
		ORDER BY ars.node_id ASC, ar.sort_order ASC, ar.id ASC
	`, now.UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	active := map[string][]string{}
	for rows.Next() {
		var nodeID string
		var rule AdminAlertRule
		if err := rows.Scan(&nodeID, &rule.Name, &rule.Metric); err != nil {
			return nil, err
		}
		active[nodeID] = append(active[nodeID], resourceAlertRuleLabel(rule))
	}
	return active, rows.Err()
}

func notificationStateRows(ctx context.Context, tx *sql.Tx) (map[notificationStateKey]notificationStateRow, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT channel_id, node_id, kind, notified, notified_detail, incident_from,
		       pending_target, pending_since, pending_attempts, last_error,
		       channel_version, destination_fingerprint, updated_at
		FROM notification_states
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := map[notificationStateKey]notificationStateRow{}
	for rows.Next() {
		var row notificationStateRow
		if err := rows.Scan(&row.ChannelID, &row.NodeID, &row.Kind, &row.Notified, &row.NotifiedDetail,
			&row.IncidentFrom, &row.PendingTarget, &row.PendingSince, &row.PendingAttempts, &row.LastError,
			&row.ChannelVersion, &row.DestinationFingerprint, &row.UpdatedAt); err != nil {
			return nil, err
		}
		states[notificationStateKey{ChannelID: row.ChannelID, NodeID: row.NodeID, Kind: row.Kind}] = row
	}
	return states, rows.Err()
}

// ApplyNotificationStates persists state rows and log entries in one short
// write transaction. No transaction is ever held while a message is sent.
// Rows for a node or channel deleted since the snapshot are skipped.
func (s *sqliteNotificationDomain) ApplyNotificationStates(ctx context.Context, rows []notificationStateRow, logs []notificationLogEntry) error {
	if len(rows) == 0 && len(logs) == 0 {
		return nil
	}
	return s.writes.withAgentWrite(ctx, notificationReconcileWriteKey, func(writeCtx context.Context) error {
		tx, err := s.db.BeginTx(writeCtx, nil)
		if err != nil {
			return err
		}
		defer func() { rollbackUnlessCommitted(tx) }()
		for _, row := range rows {
			if err := upsertNotificationStateTx(writeCtx, tx, row); err != nil {
				return err
			}
		}
		for _, entry := range logs {
			if err := insertNotificationLogTx(writeCtx, tx, entry); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		tx = nil
		return nil
	})
}

func upsertNotificationStateTx(ctx context.Context, tx *sql.Tx, row notificationStateRow) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO notification_states (
			channel_id, node_id, kind, notified, notified_detail, incident_from,
			pending_target, pending_since, pending_attempts, last_error,
			channel_version, destination_fingerprint, updated_at
		)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE EXISTS (SELECT 1 FROM nodes WHERE id = ?)
		  AND EXISTS (SELECT 1 FROM notification_channels WHERE id = ?)
		ON CONFLICT(channel_id, node_id, kind) DO UPDATE SET
			notified = excluded.notified,
			notified_detail = excluded.notified_detail,
			incident_from = excluded.incident_from,
			pending_target = excluded.pending_target,
			pending_since = excluded.pending_since,
			pending_attempts = excluded.pending_attempts,
			last_error = excluded.last_error,
			channel_version = excluded.channel_version,
			destination_fingerprint = excluded.destination_fingerprint,
			updated_at = excluded.updated_at
	`, row.ChannelID, row.NodeID, row.Kind, row.Notified, row.NotifiedDetail, row.IncidentFrom,
		row.PendingTarget, row.PendingSince, row.PendingAttempts, row.LastError,
		row.ChannelVersion, row.DestinationFingerprint, row.UpdatedAt, row.NodeID, row.ChannelID)
	return err
}

func insertNotificationLogTx(ctx context.Context, tx *sql.Tx, entry notificationLogEntry) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO notification_log (
			ts, channel_id, node_id, node_name, kind, from_state, to_state,
			outcome, attempt, error, message
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, entry.TS, entry.ChannelID, entry.NodeID, entry.NodeName, entry.Kind, entry.From, entry.To,
		entry.Outcome, entry.Attempt, entry.Error, entry.Message)
	return err
}
