package api

import (
	"context"
	"database/sql"
	"time"
)

type sqliteReadQueries struct {
	db      *sql.DB
	latency *sqliteLatencyQueries
}

func (s *sqliteReadQueries) nodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.display_name, n.status, n.country_code, n.expiry_date, n.expiry_permanent, n.billing_cycle, n.renewal_amount, n.renewal_currency, fx.cny_rate, n.billing_mode, n.monthly_reset_day, n.last_seen_at,
		       h.os_name, h.os_version, h.kernel, h.arch, h.virtualization, h.cpu_model, h.cpu_cores, h.memory_total_bytes, h.disk_total_bytes, h.boot_time,
		       ss.cpu_percent, ss.load1, ss.load5, ss.load15, ss.uptime_seconds, ss.memory_used_bytes, ss.disk_used_bytes,
		       ss.net_in_speed_bps, ss.net_out_speed_bps, ss.net_in_total_bytes, ss.net_out_total_bytes,
		       lifetime.in_bytes, lifetime.out_bytes,
		       (
		         SELECT tm.billable_bytes
		         FROM traffic_monthly tm
		         WHERE tm.node_id = n.id
		           AND tm.billing_epoch = COALESCE(n.billing_traffic_epoch, 0)
		           AND tm.month = CASE
		             WHEN CAST(strftime('%d', 'now') AS INTEGER) < CASE
		               WHEN (CASE WHEN COALESCE(n.monthly_reset_day, 1) BETWEEN 1 AND 31 THEN n.monthly_reset_day ELSE 1 END) > CAST(strftime('%d', date('now', 'start of month', '+1 month', '-1 day')) AS INTEGER)
		               THEN CAST(strftime('%d', date('now', 'start of month', '+1 month', '-1 day')) AS INTEGER)
		               ELSE (CASE WHEN COALESCE(n.monthly_reset_day, 1) BETWEEN 1 AND 31 THEN n.monthly_reset_day ELSE 1 END)
		             END THEN strftime('%Y-%m', date('now', 'start of month', '-1 day'))
		             ELSE strftime('%Y-%m', 'now')
		           END
		       ) AS billable_bytes,
		       n.monthly_quota_bytes,
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
		LEFT JOIN host_info h ON h.node_id = n.id
		LEFT JOIN state_samples ss ON ss.id = (
			SELECT id FROM state_samples WHERE node_id = n.id ORDER BY ts DESC, id DESC LIMIT 1
		)
		LEFT JOIN traffic_lifetime lifetime ON lifetime.node_id = n.id
		LEFT JOIN exchange_rates fx ON fx.currency = COALESCE(NULLIF(TRIM(n.renewal_currency), ''), 'CNY')
		WHERE n.disabled = 0
		ORDER BY n.display_order ASC, n.id ASC
	`, int64(nodeHeartbeatOfflineAfter/time.Second))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	now := time.Now()
	for rows.Next() {
		var id, displayName, status string
		var countryCode, expiryDate, billingCycle, renewalCurrency, billingMode, osName, osVersion, kernel, arch, virtualization, cpuModel sql.NullString
		var renewalAmount, renewalCNYRate sql.NullFloat64
		var expiryPermanent int
		var monthlyResetDay, cpuCores, memoryTotal, diskTotal, bootTime, lastSeenAt, uptimeSeconds sql.NullInt64
		var cpuPercent, load1, load5, load15, netInSpeed, netOutSpeed sql.NullFloat64
		var memoryUsed, diskUsed, netInTotal, netOutTotal, netInLifetime, netOutLifetime, billable, quota, offlineDurationSec sql.NullInt64
		if err := rows.Scan(&id, &displayName, &status, &countryCode, &expiryDate, &expiryPermanent, &billingCycle, &renewalAmount, &renewalCurrency, &renewalCNYRate, &billingMode, &monthlyResetDay, &lastSeenAt, &osName, &osVersion, &kernel, &arch, &virtualization, &cpuModel, &cpuCores, &memoryTotal, &diskTotal, &bootTime, &cpuPercent, &load1, &load5, &load15, &uptimeSeconds, &memoryUsed, &diskUsed, &netInSpeed, &netOutSpeed, &netInTotal, &netOutTotal, &netInLifetime, &netOutLifetime, &billable, &quota, &offlineDurationSec); err != nil {
			return nil, err
		}
		resetDay := 1
		if monthlyResetDay.Valid && monthlyResetDay.Int64 >= 1 && monthlyResetDay.Int64 <= 31 {
			resetDay = int(monthlyResetDay.Int64)
		}
		period := billingPeriodFor(now, resetDay)
		node := Node{
			ID:                   id,
			DisplayName:          displayName,
			Status:               publicNodeStatusAfter(status, lastSeenAt, now, nodeOfflineAfterFromSeconds(offlineDurationSec)),
			OS:                   nullStringOr(osName, "linux"),
			OSVersion:            nullStringOr(osVersion, ""),
			Kernel:               nullStringOr(kernel, ""),
			Arch:                 nullStringOr(arch, ""),
			Virtualization:       nullStringOr(virtualization, ""),
			CPUModel:             nullStringOr(cpuModel, ""),
			CountryCode:          nullStringOr(countryCode, ""),
			ExpiryLabel:          expiryLabelValue(expiryDate, billingCycle, expiryPermanent != 0, now),
			RenewalAmount:        floatPtr(renewalAmount),
			RenewalCurrency:      nullStringOr(renewalCurrency, "CNY"),
			BillingCycle:         nullStringOr(billingCycle, ""),
			MonthlyCostCNY:       monthlyRenewalCostCNY(renewalAmount, renewalCNYRate, billingCycle, expiryPermanent != 0),
			CPUCores:             intPtr(cpuCores),
			CPUPercent:           floatPtr(cpuPercent),
			MemoryUsedBytes:      intPtr(memoryUsed),
			MemoryTotalBytes:     intPtr(memoryTotal),
			DiskUsedBytes:        intPtr(diskUsed),
			DiskTotalBytes:       intPtr(diskTotal),
			BootTime:             unixStringPtr(bootTime),
			Load1:                floatPtr(load1),
			Load5:                floatPtr(load5),
			Load15:               floatPtr(load15),
			UptimeSeconds:        intPtr(uptimeSeconds),
			NetInSpeedBps:        floatPtr(netInSpeed),
			NetOutSpeedBps:       floatPtr(netOutSpeed),
			NetInTotalBytes:      intPtr(netInTotal),
			NetOutTotalBytes:     intPtr(netOutTotal),
			NetInLifetimeBytes:   intPtr(netInLifetime),
			NetOutLifetimeBytes:  intPtr(netOutLifetime),
			BillingMode:          nullStringOr(billingMode, "both"),
			MonthlyResetDay:      resetDay,
			MonthlyPeriodStart:   period.StartDate,
			MonthlyPeriodEnd:     period.EndDate,
			MonthlyBillableBytes: intPtr(billable),
			MonthlyQuotaBytes:    intPtr(quota),
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (s *sqliteReadQueries) nodeExists(ctx context.Context, nodeID string) (bool, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id = ? AND disabled = 0`, nodeID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *sqliteReadQueries) latestHomeLatencySummaries(ctx context.Context) (map[string]*LatencySummary, error) {
	now := time.Now().UTC()
	since := now.Add(-24 * time.Hour).Unix()
	rows, err := s.db.QueryContext(ctx, `
		WITH eligible_nodes AS (
			SELECT n.id AS node_id, TRIM(n.home_probe_target_id) AS target_id
			FROM nodes n
			JOIN probe_targets pt ON pt.id = TRIM(n.home_probe_target_id)
			JOIN node_probe_targets npt ON npt.node_id = n.id AND npt.target_id = pt.id
			WHERE n.disabled = 0
			  AND TRIM(COALESCE(n.home_probe_target_id, '')) <> ''
			  AND npt.enabled = 1
		),
		loss_by_node AS (
			SELECT eligible.node_id, AVG(pr.loss_percent) AS loss_percent
			FROM eligible_nodes eligible
			JOIN probe_rounds pr ON pr.node_id = eligible.node_id AND pr.target_id = eligible.target_id
			WHERE pr.ts >= ?
			GROUP BY eligible.node_id
		)
		SELECT eligible.node_id, eligible.target_id, pt.name,
		       latest.median_ms, latest.avg_ms, loss_by_node.loss_percent, latest.ts
		FROM eligible_nodes eligible
		JOIN probe_targets pt ON pt.id = eligible.target_id
		JOIN probe_rounds latest ON latest.id = (
			SELECT candidate.id
			FROM probe_rounds candidate
			WHERE candidate.node_id = eligible.node_id
			  AND candidate.target_id = eligible.target_id
			  AND candidate.ts >= ?
			ORDER BY candidate.ts DESC, candidate.id DESC
			LIMIT 1
		)
		JOIN loss_by_node ON loss_by_node.node_id = eligible.node_id
	`, since, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := map[string]*LatencySummary{}
	for rows.Next() {
		var nodeID, targetID, targetName string
		var median, avg, loss sql.NullFloat64
		var ts int64
		if err := rows.Scan(&nodeID, &targetID, &targetName, &median, &avg, &loss, &ts); err != nil {
			return nil, err
		}
		medianPtr := floatPtr(median)
		avgPtr := floatPtr(avg)
		if avgPtr == nil {
			avgPtr = medianPtr
		}
		summaries[nodeID] = &LatencySummary{
			TargetID:    targetID,
			TargetName:  targetName,
			MedianMS:    medianPtr,
			AvgMS:       avgPtr,
			LossPercent: floatPtr(loss),
			UpdatedAt:   time.Unix(ts, 0).UTC().Format(time.RFC3339),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	historyByNode, err := s.homeLatencyHourlyHistory(ctx, now)
	if err != nil {
		return nil, err
	}
	for nodeID, summary := range summaries {
		if history, ok := historyByNode[nodeID]; ok {
			summary.HourlyHistory = history
		} else {
			summary.HourlyHistory = emptyHourlyLatencyHistory(now)
		}
	}
	return summaries, nil
}

const homeLatencyHistoryBucketCount = 12

func homeLatencyHistoryBounds(now time.Time) (time.Time, time.Time) {
	end := now.UTC().Truncate(time.Hour)
	return end.Add(-time.Duration(homeLatencyHistoryBucketCount-1) * time.Hour), end
}

func emptyHourlyLatencyHistory(now time.Time) []HourlyLatencyPoint {
	start, _ := homeLatencyHistoryBounds(now)
	history := make([]HourlyLatencyPoint, homeLatencyHistoryBucketCount)
	for index := range history {
		history[index].StartedAt = start.Add(time.Duration(index) * time.Hour).Format(time.RFC3339)
	}
	return history
}

func (s *sqliteReadQueries) homeLatencyHourlyHistory(ctx context.Context, now time.Time) (map[string][]HourlyLatencyPoint, error) {
	start, end := homeLatencyHistoryBounds(now)
	const stepSeconds int64 = int64(time.Hour / time.Second)
	rows, err := s.db.QueryContext(ctx, `
		WITH eligible_nodes AS (
			SELECT n.id AS node_id, TRIM(n.home_probe_target_id) AS target_id
			FROM nodes n
			JOIN probe_targets pt ON pt.id = TRIM(n.home_probe_target_id)
			JOIN node_probe_targets npt ON npt.node_id = n.id AND npt.target_id = pt.id
			WHERE n.disabled = 0
			  AND TRIM(COALESCE(n.home_probe_target_id, '')) <> ''
			  AND npt.enabled = 1
		)
		SELECT eligible.node_id, (pr.ts / ?) * ? AS bucket_ts,
		       AVG(COALESCE(pr.avg_ms, pr.median_ms)), AVG(pr.loss_percent)
		FROM eligible_nodes eligible
		JOIN probe_rounds pr ON pr.node_id = eligible.node_id AND pr.target_id = eligible.target_id
		WHERE pr.ts >= ? AND pr.ts < ?
		GROUP BY eligible.node_id, bucket_ts
		ORDER BY eligible.node_id ASC, bucket_ts ASC
	`, stepSeconds, stepSeconds, start.Unix(), end.Add(time.Hour).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	raw := map[string]map[int64]HourlyLatencyPoint{}
	for rows.Next() {
		var nodeID string
		var bucketTS int64
		var latency, loss sql.NullFloat64
		if err := rows.Scan(&nodeID, &bucketTS, &latency, &loss); err != nil {
			return nil, err
		}
		if bucketTS < start.Unix() || bucketTS > end.Unix() {
			continue
		}
		if raw[nodeID] == nil {
			raw[nodeID] = map[int64]HourlyLatencyPoint{}
		}
		raw[nodeID][bucketTS] = HourlyLatencyPoint{
			StartedAt:   time.Unix(bucketTS, 0).UTC().Format(time.RFC3339),
			LatencyMS:   floatPtr(latency),
			LossPercent: floatPtr(loss),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	historyByNode := make(map[string][]HourlyLatencyPoint, len(raw))
	for nodeID, buckets := range raw {
		history := emptyHourlyLatencyHistory(now)
		for index := range history {
			bucketTS := start.Add(time.Duration(index) * time.Hour).Unix()
			if point, ok := buckets[bucketTS]; ok {
				history[index] = point
			}
		}
		historyByNode[nodeID] = history
	}
	return historyByNode, nil
}

func (s *sqliteReadQueries) latestLatencySummariesByNode(ctx context.Context) (map[string][]LatencySummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT npt.node_id, pt.id AS target_id, pt.name AS target_name,
		       pr.median_ms, pr.avg_ms, pr.loss_percent, pr.ts
		FROM node_probe_targets npt
		JOIN nodes n ON n.id = npt.node_id
		JOIN probe_targets pt ON pt.id = npt.target_id
		JOIN probe_rounds pr ON pr.id = (
			SELECT candidate.id
			FROM probe_rounds candidate
			WHERE candidate.node_id = npt.node_id
			  AND candidate.target_id = npt.target_id
			ORDER BY candidate.ts DESC, candidate.id DESC
			LIMIT 1
		)
		WHERE n.disabled = 0
		  AND npt.enabled = 1
		ORDER BY npt.node_id ASC, pt.display_order ASC, pt.name ASC, pt.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := map[string][]LatencySummary{}
	for rows.Next() {
		var nodeID, targetID, targetName string
		var median, avg, loss sql.NullFloat64
		var ts int64
		if err := rows.Scan(&nodeID, &targetID, &targetName, &median, &avg, &loss, &ts); err != nil {
			return nil, err
		}
		summaries[nodeID] = append(summaries[nodeID], LatencySummary{
			TargetID:    targetID,
			TargetName:  targetName,
			MedianMS:    floatPtr(median),
			AvgMS:       floatPtr(avg),
			LossPercent: floatPtr(loss),
			UpdatedAt:   time.Unix(ts, 0).UTC().Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return summaries, nil
}

func (s *sqliteReadQueries) serviceTargets(ctx context.Context) ([]ServiceTarget, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pt.id, pt.name, pt.type,
		       COUNT(DISTINCT CASE WHEN n.id IS NOT NULL AND COALESCE(npt.enabled, 0) = 1 AND n.disabled = 0 THEN n.id END) AS assigned_nodes
		FROM probe_targets pt
		LEFT JOIN node_probe_targets npt ON npt.target_id = pt.id
		LEFT JOIN nodes n ON n.id = npt.node_id
		WHERE NOT EXISTS (
			SELECT 1 FROM admin_deletion_jobs deletion
			WHERE deletion.entity_kind = 'probe_target'
			  AND deletion.entity_id = pt.id
			  AND deletion.state IN ('pending', 'running')
		)
		GROUP BY pt.id, pt.name, pt.type, pt.display_order
		ORDER BY pt.display_order ASC, pt.name ASC, pt.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	targets := []ServiceTarget{}
	for rows.Next() {
		var target ServiceTarget
		if err := rows.Scan(&target.ID, &target.Name, &target.Type, &target.AssignedNodeCount); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.populateServiceTargetLatencySummaries(ctx, targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func (s *sqliteReadQueries) populateServiceTargetLatencySummaries(ctx context.Context, targets []ServiceTarget) error {
	if len(targets) == 0 {
		return nil
	}
	since := time.Now().UTC().Add(-24 * time.Hour).Unix()
	rows, err := s.db.QueryContext(ctx, `
		WITH reporting AS (
			SELECT pr.target_id, COUNT(DISTINCT pr.node_id) AS reporting_node_count
			FROM probe_rounds pr
			JOIN nodes n ON n.id = pr.node_id
			JOIN node_probe_targets npt ON npt.node_id = pr.node_id AND npt.target_id = pr.target_id
			WHERE pr.ts >= ?
			  AND n.disabled = 0
			  AND npt.enabled = 1
			GROUP BY pr.target_id
		)
		SELECT pt.id, COALESCE(reporting.reporting_node_count, 0),
		       latest.median_ms, latest.avg_ms, latest.loss_percent, latest.ts
		FROM probe_targets pt
		JOIN probe_rounds latest ON latest.id = (
			SELECT candidate.id
			FROM probe_rounds candidate
			JOIN nodes candidate_node ON candidate_node.id = candidate.node_id
			JOIN node_probe_targets candidate_assignment
			  ON candidate_assignment.node_id = candidate.node_id
			 AND candidate_assignment.target_id = candidate.target_id
			WHERE candidate.target_id = pt.id
			  AND candidate_node.disabled = 0
			  AND candidate_assignment.enabled = 1
			ORDER BY candidate.ts DESC, candidate.id DESC
			LIMIT 1
		)
		LEFT JOIN reporting ON reporting.target_id = pt.id
		WHERE NOT EXISTS (
			SELECT 1 FROM admin_deletion_jobs deletion
			WHERE deletion.entity_kind = 'probe_target'
			  AND deletion.entity_id = pt.id
			  AND deletion.state IN ('pending', 'running')
		)
	`, since)
	if err != nil {
		return err
	}
	defer rows.Close()

	indexByID := make(map[string]int, len(targets))
	for index := range targets {
		indexByID[targets[index].ID] = index
	}
	for rows.Next() {
		var targetID string
		var reportingNodeCount int
		var median, avg, loss sql.NullFloat64
		var ts sql.NullInt64
		if err := rows.Scan(&targetID, &reportingNodeCount, &median, &avg, &loss, &ts); err != nil {
			return err
		}
		index, ok := indexByID[targetID]
		if !ok {
			continue
		}
		targets[index].ReportingNodeCount = reportingNodeCount
		targets[index].MedianMS = floatPtr(median)
		targets[index].AvgMS = floatPtr(avg)
		targets[index].LossPercent = floatPtr(loss)
		if ts.Valid {
			targets[index].UpdatedAt = time.Unix(ts.Int64, 0).UTC().Format(time.RFC3339)
		}
	}
	return rows.Err()
}
