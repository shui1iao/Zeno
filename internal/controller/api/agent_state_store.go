package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net"
	"strings"
	"time"
)

func (s *sqliteAgentDomain) InsertAgentState(ctx context.Context, nodeID string, state AgentStateRequest) error {
	if err := s.ensureTelemetryStorage(); err != nil {
		return err
	}
	return s.writes.withAgentWrite(ctx, nodeID, func(ctx context.Context) error {
		return s.insertAgentStateOnce(ctx, nodeID, state)
	})
}

func (s *sqliteAgentDomain) insertAgentStateOnce(ctx context.Context, nodeID string, state AgentStateRequest) error {
	now := time.Now().UTC()
	nowUnix := now.Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	if _, err := insertAgentStateSampleTx(ctx, tx, nodeID, state, now, false); err != nil {
		return err
	}
	if err := updateAgentLivenessOnlyTx(ctx, tx, nodeID, nowUnix); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *sqliteAgentDomain) RecordAgentStateReport(ctx context.Context, nodeID string, state AgentStateRequest) (bool, notificationStatusTransition, error) {
	if err := s.ensureTelemetryStorage(); err != nil {
		return false, notificationStatusTransition{}, err
	}
	var accepted bool
	var transition notificationStatusTransition
	err := s.writes.withAgentWrite(ctx, nodeID, func(ctx context.Context) error {
		var err error
		accepted, transition, err = s.recordAgentStateReportOnce(ctx, nodeID, state)
		return err
	})
	return accepted, transition, err
}

func (s *sqliteAgentDomain) recordAgentStateReportOnce(ctx context.Context, nodeID string, state AgentStateRequest) (bool, notificationStatusTransition, error) {
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, notificationStatusTransition{}, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()

	accepted, err := insertAgentStateSampleTx(ctx, tx, nodeID, state, now, true)
	if err != nil {
		return false, notificationStatusTransition{}, err
	}
	if !accepted {
		if err := updateAgentLivenessOnlyTx(ctx, tx, nodeID, now.Unix()); err != nil {
			return false, notificationStatusTransition{}, err
		}
		if err := tx.Commit(); err != nil {
			return false, notificationStatusTransition{}, err
		}
		tx = nil
		return false, notificationStatusTransition{}, nil
	}

	transition, err := recordAgentStateAlertRuleTransitionTx(ctx, tx, nodeID, time.Unix(state.TS, 0).UTC(), state)
	if err != nil {
		return false, notificationStatusTransition{}, err
	}
	if err := tx.Commit(); err != nil {
		return false, notificationStatusTransition{}, err
	}
	tx = nil
	return true, transition, nil
}

func insertAgentStateSampleTx(ctx context.Context, tx *sql.Tx, nodeID string, state AgentStateRequest, receivedAt time.Time, enforceRateLimit bool) (bool, error) {
	receivedUnix := receivedAt.UTC().Unix()
	sampleTS := time.Unix(state.TS, 0).UTC()
	sampleID := strings.TrimSpace(state.effectiveSampleID())
	if sampleID != "" && !validAgentStateSampleID(sampleID) {
		return false, errInvalidAgentStateReport
	}
	payloadHash, err := agentStatePayloadHash(state)
	if err != nil {
		return false, errInvalidAgentStateReport
	}
	if err := lockAgentNodeWriteTx(ctx, tx, nodeID); err != nil {
		return false, err
	}

	duplicate, err := agentStateSampleDuplicate(ctx, tx, nodeID, sampleID, payloadHash, state.TS)
	if err != nil {
		return false, err
	}
	if duplicate {
		return false, nil
	}

	if enforceRateLimit {
		limited, err := agentStateRateLimited(ctx, tx, nodeID, state.TS)
		if err != nil {
			return false, err
		}
		if limited {
			return false, nil
		}
	}

	counters := resolveAgentStateOptionalCounters(state)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO state_samples (
			node_id, sample_id, payload_hash, received_at, ts, cpu_percent, load1, load5, load15,
			memory_used_bytes, memory_total_bytes, swap_used_bytes, swap_total_bytes,
			disk_used_bytes, disk_total_bytes, net_in_total_bytes, net_out_total_bytes,
			net_in_speed_bps, net_out_speed_bps, process_count, tcp_connection_count, udp_connection_count, uptime_seconds
		)
		VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nodeID, sampleID, payloadHash, receivedUnix, state.TS, state.CPUPercent, state.Load1, state.Load5, state.Load15, state.MemoryUsedBytes, state.MemoryTotalBytes, state.SwapUsedBytes, state.SwapTotalBytes, state.DiskUsedBytes, state.DiskTotalBytes, counters.netInTotalBytes, counters.netOutTotalBytes, state.NetInSpeedBps, state.NetOutSpeedBps, state.ProcessCount, counters.tcpConnectionCount, counters.udpConnectionCount, state.UptimeSeconds); err != nil {
		return false, err
	}

	if err := applyAgentStateTraffic(ctx, tx, nodeID, state, sampleTS, receivedUnix); err != nil {
		return false, err
	}
	return true, nil
}

const agentStateSampleLookupSQL = `
	SELECT payload_hash
	FROM state_samples INDEXED BY idx_state_samples_node_sample_id
	WHERE node_id = ?
	  AND sample_id = ?
	  AND sample_id IS NOT NULL
	  AND sample_id <> ''
	LIMIT 1
`

func lockAgentNodeWriteTx(ctx context.Context, tx *sql.Tx, nodeID string) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET updated_at = updated_at
		WHERE id = ? AND disabled = 0
	`, nodeID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errNodeNotFound
	}
	return nil
}

func updateAgentLivenessOnlyTx(ctx context.Context, tx *sql.Tx, nodeID string, nowUnix int64) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET status = CASE WHEN status IN ('warning', 'offline') THEN status ELSE 'online' END,
		    last_seen_at = CASE WHEN last_seen_at IS NULL OR last_seen_at <= ? THEN ? ELSE last_seen_at END,
		    updated_at = CASE WHEN updated_at <= ? THEN ? ELSE updated_at END
		WHERE id = ? AND disabled = 0
	`, nowUnix, nowUnix, nowUnix, nowUnix, nodeID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errNodeNotFound
	}
	return nil
}

func agentStatePayloadHash(state AgentStateRequest) (string, error) {
	copy := state
	copy.SampleID = ""
	copy.IdempotencyKey = ""
	payload, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func normalizeAgentPublicIP(value string, family int) string {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return ""
	}
	if family == 4 {
		ipv4 := ip.To4()
		if ipv4 == nil {
			return ""
		}
		return ipv4.String()
	}
	if family == 6 {
		if ip.To4() != nil || ip.To16() == nil {
			return ""
		}
		return ip.String()
	}
	return ""
}

func normalizeAgentCountryCode(value string) string {
	trimmed := strings.ToUpper(strings.TrimSpace(value))
	if len(trimmed) != 2 {
		return ""
	}
	for _, r := range trimmed {
		if r < 'A' || r > 'Z' {
			return ""
		}
	}
	return trimmed
}
