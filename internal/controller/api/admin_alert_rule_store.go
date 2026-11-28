package api

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"
)

var defaultAdminAlertRules = []AdminAlertRule{
	{
		ID:                    "cpu_high",
		Name:                  "CPU 使用率",
		Category:              "resource",
		Metric:                "cpu_percent",
		Comparator:            ">=",
		Threshold:             90,
		ThresholdUnit:         "%",
		DurationSec:           300,
		Enabled:               true,
		NotificationEventType: "probe_unhealthy",
	},
	{
		ID:                    "memory_high",
		Name:                  "内存使用率",
		Category:              "resource",
		Metric:                "memory_percent",
		Comparator:            ">=",
		Threshold:             90,
		ThresholdUnit:         "%",
		DurationSec:           300,
		Enabled:               true,
		NotificationEventType: "probe_unhealthy",
	},
	{
		ID:                    "disk_high",
		Name:                  "磁盘使用率",
		Category:              "resource",
		Metric:                "disk_percent",
		Comparator:            ">=",
		Threshold:             90,
		ThresholdUnit:         "%",
		DurationSec:           300,
		Enabled:               true,
		NotificationEventType: "probe_unhealthy",
	},
	{
		ID:                    "node_offline",
		Name:                  "离线通知",
		Category:              "liveness",
		Metric:                "heartbeat_age_sec",
		Comparator:            ">=",
		Threshold:             60,
		ThresholdUnit:         "s",
		DurationSec:           60,
		Enabled:               true,
		NotificationEventType: "node_offline",
	},
	{
		ID:                    "renewal_due",
		Name:                  "续费提醒",
		Category:              "billing",
		Metric:                "expiry_days",
		Comparator:            "<=",
		Threshold:             3,
		RenewalDays:           []int{3},
		ThresholdUnit:         "d",
		DurationSec:           0,
		Enabled:               false,
		NotificationEventType: "renewal_due",
	},
}

type sqliteAdminAlertRules struct {
	db     *sql.DB
	readDB *sql.DB
}

func (s *sqliteAdminAlertRules) queryReadDB() *sql.DB {
	return preferredReadDB(s.readDB, s.db)
}

var retiredAdminAlertRuleIDs = []string{"probe_latency_high", "probe_loss_high", "node_recovered"}

var retiredAdminNotificationEventTypes = []string{"node_online"}

const renewalNoticeCalendarMonthThreshold = 30

var allowedRenewalNoticeDays = map[int]bool{1: true, 3: true, 7: true, 15: true, renewalNoticeCalendarMonthThreshold: true}

