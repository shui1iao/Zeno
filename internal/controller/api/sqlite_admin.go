package api

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func (s *sqliteAdminDomain) queryReadDB() *sql.DB {
	if s.readDB != nil {
		return s.readDB
	}
	return s.db
}

func (s *sqliteAdminDomain) AdminNodes(ctx context.Context) ([]AdminNode, error) {
	rows, err := s.queryReadDB().QueryContext(ctx, `
		SELECT n.id, n.display_name, n.status, n.country_code, n.region, n.disabled,
		       n.home_probe_target_id, n.billing_mode, n.monthly_reset_day, n.expiry_date, n.expiry_permanent, n.billing_cycle, n.renewal_amount, n.renewal_currency, n.display_order, n.public_ipv4, n.public_ipv6,
		       n.monthly_quota_bytes, n.last_seen_at, n.created_at, n.updated_at,
		       COALESCE((
		         SELECT MAX(ar.duration_sec)
		         FROM alert_rules ar
		         WHERE ar.notification_event_type = 'node_offline'
		           AND (
		             NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		             OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = n.id)
		           )
		       ), ?) AS offline_duration_sec,
		       h.hostname, h.os_name, h.os_version, h.kernel, h.arch, h.virtualization,
		       h.cpu_model, h.cpu_cores, h.memory_total_bytes, h.disk_total_bytes,
		       h.boot_time, h.agent_version
		FROM nodes n
		LEFT JOIN host_info h ON h.node_id = n.id
		WHERE NOT EXISTS (
			SELECT 1 FROM admin_deletion_jobs deletion
			WHERE deletion.entity_kind = 'node'
			  AND deletion.entity_id = n.id
			  AND deletion.state IN ('pending', 'running')
		)
		ORDER BY n.display_order ASC, n.id ASC
	`, int64(nodeHeartbeatOfflineAfter/time.Second))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []AdminNode
	now := time.Now()
	for rows.Next() {
		var node AdminNode
		var status string
		var countryCode, region, homeProbeTargetID, billingMode, expiryDate, billingCycle, renewalCurrency, publicIPv4, publicIPv6 sql.NullString
		var renewalAmount sql.NullFloat64
		var disabled int
		var expiryPermanent int
		var monthlyResetDay int
		var displayOrder int
		var quota, lastSeenAt, createdAt, updatedAt, offlineDurationSec sql.NullInt64
		var hostname, osName, osVersion, kernel, arch, virtualization, cpuModel, agentVersion sql.NullString
		var cpuCores, memoryTotal, diskTotal, bootTime sql.NullInt64
		if err := rows.Scan(
			&node.ID, &node.DisplayName, &status, &countryCode, &region, &disabled,
			&homeProbeTargetID, &billingMode, &monthlyResetDay, &expiryDate, &expiryPermanent, &billingCycle, &renewalAmount, &renewalCurrency, &displayOrder, &publicIPv4, &publicIPv6,
			&quota, &lastSeenAt, &createdAt, &updatedAt, &offlineDurationSec,
			&hostname, &osName, &osVersion, &kernel, &arch, &virtualization,
			&cpuModel, &cpuCores, &memoryTotal, &diskTotal,
			&bootTime, &agentVersion,
		); err != nil {
			return nil, err
		}
		node.Disabled = disabled != 0
		node.Status = publicNodeStatusAfter(status, lastSeenAt, now, nodeOfflineAfterFromSeconds(offlineDurationSec))
		if node.Disabled {
			node.Status = "disabled"
		}
		node.CountryCode = nullStringOr(countryCode, "")
		node.Region = nullStringOr(region, "")
		node.HomeProbeTargetID = nullStringOr(homeProbeTargetID, "")
		node.BillingMode = nullStringOr(billingMode, "both")
		if monthlyResetDay <= 0 {
			monthlyResetDay = 1
		}
		node.MonthlyResetDay = monthlyResetDay
		node.ExpiryDate = nullStringOr(expiryDate, "")
		node.ExpiryPermanent = expiryPermanent != 0
		node.BillingCycle = nullStringOr(billingCycle, "")
		node.RenewalAmount = floatPtr(renewalAmount)
		node.RenewalCurrency = nullStringOr(renewalCurrency, "CNY")
		node.DisplayOrder = displayOrder
		node.PublicIPv4 = nullStringOr(publicIPv4, "")
		node.PublicIPv6 = nullStringOr(publicIPv6, "")
		node.MonthlyQuotaBytes = int64Ptr(quota)
		node.LastSeenAt = unixStringPtr(lastSeenAt)
		node.CreatedAt = unixStringOr(createdAt, now)
		node.UpdatedAt = unixStringOr(updatedAt, now)
		node.Hostname = nullStringOr(hostname, "")
		node.OSName = nullStringOr(osName, "")
		node.OSVersion = nullStringOr(osVersion, "")
		node.Kernel = nullStringOr(kernel, "")
		node.Arch = nullStringOr(arch, "")
		node.Virtualization = nullStringOr(virtualization, "")
		node.CPUModel = nullStringOr(cpuModel, "")
		node.CPUCores = intSQLPtr(cpuCores)
		node.MemoryTotalBytes = int64Ptr(memoryTotal)
		node.DiskTotalBytes = int64Ptr(diskTotal)
		node.BootTime = unixStringPtr(bootTime)
		node.AgentVersion = nullStringOr(agentVersion, "")
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if nodes == nil {
		nodes = []AdminNode{}
	}
	return nodes, nil
}

func (s *sqliteAdminDomain) AdminProbeTargets(ctx context.Context) ([]AdminProbeTarget, error) {
	rows, err := s.queryReadDB().QueryContext(ctx, `
		SELECT pt.id, pt.name, pt.type, pt.address, pt.port, pt.count, pt.timeout_ms, pt.interval_sec, pt.display_order,
		       n.id, n.display_name, npt.enabled
		FROM probe_targets pt
		LEFT JOIN node_probe_targets npt ON npt.target_id = pt.id
		LEFT JOIN nodes n ON n.id = npt.node_id
		  AND NOT EXISTS (
			SELECT 1 FROM admin_deletion_jobs node_deletion
			WHERE node_deletion.entity_kind = 'node'
			  AND node_deletion.entity_id = n.id
			  AND node_deletion.state IN ('pending', 'running')
		  )
		WHERE NOT EXISTS (
			SELECT 1 FROM admin_deletion_jobs deletion
			WHERE deletion.entity_kind = 'probe_target'
			  AND deletion.entity_id = pt.id
			  AND deletion.state IN ('pending', 'running')
		)
		ORDER BY pt.display_order ASC, pt.id ASC, npt.node_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	targets := make([]AdminProbeTarget, 0)
	indexByID := map[string]int{}
	for rows.Next() {
		var target AdminProbeTarget
		var port sql.NullInt64
		var nodeID, nodeDisplayName sql.NullString
		var assignmentEnabled sql.NullInt64
		if err := rows.Scan(&target.ID, &target.Name, &target.Type, &target.Address, &port, &target.Count, &target.TimeoutMS, &target.IntervalSec, &target.DisplayOrder, &nodeID, &nodeDisplayName, &assignmentEnabled); err != nil {
			return nil, err
		}
		if existingIndex, exists := indexByID[target.ID]; exists {
			if nodeID.Valid {
				targets[existingIndex].Assignments = append(targets[existingIndex].Assignments, AdminProbeTargetAssignment{NodeID: nodeID.String, NodeDisplayName: nullStringOr(nodeDisplayName, ""), Enabled: assignmentEnabled.Valid && assignmentEnabled.Int64 != 0})
			}
			continue
		}
		if port.Valid {
			converted := int(port.Int64)
			target.Port = &converted
		}
		target.Assignments = []AdminProbeTargetAssignment{}
		if nodeID.Valid {
			target.Assignments = append(target.Assignments, AdminProbeTargetAssignment{NodeID: nodeID.String, NodeDisplayName: nullStringOr(nodeDisplayName, ""), Enabled: assignmentEnabled.Valid && assignmentEnabled.Int64 != 0})
		}
		indexByID[target.ID] = len(targets)
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}

func (s *sqliteAdminDomain) CreateAdminProbeTarget(ctx context.Context, create AdminProbeTargetCreateRequest) (AdminProbeTarget, error) {
	if err := create.normalize(); err != nil {
		return AdminProbeTarget{}, err
	}
	targetID := create.ID
	if targetID == "" {
		generated, err := generatedAdminNodeID(create.Name)
		if err != nil {
			return AdminProbeTarget{}, err
		}
		targetID = generated
	}
	now := time.Now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
		return AdminProbeTarget{}, err
	}
	usageBefore, err := probeNodeUsagesTx(ctx, tx)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	displayOrder := create.DisplayOrder
	if displayOrder == 0 {
		var maxDisplayOrder int64
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(pt.display_order), 0)
			FROM probe_targets pt
			WHERE NOT EXISTS (
				SELECT 1 FROM admin_deletion_jobs deletion
				WHERE deletion.entity_kind = 'probe_target'
				  AND deletion.entity_id = pt.id
				  AND deletion.state IN ('pending', 'running')
			)
		`).Scan(&maxDisplayOrder); err != nil {
			return AdminProbeTarget{}, err
		}
		maxInt := int64(^uint(0) >> 1)
		if maxDisplayOrder >= maxInt-10 {
			displayOrder = int(maxInt)
		} else {
			displayOrder = int(maxDisplayOrder) + 10
		}
	}
	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO probe_targets (id, name, type, address, port, count, timeout_ms, interval_sec, display_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, targetID, create.Name, create.Type, create.Address, adminOptionalInt64SQLValue(create.Port), create.Count, create.TimeoutMS, create.IntervalSec, displayOrder, now, now)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return AdminProbeTarget{}, err
	}
	if affected == 0 {
		return AdminProbeTarget{}, errProbeTargetAlreadyExists
	}
	for _, assignment := range create.Assignments {
		var nodeExists int
		if err := tx.QueryRowContext(ctx, activeAdminNodeExistsSQL, assignment.NodeID).Scan(&nodeExists); err != nil {
			if err == sql.ErrNoRows {
				return AdminProbeTarget{}, errInvalidAdminTargetWrite
			}
			return AdminProbeTarget{}, err
		}
		enabled := 0
		if assignment.Enabled {
			enabled = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO node_probe_targets (node_id, target_id, enabled)
			VALUES (?, ?, ?)
			ON CONFLICT(node_id, target_id) DO UPDATE SET enabled = excluded.enabled
		`, assignment.NodeID, targetID, enabled); err != nil {
			return AdminProbeTarget{}, err
		}
	}
	usageAfter, err := probeNodeUsagesTx(ctx, tx)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	if err := validateProbeNodeUsageTransition(usageBefore, usageAfter); err != nil {
		return AdminProbeTarget{}, err
	}
	if err := bumpProbeConfigVersionTx(ctx, tx); err != nil {
		return AdminProbeTarget{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminProbeTarget{}, err
	}
	tx = nil
	return s.adminProbeTargetByID(ctx, targetID)
}

func (s *sqliteAdminDomain) UpdateAdminProbeTarget(ctx context.Context, targetID string, update AdminProbeTargetUpdateRequest) (AdminProbeTarget, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return AdminProbeTarget{}, errProbeTargetNotFound
	}
	if err := update.normalize(); err != nil {
		return AdminProbeTarget{}, err
	}
	patch := buildAdminProbeTargetPatch(update)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
		return AdminProbeTarget{}, err
	}
	current, err := adminProbeTargetConfigTx(ctx, tx, targetID)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	if err := validateAdminProbeTargetUpdate(current, update); err != nil {
		return AdminProbeTarget{}, err
	}
	usageBefore, err := probeNodeUsagesTx(ctx, tx)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	if !patch.empty() {
		patch.set("updated_at", time.Now().UTC().Unix())
		statement, args := patch.updateStatement("probe_targets", "id = ?", targetID)
		if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
			return AdminProbeTarget{}, err
		}
	}
	if update.Assignments != nil {
		for _, assignment := range update.Assignments {
			var nodeExists int
			if err := tx.QueryRowContext(ctx, activeAdminNodeExistsSQL, assignment.NodeID).Scan(&nodeExists); err != nil {
				if err == sql.ErrNoRows {
					return AdminProbeTarget{}, errInvalidAdminTargetWrite
				}
				return AdminProbeTarget{}, err
			}
			enabled := 0
			if assignment.Enabled {
				enabled = 1
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO node_probe_targets (node_id, target_id, enabled)
				VALUES (?, ?, ?)
				ON CONFLICT(node_id, target_id) DO UPDATE SET enabled = excluded.enabled
			`, assignment.NodeID, targetID, enabled); err != nil {
				return AdminProbeTarget{}, err
			}
			if !assignment.Enabled {
				if _, err := tx.ExecContext(ctx, `UPDATE nodes SET home_probe_target_id = NULL WHERE id = ? AND home_probe_target_id = ?`, assignment.NodeID, targetID); err != nil {
					return AdminProbeTarget{}, err
				}
			}
		}
	}
	usageAfter, err := probeNodeUsagesTx(ctx, tx)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	if err := validateProbeNodeUsageTransition(usageBefore, usageAfter); err != nil {
		return AdminProbeTarget{}, err
	}
	if err := bumpProbeConfigVersionTx(ctx, tx); err != nil {
		return AdminProbeTarget{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminProbeTarget{}, err
	}
	tx = nil
	return s.adminProbeTargetByID(ctx, targetID)
}

