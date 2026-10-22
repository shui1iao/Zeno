package api

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shui1iao/zeno/internal/shared/probe"
)

func (s *sqliteAgentDomain) InsertProbeRound(ctx context.Context, nodeID string, target ProbeTarget, ts time.Time, samples []probe.Sample) error {
	return s.InsertProbeRounds(ctx, nodeID, []preparedAgentProbeRound{{target: target, ts: ts, samples: samples}})
}

func (s *sqliteAgentDomain) InsertProbeRounds(ctx context.Context, nodeID string, rounds []preparedAgentProbeRound) error {
	if len(rounds) == 0 {
		return nil
	}
	return s.writes.withAgentWrite(ctx, nodeID, func(ctx context.Context) error {
		return s.insertProbeRoundsOnce(ctx, nodeID, rounds)
	})
}

func (s *sqliteAgentDomain) insertProbeRoundsOnce(ctx context.Context, nodeID string, rounds []preparedAgentProbeRound) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	// Acquire SQLite's writer reservation before insertProbeRoundTx performs
	// idempotency reads. Otherwise a concurrent writer can commit between the
	// read and INSERT and make the deferred transaction fail with BUSY_SNAPSHOT.
	if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
		return err
	}
	if err := lockAgentNodeWriteTx(ctx, tx, nodeID); err != nil {
		return err
	}

	for _, round := range rounds {
		var targetEnabled int
		if err := tx.QueryRowContext(ctx, `
			SELECT 1
			FROM probe_targets pt
			JOIN node_probe_targets npt ON npt.target_id = pt.id
			WHERE pt.id = ?
			  AND npt.node_id = ? AND npt.enabled = 1
			  AND NOT EXISTS (
				SELECT 1 FROM admin_deletion_jobs deletion
				WHERE deletion.entity_kind = 'probe_target'
				  AND deletion.entity_id = pt.id
				  AND deletion.state IN ('pending', 'running')
			  )
		`, round.target.ID, nodeID).Scan(&targetEnabled); err != nil {
			if err == sql.ErrNoRows {
				return errInvalidAgentProbeResults
			}
			return err
		}
		if err := insertProbeRoundTx(ctx, tx, nodeID, round); err != nil {
			return err
		}
	}

	// Probe rounds may be delayed, batched, or use an Agent-provided timestamp.
	// They are service measurements, not authoritative node-liveness updates;
	// heartbeat/state/host reports own last_seen_at and status transitions.

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *sqliteAgentDomain) InsertAgentProbeResults(ctx context.Context, nodeID string, configVersion int64, rounds []preparedAgentProbeRound) (agentProbeInsertResult, error) {
	if configVersion < 0 {
		return agentProbeInsertResult{}, errInvalidAgentProbeResults
	}
	if len(rounds) == 0 {
		return agentProbeInsertResult{}, nil
	}
	if len(rounds) > maxAgentProbeRounds || !validAgentProbeErrorBudget(rounds) {
		return agentProbeInsertResult{}, errInvalidAgentProbeResults
	}
	var result agentProbeInsertResult
	err := s.writes.withAgentWrite(ctx, nodeID, func(ctx context.Context) error {
		var err error
		result, err = s.insertAgentProbeResultsOnce(ctx, nodeID, configVersion, rounds)
		return err
	})
	return result, err
}

func validAgentProbeErrorBudget(rounds []preparedAgentProbeRound) bool {
	totalErrorBytes := 0
	for _, round := range rounds {
		roundErrorBytes := 0
		for _, sample := range round.samples {
			errorText := strings.TrimSpace(sample.Error)
			if len(errorText) > maxProbeErrorBytes {
				return false
			}
			roundErrorBytes += len(errorText)
			totalErrorBytes += len(errorText)
			if roundErrorBytes > maxProbeErrorBytesPerRound || totalErrorBytes > maxAgentProbeErrorBytesPerRequest {
				return false
			}
		}
	}
	return true
}

func (s *sqliteAgentDomain) insertAgentProbeResultsOnce(ctx context.Context, nodeID string, configVersion int64, rounds []preparedAgentProbeRound) (agentProbeInsertResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return agentProbeInsertResult{}, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()

	// Acquire the SQLite writer lock before reading the probe config version and
	// target set. This keeps the non-zero config_version comparison and the full
	// batch insert in one serialized transaction boundary instead of accepting a
	// handler-level pre-read that could race with an admin config change.
	if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
		return agentProbeInsertResult{}, err
	}

	pending, result, err := classifyAgentProbeRounds(ctx, tx, nodeID, rounds)
	if err != nil {
		return agentProbeInsertResult{}, err
	}
	if len(pending) == 0 {
		// The writer reservation and indexed read are sufficient to classify the
		// replay. Leave the no-op transaction to the deferred rollback so an exact
		// retry has no durable database side effect.
		return result, nil
	}
	// Exact retries allocate no storage. Any mixed/new batch still checks the
	// high-water guard before its first INSERT and rolls back as one transaction.
	if err := s.ensureTelemetryStorage(); err != nil {
		return agentProbeInsertResult{}, err
	}
	if err := checkAgentProbeConfigVersion(ctx, tx, configVersion); err != nil {
		return agentProbeInsertResult{}, err
	}

	targetsByID, err := loadEnabledProbeTargetsByID(ctx, tx, nodeID)
	if err != nil {
		return agentProbeInsertResult{}, err
	}
	receivedAt := time.Now().UTC()
	for _, round := range pending {
		resolved, err := resolveAgentProbeRound(round, targetsByID, receivedAt)
		if err != nil {
			return agentProbeInsertResult{}, err
		}
		if err := insertProbeRoundTx(ctx, tx, nodeID, resolved); err != nil {
			return agentProbeInsertResult{}, err
		}
		result.inserted++
	}
	if err := tx.Commit(); err != nil {
		return agentProbeInsertResult{}, err
	}
	tx = nil
	return result, nil
}

