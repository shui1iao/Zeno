package api

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type stateAlertRuleTransitionStore interface {
	RecordAgentStateAlertRuleTransition(ctx context.Context, nodeID string, ts time.Time, state AgentStateRequest) (notificationStatusTransition, error)
}

func (s *sqliteAgentDomain) RecordAgentStateAlertRuleTransition(ctx context.Context, nodeID string, ts time.Time, state AgentStateRequest) (notificationStatusTransition, error) {
	return withAgentWriteResult(s.writes, ctx, nodeID, func(writeCtx context.Context) (notificationStatusTransition, error) {
		return s.recordAgentStateAlertRuleTransitionOnce(writeCtx, nodeID, ts, state)
	})
}

func (s *sqliteAgentDomain) recordAgentStateAlertRuleTransitionOnce(ctx context.Context, nodeID string, ts time.Time, state AgentStateRequest) (notificationStatusTransition, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	if err := lockAgentNodeWriteTx(ctx, tx, nodeID); err != nil {
		return notificationStatusTransition{}, err
	}
	transition, err := recordAgentStateAlertRuleTransitionTx(ctx, tx, nodeID, ts, state)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	if err := tx.Commit(); err != nil {
		return notificationStatusTransition{}, err
	}
	tx = nil
	return transition, nil
}

func recordAgentStateAlertRuleTransitionTx(ctx context.Context, tx *sql.Tx, nodeID string, ts time.Time, state AgentStateRequest) (notificationStatusTransition, error) {
	rules, err := alertRulesForMetrics(ctx, tx, nodeID, map[string]bool{"cpu_percent": true, "memory_percent": true, "disk_percent": true})
	if err != nil {
		return notificationStatusTransition{}, err
	}
	values, err := stateAlertMetricValues(ctx, tx, nodeID, state, rules)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	_, hadRelevantActive, activeNames, recoveredNames, err := setAlertRuleStates(ctx, tx, nodeID, rules, values)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	status, err := aggregateAlertRuleStatus(ctx, tx, nodeID)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	transition, err := updateNodeStatusForAlertRules(ctx, tx, nodeID, ts, status, !hadRelevantActive)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	if transition.Current.Status == "warning" {
		transition.Detail = resourceAlertDetail(activeNames, false)
	} else if transition.Previous.Status == "warning" && transition.Current.Status == "online" {
		transition.Detail = resourceAlertDetail(recoveredNames, true)
	}
	return transition, nil
}