func (s *sqliteAdminAlertRules) ensureDefaultAlertRules(ctx context.Context) error {
	now := time.Now().UTC().Unix()
	for sortOrder, rule := range defaultAdminAlertRules {
		enabled := 0
		if rule.Enabled {
			enabled = 1
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT OR IGNORE INTO alert_rules (
				id, name, category, metric, comparator, threshold, threshold_unit,
				duration_sec, enabled, notification_event_type, description, sort_order,
				created_at, updated_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, rule.ID, rule.Name, rule.Category, rule.Metric, rule.Comparator, rule.Threshold, rule.ThresholdUnit, rule.DurationSec, enabled, rule.NotificationEventType, rule.Description, sortOrder, now, now); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE alert_rules
			SET name = ?, description = '', sort_order = ?
			WHERE id = ?
		`, rule.Name, sortOrder, rule.ID); err != nil {
			return err
		}
	}
	if err := s.migrateRemovedSameDayRenewalThreshold(ctx); err != nil {
		return err
	}
	if err := s.ensureRenewalNoticeDays(ctx); err != nil {
		return err
	}
	if err := s.migrateDefaultAlertRuleDurations(ctx); err != nil {
		return err
	}
	if err := s.migrateNodeOfflineDefaultToOneMinute(ctx); err != nil {
		return err
	}
	if err := s.migrateResourceAlertRuleDurationToFiveMinutes(ctx); err != nil {
		return err
	}
	if err := s.migrateNotificationTypesToAlertRules(ctx); err != nil {
		return err
	}
	return nil
}

func (s *sqliteAdminAlertRules) ensureRenewalNoticeDays(ctx context.Context) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_rule_renewal_days WHERE rule_id = 'renewal_due'`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	var threshold float64
	if err := s.db.QueryRowContext(ctx, `SELECT threshold FROM alert_rules WHERE id = 'renewal_due'`).Scan(&threshold); err != nil {
		return err
	}
	days := int(threshold)
	if threshold != float64(days) || !allowedRenewalNoticeDays[days] {
		days = 3
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO alert_rule_renewal_days (rule_id, days, created_at)
		VALUES ('renewal_due', ?, ?)
	`, days, time.Now().UTC().Unix())
	return err
}

func (s *sqliteAdminAlertRules) migrateRemovedSameDayRenewalThreshold(ctx context.Context) error {
	now := time.Now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	result, err := tx.ExecContext(ctx, `
		UPDATE alert_rules
		SET threshold = 1, updated_at = ?
		WHERE id = 'renewal_due' AND threshold = 0
	`, now)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM alert_rule_renewal_days WHERE rule_id = 'renewal_due'`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO alert_rule_renewal_days (rule_id, days, created_at)
			VALUES ('renewal_due', 1, ?)
		`, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *sqliteAdminAlertRules) migrateDefaultAlertRuleDurations(ctx context.Context) error {
	now := time.Now().UTC().Unix()
	const migrationKey = "alert_default_durations_v2_migrated"
	var marker string
	migrated := true
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, migrationKey).Scan(&marker); err != nil {
		if err != sql.ErrNoRows {
			return err
		}
		migrated = false
	}
	if migrated {
		return nil
	}
	// Only untouched legacy defaults are migrated. Resource-rule defaults are
	// handled directly by migrateResourceAlertRuleDurationToFiveMinutes below;
	// avoiding an intermediate 60s value prevents same-second marker ambiguity.
	if _, err := s.db.ExecContext(ctx, `
		UPDATE alert_rules
		SET threshold = CASE WHEN threshold = 180 THEN 30 ELSE threshold END,
		    duration_sec = CASE WHEN duration_sec = 180 THEN 30 ELSE duration_sec END,
		    updated_at = ?
		WHERE id = 'node_offline'
		  AND updated_at = created_at
		  AND (threshold = 180 OR duration_sec = 180)
	`, now); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at)
			VALUES (?, '1', ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, migrationKey, now); err != nil {
		return err
	}
	return nil
}

func (s *sqliteAdminAlertRules) migrateNodeOfflineDefaultToOneMinute(ctx context.Context) error {
	const migrationKey = "node_offline_default_60s_migrated"
	now := time.Now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	var marker string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, migrationKey).Scan(&marker); err != nil {
		if err != sql.ErrNoRows {
			return err
		}
	} else {
		return nil
	}
	// Only the two released forms of the untouched 30s default are eligible:
	// a row that was created as 30/30, or a legacy 180/180 row changed by the
	// v2 migration. A later user edit has a different updated_at and is kept.
	if _, err := tx.ExecContext(ctx, `
		UPDATE alert_rules
		SET threshold = 60, duration_sec = 60, updated_at = ?
		WHERE id = 'node_offline'
		  AND threshold = 30
		  AND duration_sec = 30
		  AND (
		    updated_at = created_at
		    OR updated_at = (
		      SELECT updated_at FROM settings
		      WHERE key = 'alert_default_durations_v2_migrated'
		    )
		  )
	`, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, '1', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, migrationKey, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *sqliteAdminAlertRules) migrateNotificationTypesToAlertRules(ctx context.Context) error {
	const migrationKey = "notification_types_alert_rules_migrated"
	var marker string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, migrationKey).Scan(&marker); err != nil {
		if err != sql.ErrNoRows {
			return err
		}
	} else {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT event_type, enabled, updated_at FROM notification_types`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type legacyNotificationType struct {
		eventType string
		enabled   int
		updatedAt int64
	}
	legacyTypes := []legacyNotificationType{}
	for rows.Next() {
		var legacy legacyNotificationType
		if err := rows.Scan(&legacy.eventType, &legacy.enabled, &legacy.updatedAt); err != nil {
			return err
		}
		legacy.eventType = strings.TrimSpace(legacy.eventType)
		if legacy.eventType != "" {
			legacyTypes = append(legacyTypes, legacy)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Unix()
	for _, legacy := range legacyTypes {
		if _, ok := adminNotificationTypeLabel(legacy.eventType); !ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE alert_rules
			SET enabled = ?, updated_at = ?
			WHERE notification_event_type = ?
			  AND (
			    updated_at = created_at
			    OR updated_at < ?
			    OR updated_at IN (
			      SELECT updated_at FROM settings
			      WHERE key IN ('alert_default_durations_v2_migrated', 'resource_alert_duration_5m_migrated')
			    )
			  )
		`, sqliteBoolInt(legacy.enabled != 0), now, legacy.eventType, legacy.updatedAt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, '1', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, migrationKey, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteAdminAlertRules) migrateResourceAlertRuleDurationToFiveMinutes(ctx context.Context) error {
	now := time.Now().UTC().Unix()
	const migrationKey = "resource_alert_duration_5m_migrated"
	var marker string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, migrationKey).Scan(&marker); err != nil {
		if err != sql.ErrNoRows {
			return err
		}
	} else {
		return nil
	}
	for _, ruleID := range []string{"cpu_high", "memory_high", "disk_high"} {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE alert_rules
			SET duration_sec = 300, updated_at = ?
			WHERE id = ?
			  AND duration_sec IN (30, 60, 600)
			  AND (
			    updated_at = created_at
			    OR (
			      duration_sec = 60
			      AND updated_at IN (
			        SELECT updated_at FROM settings WHERE key = 'alert_default_durations_v2_migrated'
			      )
			    )
			  )
		`, now, ruleID); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, '1', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, migrationKey, now); err != nil {
		return err
	}
	return nil
}

func (s *sqliteAdminAlertRules) pruneRetiredNotificationConfig(ctx context.Context) error {
	for _, ruleID := range retiredAdminAlertRuleIDs {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM alert_rule_node_scopes WHERE rule_id = ?`, ruleID); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM alert_rule_states WHERE rule_id = ?`, ruleID); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = ?`, ruleID); err != nil {
			return err
		}
	}
	for _, eventType := range retiredAdminNotificationEventTypes {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM notification_types WHERE event_type = ?`, eventType); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `UPDATE alert_rules SET description = '' WHERE description <> ''`)
	return err
}

func (s *sqliteAdminAlertRules) AdminAlertRules(ctx context.Context) ([]AdminAlertRule, error) {
	readDB := s.queryReadDB()
	rows, err := readDB.QueryContext(ctx, `
		SELECT id, name, category, metric, comparator, threshold, threshold_unit, duration_sec,
		       enabled, notification_event_type, description, created_at, updated_at
		FROM alert_rules
		ORDER BY sort_order ASC, id ASC
	`)
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
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.attachAlertRuleMetadataWithDB(ctx, readDB, rules)
}

func (s *sqliteAdminAlertRules) UpdateAdminAlertRule(ctx context.Context, ruleID string, update AdminAlertRuleUpdateRequest) (AdminAlertRule, error) {
	ruleID = strings.TrimSpace(ruleID)
	if ruleID == "" {
		return AdminAlertRule{}, errAlertRuleNotFound
	}
	if err := update.normalize(); err != nil {
		return AdminAlertRule{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminAlertRule{}, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()

	var metric string
	if err := tx.QueryRowContext(ctx, `SELECT metric FROM alert_rules WHERE id = ?`, ruleID).Scan(&metric); err != nil {
		if err == sql.ErrNoRows {
			return AdminAlertRule{}, errAlertRuleNotFound
		}
		return AdminAlertRule{}, err
	}
	renewalDays, replaceRenewalDays, err := normalizedAlertRuleRenewalUpdate(update, metric)
	if err != nil {
		return AdminAlertRule{}, err
	}
	patch := newSQLPatch(4)
	patch.addBoolInt("enabled", update.Enabled)
	if update.RenewalDays != nil {
		patch.set("threshold", renewalDays[len(renewalDays)-1])
	} else if update.Threshold != nil {
		patch.set("threshold", *update.Threshold)
	}
	patch.addInt("duration_sec", update.DurationSec)
	now := time.Now().UTC().Unix()
	if !patch.empty() {
		patch.set("updated_at", now)
		statement, args := patch.updateStatement("alert_rules", "id = ?", ruleID)
		if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
			return AdminAlertRule{}, err
		}
	} else if update.ScopeNodeIDs != nil || update.RenewalDays != nil {
		// Scope-only edits still bump updated_at so clients can tell the rule
		// changed even though no column on the rule row differs.
		if _, err := tx.ExecContext(ctx, `UPDATE alert_rules SET updated_at = ? WHERE id = ?`, now, ruleID); err != nil {
			return AdminAlertRule{}, err
		}
	}
	if update.ScopeNodeIDs != nil {
		if err := replaceAlertRuleNodeScopes(ctx, tx, ruleID, *update.ScopeNodeIDs, now); err != nil {
			return AdminAlertRule{}, err
		}
	}
	if replaceRenewalDays {
		if err := replaceAlertRuleRenewalDays(ctx, tx, ruleID, renewalDays, now); err != nil {
			return AdminAlertRule{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AdminAlertRule{}, err
	}
	tx = nil
	return s.adminAlertRuleByID(ctx, ruleID)
}

func normalizedAlertRuleRenewalUpdate(update AdminAlertRuleUpdateRequest, metric string) ([]int, bool, error) {
	if update.RenewalDays != nil {
		if metric != "expiry_days" {
			return nil, false, errInvalidAdminAlertRuleUpdate
		}
		days := append([]int(nil), (*update.RenewalDays)...)
		sort.Ints(days)
		return days, true, nil
	}
	if update.Threshold == nil || metric != "expiry_days" {
		return nil, false, nil
	}
	threshold := *update.Threshold
	days := int(threshold)
	if threshold != float64(days) || !allowedRenewalNoticeDays[days] {
		return nil, false, errInvalidAdminAlertRuleUpdate
	}
	return []int{days}, true, nil
}

func (s *sqliteAdminAlertRules) adminAlertRuleByID(ctx context.Context, ruleID string) (AdminAlertRule, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, category, metric, comparator, threshold, threshold_unit, duration_sec,
		       enabled, notification_event_type, description, created_at, updated_at
		FROM alert_rules
		WHERE id = ?
	`, ruleID)
	rule, err := scanAdminAlertRule(row)
	if err != nil {
		return AdminAlertRule{}, err
	}
	rules, err := s.attachAlertRuleMetadata(ctx, []AdminAlertRule{rule})
	if err != nil {
		return AdminAlertRule{}, err
	}
	if len(rules) == 0 {
		return AdminAlertRule{}, errAlertRuleNotFound
	}
	return rules[0], nil
}

func (s *sqliteAdminAlertRules) attachAlertRuleMetadata(ctx context.Context, rules []AdminAlertRule) ([]AdminAlertRule, error) {
	return s.attachAlertRuleMetadataWithDB(ctx, s.db, rules)
}

func (s *sqliteAdminAlertRules) attachAlertRuleMetadataWithDB(ctx context.Context, targetDB *sql.DB, rules []AdminAlertRule) ([]AdminAlertRule, error) {
	rules, err := s.attachAlertRuleScopesWithDB(ctx, targetDB, rules)
	if err != nil {
		return nil, err
	}
	for index := range rules {
		if rules[index].Metric != "expiry_days" {
			continue
		}
		days, err := loadAlertRuleRenewalDays(ctx, targetDB, rules[index])
		if err != nil {
			return nil, err
		}
		rules[index].RenewalDays = days
	}
	return rules, nil
}

func (s *sqliteAdminAlertRules) attachAlertRuleScopesWithDB(ctx context.Context, targetDB *sql.DB, rules []AdminAlertRule) ([]AdminAlertRule, error) {
	if len(rules) == 0 {
		return rules, nil
	}
	for index := range rules {
		scopeNodeIDs, err := alertRuleScopeNodeIDsFromDB(ctx, targetDB, rules[index].ID)
		if err != nil {
			return nil, err
		}
		rules[index].ScopeNodeIDs = scopeNodeIDs
	}
	return rules, nil
}

func alertRuleScopeNodeIDsFromDB(ctx context.Context, targetDB *sql.DB, ruleID string) ([]string, error) {
	rows, err := targetDB.QueryContext(ctx, `
		SELECT scope.node_id
		FROM alert_rule_node_scopes scope
		JOIN nodes n ON n.id = scope.node_id
		WHERE scope.rule_id = ?
		  AND NOT EXISTS (
			SELECT 1 FROM admin_deletion_jobs deletion
			WHERE deletion.entity_kind = 'node'
			  AND deletion.entity_id = n.id
			  AND deletion.state IN ('pending', 'running')
		  )
		ORDER BY COALESCE(n.display_order, 0) ASC, scope.node_id ASC
	`, ruleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodeIDs := make([]string, 0)
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			return nil, err
		}
		nodeIDs = append(nodeIDs, nodeID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nodeIDs, nil
}

func replaceAlertRuleNodeScopes(ctx context.Context, tx *sql.Tx, ruleID string, nodeIDs []string, now int64) error {
	for _, nodeID := range nodeIDs {
		var exists int
		if err := tx.QueryRowContext(ctx, activeAdminNodeExistsSQL, nodeID).Scan(&exists); err != nil {
			if err == sql.ErrNoRows {
				return errInvalidAdminAlertRuleUpdate
			}
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM alert_rule_node_scopes WHERE rule_id = ?`, ruleID); err != nil {
		return err
	}
	for _, nodeID := range nodeIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO alert_rule_node_scopes (rule_id, node_id, created_at)
			VALUES (?, ?, ?)
		`, ruleID, nodeID, now); err != nil {
			return err
		}
	}
	return nil
}

func replaceAlertRuleRenewalDays(ctx context.Context, tx *sql.Tx, ruleID string, days []int, now int64) error {
	if len(days) == 0 {
		return errInvalidAdminAlertRuleUpdate
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM alert_rule_renewal_days WHERE rule_id = ?`, ruleID); err != nil {
		return err
	}
	for _, day := range days {
		if !allowedRenewalNoticeDays[day] {
			return errInvalidAdminAlertRuleUpdate
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO alert_rule_renewal_days (rule_id, days, created_at)
			VALUES (?, ?, ?)
		`, ruleID, day, now); err != nil {
			return err
		}
	}
	return nil
}

type alertRuleQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadAlertRuleRenewalDays(ctx context.Context, queryer alertRuleQueryer, rule AdminAlertRule) ([]int, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT days
		FROM alert_rule_renewal_days
		WHERE rule_id = ?
		ORDER BY days ASC
	`, rule.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := make([]int, 0)
	for rows.Next() {
		var day int
		if err := rows.Scan(&day); err != nil {
			return nil, err
		}
		if allowedRenewalNoticeDays[day] {
			days = append(days, day)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(days) > 0 {
		return days, nil
	}
	return effectiveRenewalNoticeDays(rule), nil
}

func effectiveRenewalNoticeDays(rule AdminAlertRule) []int {
	if len(rule.RenewalDays) > 0 {
		return rule.RenewalDays
	}
	days := int(rule.Threshold)
	if rule.Threshold == float64(days) && allowedRenewalNoticeDays[days] {
		return []int{days}
	}
	return nil
}

type adminAlertRuleScanner interface {
	Scan(dest ...any) error
}

func scanAdminAlertRule(scanner adminAlertRuleScanner) (AdminAlertRule, error) {
	var rule AdminAlertRule
	var enabled int
	var createdAt, updatedAt sql.NullInt64
	if err := scanner.Scan(
		&rule.ID,
		&rule.Name,
		&rule.Category,
		&rule.Metric,
		&rule.Comparator,
		&rule.Threshold,
		&rule.ThresholdUnit,
		&rule.DurationSec,
		&enabled,
		&rule.NotificationEventType,
		&rule.Description,
		&createdAt,
		&updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return AdminAlertRule{}, errAlertRuleNotFound
		}
		return AdminAlertRule{}, err
	}
	rule.Enabled = enabled != 0
	if label, ok := adminNotificationTypeLabel(rule.NotificationEventType); ok {
		rule.NotificationLabel = label
	}
	rule.CreatedAt = unixStringOr(createdAt, time.Now().UTC())
	rule.UpdatedAt = unixStringOr(updatedAt, time.Now().UTC())
	rule.ScopeNodeIDs = []string{}
	return rule, nil
}
