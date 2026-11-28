package api

import (
	"context"
	"database/sql"
	"time"
)

type PreviewSeedOptions struct {
	NodeID      string
	DisplayName string
	CountryCode string
	AgentToken  string
}

type ProbeTarget struct {
	ID           string
	Name         string
	Type         string
	Address      string
	Port         *int
	Count        int
	TimeoutMS    int
	IntervalSec  int
	DisplayOrder int
}

func (s *sqliteAdminDomain) SeedPreviewData(ctx context.Context, options PreviewSeedOptions) error {
	nodeID := options.NodeID
	if nodeID == "" {
		nodeID = "example-node-a"
	}
	displayName := options.DisplayName
	if displayName == "" {
		displayName = "Example Node A"
	}
	countryCode := options.CountryCode
	if countryCode == "" {
		countryCode = "HK"
	}

	now := time.Now().UTC().Unix()
	tokenHash := "preview-local-collector-token-hash"
	if options.AgentToken != "" {
		tokenHash = hashAgentToken(options.AgentToken)
	}
	return withSQLiteTx(ctx, s.db, func(tx *sql.Tx) error {
		return seedPreviewDataTx(ctx, tx, nodeID, displayName, countryCode, tokenHash, now)
	})
}

func seedPreviewDataTx(ctx context.Context, tx *sql.Tx, nodeID, displayName, countryCode, tokenHash string, now int64) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, install_token, status, country_code, billing_mode, monthly_quota_bytes, monthly_reset_day, disabled, created_at, updated_at, last_seen_at)
		VALUES (?, ?, ?, NULL, 'no_data', ?, 'both', ?, 1, 0, ?, ?, NULL)
		ON CONFLICT(id) DO UPDATE SET
			display_name = excluded.display_name,
			token_hash = excluded.token_hash,
			install_token = NULL,
			pending_token_hash = NULL,
			pending_token_expires_at = NULL,
			country_code = excluded.country_code,
			monthly_quota_bytes = excluded.monthly_quota_bytes,
			updated_at = excluded.updated_at
	`, nodeID, displayName, tokenHash, countryCode, int64(10*1024*1024*1024*1024), now, now); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO host_info (node_id, hostname, os_name, os_version, kernel, arch, virtualization, cpu_model, cpu_cores, memory_total_bytes, disk_total_bytes, agent_version, updated_at)
		VALUES (?, 'example-node-a', 'debian', '13', '', 'x86_64', 'kvm', '', 2, ?, ?, 'controller-local-preview', ?)
		ON CONFLICT(node_id) DO UPDATE SET
			hostname = excluded.hostname,
			os_name = excluded.os_name,
			os_version = excluded.os_version,
			arch = excluded.arch,
			virtualization = excluded.virtualization,
			cpu_cores = excluded.cpu_cores,
			memory_total_bytes = excluded.memory_total_bytes,
			disk_total_bytes = excluded.disk_total_bytes,
			agent_version = excluded.agent_version,
			updated_at = excluded.updated_at
	`, nodeID, int64(4112953344), int64(107374182400), now); err != nil {
		return err
	}

	for index, target := range DefaultPreviewProbeTargets() {
		if target.DisplayOrder == 0 {
			target.DisplayOrder = (index + 1) * 10
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO probe_targets (id, name, type, address, port, count, timeout_ms, interval_sec, display_order, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				name = excluded.name,
				type = excluded.type,
				address = excluded.address,
				port = excluded.port,
				count = excluded.count,
				timeout_ms = excluded.timeout_ms,
				interval_sec = excluded.interval_sec,
				display_order = CASE WHEN probe_targets.display_order = 0 THEN excluded.display_order ELSE probe_targets.display_order END,
				updated_at = excluded.updated_at
		`, target.ID, target.Name, target.Type, target.Address, nullableValue(target.Port), target.Count, target.TimeoutMS, target.IntervalSec, target.DisplayOrder, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO node_probe_targets (node_id, target_id, enabled)
			VALUES (?, ?, 1)
			ON CONFLICT(node_id, target_id) DO UPDATE SET enabled = 1
		`, nodeID, target.ID); err != nil {
			return err
		}
	}
	return bumpProbeConfigVersionTx(ctx, tx)
}