func alertRulesForMetrics(ctx context.Context, tx *sql.Tx, nodeID string, metrics map[string]bool) ([]AdminAlertRule, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, name, category, metric, comparator, threshold, threshold_unit, duration_sec,
		       enabled, notification_event_type, description, created_at, updated_at
		FROM alert_rules ar
		WHERE NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		   OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = ?)
		ORDER BY sort_order ASC, id ASC
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rules := make([]AdminAlertRule, 0)
	for rows.Next() {
		rule, err := scanAdminAlertRule(rows)
		if err != nil {
			return nil, err
		}
		if metrics[rule.Metric] {
			rules = append(rules, rule)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range rules {
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

func setAlertRuleStates(ctx context.Context, tx *sql.Tx, nodeID string, rules []AdminAlertRule, values map[string]*float64) (bool, bool, []string, []string, error) {
	anyActive := false
	hadRelevantActive := false
	activeNames := make([]string, 0)
	recoveredNames := make([]string, 0)
	now := time.Now().UTC().Unix()
	seenAt := now
	for _, rule := range rules {
		var previousActive int
		err := tx.QueryRowContext(ctx, `SELECT active FROM alert_rule_states WHERE node_id = ? AND rule_id = ?`, nodeID, rule.ID).Scan(&previousActive)
		if err != nil && err != sql.ErrNoRows {
			return false, false, nil, nil, err
		}
		if previousActive != 0 {
			hadRelevantActive = true
		}
		value := values[rule.Metric]
		active := rule.Enabled && value != nil && compareAlertRuleValue(*value, rule.Comparator, rule.Threshold)
		if active {
			anyActive = true
			activeNames = append(activeNames, resourceAlertRuleLabel(rule))
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO alert_rule_states (node_id, rule_id, active, first_seen_at, last_seen_at, last_value, updated_at)
				VALUES (?, ?, 1, ?, ?, ?, ?)
				ON CONFLICT(node_id, rule_id) DO UPDATE SET
					active = 1,
					first_seen_at = CASE
						WHEN alert_rule_states.active = 1 AND alert_rule_states.first_seen_at IS NOT NULL THEN alert_rule_states.first_seen_at
						ELSE excluded.first_seen_at
					END,
					last_seen_at = excluded.last_seen_at,
					last_value = excluded.last_value,
					updated_at = excluded.updated_at
			`, nodeID, rule.ID, seenAt, seenAt, *value, now); err != nil {
				return false, false, nil, nil, err
			}
			continue
		}
		if previousActive != 0 {
			recoveredNames = append(recoveredNames, resourceAlertRuleLabel(rule))
		}
		var lastValue any
		if value != nil {
			lastValue = *value
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE alert_rule_states
			SET active = 0, last_seen_at = ?, last_value = ?, updated_at = ?
			WHERE node_id = ? AND rule_id = ?
		`, seenAt, lastValue, now, nodeID, rule.ID); err != nil {
			return false, false, nil, nil, err
		}
	}
	return anyActive, hadRelevantActive, activeNames, recoveredNames, nil
}

func resourceAlertRuleLabel(rule AdminAlertRule) string {
	switch rule.Metric {
	case "cpu_percent":
		return "CPU"
	case "memory_percent":
		return "内存"
	case "disk_percent":
		return "硬盘"
	default:
		return strings.TrimSpace(rule.Name)
	}
}

func resourceAlertDetail(names []string, recovered bool) string {
	names = compactNonEmptyStrings(names)
	if len(names) == 0 {
		if recovered {
			return "状态恢复正常"
		}
		return "状态异常"
	}
	label := strings.Join(names, "、")
	if recovered {
		return label + "恢复正常"
	}
	return label + "持续占用过高"
}

func compactNonEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		result = append(result, trimmed)
	}
	return result
}

func aggregateAlertRuleStatus(ctx context.Context, tx *sql.Tx, nodeID string) (string, error) {
	var activeRules int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM alert_rule_states ars
		JOIN alert_rules ar ON ar.id = ars.rule_id
		WHERE ars.node_id = ?
		  AND ars.active = 1
		  AND ar.enabled = 1
		  AND ar.notification_event_type = 'probe_unhealthy'
		  AND (ar.category = 'resource' OR ar.duration_sec <= 0 OR (ars.first_seen_at IS NOT NULL AND ars.first_seen_at <= ? - ar.duration_sec))
		  AND (
		    NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		    OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = ?)
		  )
	`, nodeID, time.Now().UTC().Unix(), nodeID).Scan(&activeRules); err != nil {
		return "", err
	}
	if activeRules > 0 {
		return "warning", nil
	}
	return "online", nil
}

func updateNodeStatusForAlertRules(ctx context.Context, tx *sql.Tx, nodeID string, ts time.Time, status string, preserveExistingWarning bool) (notificationStatusTransition, error) {
	now := time.Now().UTC()
	nowUnix := now.Unix()
	seenAt := nowUnix
	var previous notificationNodeSnapshot
	var storedStatus string
	var lastSeenAt sql.NullInt64
	var offlineIncident int
	if err := tx.QueryRowContext(ctx, `
		SELECT n.id, n.display_name, n.status, n.last_seen_at, COALESCE(n.public_ipv4, ''),
		       CASE WHEN
		         EXISTS (
		           SELECT 1 FROM alert_rule_states ars
		           WHERE ars.node_id = n.id AND ars.rule_id = 'node_offline' AND ars.active = 1
		         )
		       THEN 1 ELSE 0 END
		FROM nodes n
		WHERE n.id = ? AND n.disabled = 0
	`, nodeID).Scan(&previous.ID, &previous.DisplayName, &storedStatus, &lastSeenAt, &previous.PublicIPv4, &offlineIncident); err != nil {
		if err == sql.ErrNoRows {
			return notificationStatusTransition{}, errNodeNotFound
		}
		return notificationStatusTransition{}, err
	}
	previous.Status = publicNodeStatus(storedStatus, lastSeenAt, now)
	if offlineIncident != 0 {
		previous.Status = "offline"
	}
	nextStatus := status
	if preserveExistingWarning && status == "online" && storedStatus == "warning" {
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
	if offlineIncident != 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE alert_rule_states
			SET active = 0, last_seen_at = ?, updated_at = ?
			WHERE node_id = ? AND rule_id = 'node_offline'
		`, nowUnix, nowUnix, nodeID); err != nil {
			return notificationStatusTransition{}, err
		}
	}
	current := notificationNodeSnapshot{ID: previous.ID, DisplayName: previous.DisplayName, Status: publicNodeStatus(nextStatus, sql.NullInt64{Int64: seenAt, Valid: true}, now), PublicIPv4: previous.PublicIPv4}
	if offlineIncident != 0 {
		// Report the liveness recovery independently from any resource warning
		// that remains persisted on the node.
		current.Status = "online"
	}
	return notificationStatusTransition{Previous: previous, Current: current}, nil
}