func adminProbeTargetConfigTx(ctx context.Context, tx *sql.Tx, targetID string) (adminProbeTargetConfig, error) {
	var current adminProbeTargetConfig
	err := tx.QueryRowContext(ctx, `
		SELECT type, address, port, count, timeout_ms, interval_sec
		FROM probe_targets pt
		WHERE pt.id = ?
		  AND NOT EXISTS (
			SELECT 1 FROM admin_deletion_jobs deletion
			WHERE deletion.entity_kind = 'probe_target'
			  AND deletion.entity_id = pt.id
			  AND deletion.state IN ('pending', 'running')
		  )
	`, targetID).Scan(
		&current.targetType,
		&current.address,
		&current.port,
		&current.count,
		&current.timeoutMS,
		&current.intervalSec,
	)
	if err == sql.ErrNoRows {
		return adminProbeTargetConfig{}, errProbeTargetNotFound
	}
	return current, err
}

func (s *sqliteAdminDomain) DeleteAdminProbeTarget(ctx context.Context, targetID string) error {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return errProbeTargetNotFound
	}
	return s.enqueueAdminProbeTargetDeletion(ctx, targetID)
}

type probeNodeUsage struct {
	targetCount   int
	roundBudgetMS int64
}

func probeNodeUsagesTx(ctx context.Context, tx *sql.Tx) (map[string]probeNodeUsage, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT npt.node_id, COUNT(*) AS target_count, COALESCE(SUM(pt.count * pt.timeout_ms), 0) AS round_budget_ms
		FROM node_probe_targets npt
		JOIN probe_targets pt ON pt.id = npt.target_id
		WHERE npt.enabled = 1
		GROUP BY npt.node_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	usages := make(map[string]probeNodeUsage)
	for rows.Next() {
		var nodeID string
		var usage probeNodeUsage
		if err := rows.Scan(&nodeID, &usage.targetCount, &usage.roundBudgetMS); err != nil {
			return nil, err
		}
		usages[nodeID] = usage
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return usages, nil
}

func validateProbeNodeUsageTransition(before, after map[string]probeNodeUsage) error {
	for nodeID, current := range after {
		if current.targetCount <= maxProbeTargetsPerNode && current.roundBudgetMS <= maxProbeNodeRoundBudgetMS {
			continue
		}
		previous := before[nodeID]
		if current.targetCount > previous.targetCount || current.roundBudgetMS > previous.roundBudgetMS {
			return errInvalidAdminTargetWrite
		}
	}
	return nil
}

func (s *sqliteAdminDomain) adminProbeTargetByID(ctx context.Context, targetID string) (AdminProbeTarget, error) {
	return adminResourceByID(ctx, targetID, s.AdminProbeTargets, func(target AdminProbeTarget) string { return target.ID }, errProbeTargetNotFound)
}

func adminOptionalInt64SQLValue(value adminOptionalInt64) any {
	if !value.Set || !value.Valid {
		return nil
	}
	return value.Value
}

func validAdminProbeTargetForType(targetType string, address string, port sql.NullInt64) bool {
	switch targetType {
	case "tcping":
		return port.Valid && validPort(port.Int64)
	case "ping":
		return !port.Valid && validPingTargetAddress(address)
	case "http_get":
		return !port.Valid && validHTTPGetTargetAddress(address)
	default:
		return false
	}
}

func (s *sqliteAdminDomain) UpdateAdminNode(ctx context.Context, nodeID string, update AdminNodeUpdateRequest) (AdminNode, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return AdminNode{}, errNodeNotFound
	}
	if err := update.normalize(); err != nil {
		return AdminNode{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminNode{}, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
		return AdminNode{}, err
	}

	if err := ensureActiveAdminNodeExistsTx(ctx, tx, nodeID); err != nil {
		return AdminNode{}, err
	}
	if err := verifyAdminNodeProbeSelectionTx(ctx, tx, update); err != nil {
		return AdminNode{}, err
	}
	if err := applyAdminNodeUpdateTx(ctx, tx, nodeID, update); err != nil {
		return AdminNode{}, err
	}

	if err := tx.Commit(); err != nil {
		return AdminNode{}, err
	}
	tx = nil

	return s.adminNodeByID(ctx, nodeID)
}

func reorderAdminDisplayOrderTx(ctx context.Context, tx *sql.Tx, ids []string, invalid error, activeIDsQuery, updateQuery string) error {
	rows, err := tx.QueryContext(ctx, activeIDsQuery)
	if err != nil {
		return err
	}
	activeIDs := make(map[string]struct{}, len(ids))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		activeIDs[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(activeIDs) != len(ids) {
		return invalid
	}
	for _, id := range ids {
		if _, exists := activeIDs[id]; !exists {
			return invalid
		}
	}
	now := time.Now().UTC().Unix()
	for index, id := range ids {
		if _, err := tx.ExecContext(ctx, updateQuery, (index+1)*10, now, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqliteAdminDomain) ReorderAdminNodes(ctx context.Context, request AdminNodeReorderRequest) error {
	if err := request.normalize(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
		return err
	}
	if err := reorderAdminDisplayOrderTx(ctx, tx, request.NodeIDs, errInvalidAdminNodeUpdate, `
		SELECT n.id
		FROM nodes n
		WHERE NOT EXISTS (
			SELECT 1 FROM admin_deletion_jobs deletion
			WHERE deletion.entity_kind = 'node'
			  AND deletion.entity_id = n.id
			  AND deletion.state IN ('pending', 'running')
		)
	`, `UPDATE nodes SET display_order = ?, updated_at = ? WHERE id = ?`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *sqliteAdminDomain) ReorderAdminProbeTargets(ctx context.Context, request AdminProbeTargetReorderRequest) error {
	if err := request.normalize(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
		return err
	}
	if err := reorderAdminDisplayOrderTx(ctx, tx, request.TargetIDs, errInvalidAdminTargetWrite, `
		SELECT pt.id
		FROM probe_targets pt
		WHERE NOT EXISTS (
			SELECT 1 FROM admin_deletion_jobs deletion
			WHERE deletion.entity_kind = 'probe_target'
			  AND deletion.entity_id = pt.id
			  AND deletion.state IN ('pending', 'running')
		)
	`, `UPDATE probe_targets SET display_order = ?, updated_at = ? WHERE id = ?`); err != nil {
		return err
	}
	if err := bumpProbeConfigVersionTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func ensureActiveAdminNodeExistsTx(ctx context.Context, tx *sql.Tx, nodeID string) error {
	return requireAdminResourceTx(ctx, tx, activeAdminNodeExistsSQL, nodeID, errNodeNotFound)
}

func applyAdminNodeUpdateTx(ctx context.Context, tx *sql.Tx, nodeID string, update AdminNodeUpdateRequest) error {
	var usageBefore map[string]probeNodeUsage
	var err error
	if update.ProbeTargetIDs != nil {
		usageBefore, err = probeNodeUsagesTx(ctx, tx)
		if err != nil {
			return err
		}
	}

	patch := buildAdminNodePatch(update)
	if !patch.empty() || update.ProbeTargetIDs != nil {
		patch.set("updated_at", time.Now().UTC().Unix())
		statement, args := patch.updateStatement("nodes", "id = ?", nodeID)
		if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
			return err
		}
	}
	if update.ProbeTargetIDs == nil {
		return nil
	}

	if err := replaceAdminNodeProbeAssignmentsTx(ctx, tx, nodeID, update.ProbeTargetIDs); err != nil {
		return err
	}
	if update.HomeProbeTargetID == nil {
		if err := clearUnselectedHomeProbeTargetTx(ctx, tx, nodeID, update.ProbeTargetIDs); err != nil {
			return err
		}
	}
	usageAfter, err := probeNodeUsagesTx(ctx, tx)
	if err != nil {
		return err
	}
	if err := validateProbeNodeUsageTransition(usageBefore, usageAfter); err != nil {
		return err
	}
	return bumpProbeConfigVersionTx(ctx, tx)
}