func DefaultPreviewProbeTargets() []ProbeTarget {
	return []ProbeTarget{
		// RFC 5737 documentation addresses are deliberately non-routable. Preview
		// data must never publish or probe a maintainer's real infrastructure.
		previewTarget("example-node-a-local", "Example Node A", "192.0.2.1", 443),
		previewTarget("google-dns", "Example DNS A", "192.0.2.53", 53),
		previewTarget("cloudflare-dns", "Example DNS B", "198.51.100.53", 53),
		previewTarget("telegram-dc5", "Example HTTPS", "203.0.113.44", 443),
		previewTarget("example-edge-a", "Example Edge A", "192.0.2.10", 443),
		previewTarget("example-edge-b", "Example Edge B", "198.51.100.20", 443),
		previewTarget("example-edge-c", "Example Edge C", "203.0.113.30", 443),
	}
}

func previewTarget(id, name, address string, port int) ProbeTarget {
	return ProbeTarget{
		ID:          id,
		Name:        name,
		Type:        "tcping",
		Address:     address,
		Port:        &port,
		Count:       3,
		TimeoutMS:   1000,
		IntervalSec: 30,
	}
}

func (s *sqliteAgentDomain) queryReadDB() *sql.DB {
	return preferredReadDB(s.readDB, s.db)
}

func (s *sqliteAgentDomain) EnabledProbeTargets(ctx context.Context, nodeID string) ([]ProbeTarget, error) {
	return enabledProbeTargetsQuery(ctx, s.queryReadDB(), nodeID)
}

func (s *sqliteAgentDomain) EnabledProbeTargetsWithConfigVersion(ctx context.Context, nodeID string) ([]ProbeTarget, int64, error) {
	tx, err := s.queryReadDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT version FROM probe_config_meta WHERE id = 1`).Scan(&version); err != nil {
		return nil, 0, err
	}
	targets, err := enabledProbeTargetsQuery(ctx, tx, nodeID)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	tx = nil
	return targets, version, nil
}

type probeTargetQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func enabledProbeTargetsQuery(ctx context.Context, queryer probeTargetQueryer, nodeID string) ([]ProbeTarget, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT pt.id, pt.name, pt.type, pt.address, pt.port, pt.count, pt.timeout_ms, pt.interval_sec
		FROM probe_targets pt
		JOIN node_probe_targets npt ON npt.target_id = pt.id
		JOIN nodes n ON n.id = npt.node_id
		WHERE npt.node_id = ?
		  AND n.disabled = 0
		  AND npt.enabled = 1
		  AND NOT EXISTS (
			SELECT 1 FROM admin_deletion_jobs deletion
			WHERE deletion.entity_kind = 'probe_target'
			  AND deletion.entity_id = pt.id
			  AND deletion.state IN ('pending', 'running')
		  )
		ORDER BY pt.display_order ASC, pt.id ASC
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	targets := make([]ProbeTarget, 0, maxProbeTargetsPerNode)
	var nodeBudgetMS int64
	for rows.Next() {
		var target ProbeTarget
		var port sql.NullInt64
		if err := rows.Scan(&target.ID, &target.Name, &target.Type, &target.Address, &port, &target.Count, &target.TimeoutMS, &target.IntervalSec); err != nil {
			return nil, err
		}
		target.Port = intSQLPtr(port)
		target = normalizeProbeTargetForExecution(target)
		targetBudgetMS := probeTargetRoundBudgetMS(target.Count, target.TimeoutMS)
		if len(targets) >= maxProbeTargetsPerNode || nodeBudgetMS+targetBudgetMS > maxProbeNodeRoundBudgetMS {
			continue
		}
		nodeBudgetMS += targetBudgetMS
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}