func stateAlertMetricValues(ctx context.Context, tx *sql.Tx, nodeID string, state AgentStateRequest, rules []AdminAlertRule) (map[string]*float64, error) {
	values := map[string]*float64{
		"cpu_percent":    floatValuePtr(state.CPUPercent),
		"memory_percent": percentValuePtr(state.MemoryUsedBytes, state.MemoryTotalBytes),
		"disk_percent":   percentValuePtr(state.DiskUsedBytes, state.DiskTotalBytes),
	}
	for _, rule := range rules {
		if rule.Category != "resource" || rule.DurationSec <= 0 {
			continue
		}
		average, err := averageStateAlertMetricValue(ctx, tx, nodeID, state.TS, rule)
		if err != nil {
			return nil, err
		}
		values[rule.Metric] = average
	}
	return values, nil
}

func averageStateAlertMetricValue(ctx context.Context, tx *sql.Tx, nodeID string, sampleTS int64, rule AdminAlertRule) (*float64, error) {
	if sampleTS <= 0 || rule.DurationSec <= 0 {
		return nil, nil
	}
	cutoff := sampleTS - int64(rule.DurationSec)
	var query string
	switch rule.Metric {
	case "cpu_percent":
		query = `SELECT AVG(cpu_percent), MIN(ts), COUNT(*) FROM state_samples WHERE node_id = ? AND ts >= ? AND ts <= ? AND cpu_percent IS NOT NULL`
	case "memory_percent":
		query = `SELECT AVG(memory_used_bytes * 100.0 / memory_total_bytes), MIN(ts), COUNT(*) FROM state_samples WHERE node_id = ? AND ts >= ? AND ts <= ? AND memory_total_bytes > 0 AND memory_used_bytes IS NOT NULL`
	case "disk_percent":
		query = `SELECT AVG(disk_used_bytes * 100.0 / disk_total_bytes), MIN(ts), COUNT(*) FROM state_samples WHERE node_id = ? AND ts >= ? AND ts <= ? AND disk_total_bytes > 0 AND disk_used_bytes IS NOT NULL`
	default:
		return nil, nil
	}
	var avg sql.NullFloat64
	var minTS sql.NullInt64
	var count int
	if err := tx.QueryRowContext(ctx, query, nodeID, cutoff, sampleTS).Scan(&avg, &minTS, &count); err != nil {
		return nil, err
	}
	if !avg.Valid || !minTS.Valid || count == 0 {
		return nil, nil
	}
	coverageSlack := int64(5)
	if rule.DurationSec < 5 {
		coverageSlack = 0
	}
	if minTS.Int64 > cutoff+coverageSlack {
		return nil, nil
	}
	value := avg.Float64
	return &value, nil
}

func compareAlertRuleValue(value float64, comparator string, threshold float64) bool {
	switch strings.TrimSpace(comparator) {
	case ">=":
		return value >= threshold
	case ">":
		return value > threshold
	case "<=":
		return value <= threshold
	case "<":
		return value < threshold
	case "=", "==":
		return value == threshold
	default:
		return false
	}
}

func floatValuePtr(value float64) *float64 {
	converted := value
	return &converted
}

func percentValuePtr(used, total int64) *float64 {
	if total <= 0 || used < 0 {
		return nil
	}
	converted := float64(used) / float64(total) * 100
	return &converted
}