func agentProbeSamplesForTarget(samples []probe.Sample, target ProbeTarget) []probe.Sample {
	normalized := make([]probe.Sample, 0, len(samples))
	remainingErrorBytes := maxProbeErrorBytesPerRound
	effectiveTimeoutMS := target.TimeoutMS
	if effectiveTimeoutMS <= 0 || effectiveTimeoutMS > int(localDrawableLatencyCap/time.Millisecond) {
		effectiveTimeoutMS = int(localDrawableLatencyCap / time.Millisecond)
	}
	for _, sample := range samples {
		copy := sample
		if !copy.Success {
			copy.LatencyMS = nil
		}
		copy.Error = boundedProbeErrorWithLimit(copy.Error, remainingErrorBytes)
		remainingErrorBytes -= len(copy.Error)
		if copy.LatencyMS != nil && *copy.LatencyMS > float64(effectiveTimeoutMS) {
			copy.Success = false
			copy.Error = "timeout"
		}
		normalized = append(normalized, copy)
	}
	return normalized
}

func insertProbeRoundTx(ctx context.Context, tx *sql.Tx, nodeID string, round preparedAgentProbeRound) error {
	remainingErrorBytes := maxProbeErrorBytesPerRound
	for index := range round.samples {
		round.samples[index].Error = boundedProbeErrorWithLimit(round.samples[index].Error, remainingErrorBytes)
		remainingErrorBytes -= len(round.samples[index].Error)
	}
	stats, err := probe.ComputeStats(round.samples)
	if err != nil {
		return err
	}
	ts := round.ts.UTC().Unix()
	idempotencyKey := strings.TrimSpace(round.idempotencyKey)
	payloadHash := strings.TrimSpace(round.payloadHash)
	if payloadHash == "" {
		payloadHash = probeRoundIdempotencyKey(round.samples)
	}
	if idempotencyKey == "" {
		idempotencyKey = "legacy:" + payloadHash
	}
	agentRoundID := strings.TrimSpace(round.agentRoundID)
	if agentRoundID != "" {
		var existingTargetID, existingType, existingPayloadHash string
		var existingTS int64
		err := tx.QueryRowContext(ctx, agentProbeRoundLookupSQL,
			nodeID, agentRoundID,
		).Scan(&existingTargetID, &existingTS, &existingType, &existingPayloadHash)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			if existingTargetID == round.target.ID && existingTS == ts && existingType == round.target.Type && existingPayloadHash == payloadHash {
				return nil
			}
			return fmt.Errorf("probe round id conflict for node %q", nodeID)
		}
	}
	var existingRoundID int64
	query := `
		SELECT id
		FROM probe_rounds
		WHERE node_id = ? AND target_id = ? AND ts = ? AND type = ? AND idempotency_key = ?
		LIMIT 1
	`
	args := []any{nodeID, round.target.ID, ts, round.target.Type, idempotencyKey}
	if legacyPattern, ok := migratedProbeRoundLegacyRetryPattern(idempotencyKey); ok {
		query = `
			SELECT id
			FROM probe_rounds
			WHERE node_id = ? AND target_id = ? AND ts = ? AND type = ?
			  AND (idempotency_key = ? OR idempotency_key GLOB ?)
			LIMIT 1
		`
		args = append(args, legacyPattern)
	}
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&existingRoundID); err != nil && err != sql.ErrNoRows {
		return err
	} else if err == nil {
		return nil
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, idempotency_key, agent_round_id, payload_hash, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms, error)
		VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nodeID, round.target.ID, ts, round.target.Type, idempotencyKey, agentRoundID, payloadHash, stats.Sent, stats.Received, stats.LossPercent, nullableFloat(stats.MinMS), nullableFloat(stats.AvgMS), nullableFloat(stats.MedianMS), nullableFloat(stats.MaxMS), nullableFloat(stats.StddevMS), roundError(round.samples))
	if err != nil {
		return err
	}
	roundID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	for index, sample := range round.samples {
		seq := sample.Seq
		if seq == 0 {
			seq = index + 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO probe_samples (round_id, seq, success, latency_ms, error)
			VALUES (?, ?, ?, ?, ?)
		`, roundID, seq, sqliteBoolInt(sample.Success), nullableFloat(sample.LatencyMS), nullIfEmpty(sample.Error)); err != nil {
			return err
		}
	}
	return nil
}

func boundedProbeErrorWithLimit(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if limit <= 0 {
		return ""
	}
	if limit > maxProbeErrorBytes {
		limit = maxProbeErrorBytes
	}
	if len(trimmed) <= limit {
		return trimmed
	}
	trimmed = trimmed[:limit]
	for len(trimmed) > 0 && !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}

const agentProbeRoundLookupSQL = `
			SELECT target_id, ts, type, payload_hash
			FROM probe_rounds
			WHERE node_id = ?
			  AND agent_round_id = ?
			  AND agent_round_id IS NOT NULL
			  AND agent_round_id <> ''
			LIMIT 1
`

const agentProbePayloadHashV2Prefix = "v2:"

func roundError(samples []probe.Sample) any {
	for _, sample := range samples {
		if !sample.Success && sample.Error != "" {
			return sample.Error
		}
	}
	return nil
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}
