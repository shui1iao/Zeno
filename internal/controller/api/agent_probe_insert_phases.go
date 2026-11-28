package api

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// classifyAgentProbeRounds splits a batch into rounds already durably stored and
// rounds still to insert.
//
// A committed round is the durable acknowledgement for the Agent, so this runs
// before any mutable config, target or timestamp state is consulted: a lost HTTP
// response must stay retryable even after the Agent's config snapshot has aged
// out. A round id that exists with different content is a genuine conflict
// rather than a replay, and is reported as such.
func classifyAgentProbeRounds(
	ctx context.Context,
	tx *sql.Tx,
	nodeID string,
	rounds []preparedAgentProbeRound,
) ([]preparedAgentProbeRound, agentProbeInsertResult, error) {
	existing, err := loadAgentProbeRoundsByIDTx(ctx, tx, nodeID, rounds)
	if err != nil {
		return nil, agentProbeInsertResult{}, err
	}

	pending := make([]preparedAgentProbeRound, 0, len(rounds))
	result := agentProbeInsertResult{}
	for _, round := range rounds {
		round.payloadHash = agentProbeRoundPayloadHash(round)
		roundID := strings.TrimSpace(round.agentRoundID)
		if found, ok := existing[roundID]; roundID != "" && ok {
			if !found.matches(round) {
				return nil, agentProbeInsertResult{}, errAgentProbeRoundConflict
			}
			result.idempotent++
			continue
		}
		pending = append(pending, round)
	}
	return pending, result, nil
}

// checkAgentProbeConfigVersion rejects a batch measured against a probe config
// the controller has since replaced.
//
// configVersion == 0 is the legacy/unknown snapshot value sent by older Agents.
// It intentionally skips the comparison; the enabled-target check applied to
// every round afterwards still validates the whole batch atomically.
func checkAgentProbeConfigVersion(ctx context.Context, tx *sql.Tx, configVersion int64) error {
	if configVersion <= 0 {
		return nil
	}
	var currentVersion int64
	if err := tx.QueryRowContext(ctx, `SELECT version FROM probe_config_meta WHERE id = 1`).Scan(&currentVersion); err != nil {
		return err
	}
	if currentVersion != configVersion {
		return errAgentProbeConfigStale
	}
	return nil
}

// loadEnabledProbeTargetsByID reads the node's enabled targets keyed by id, so
// each round can be matched without a per-round query inside the write
// transaction.
func loadEnabledProbeTargetsByID(ctx context.Context, tx *sql.Tx, nodeID string) (map[string]ProbeTarget, error) {
	targets, err := enabledProbeTargetsQuery(ctx, tx, nodeID)
	if err != nil {
		return nil, err
	}
	targetsByID := make(map[string]ProbeTarget, len(targets))
	for _, target := range targets {
		targetsByID[target.ID] = target
	}
	return targetsByID, nil
}

// resolveAgentProbeRound validates one pending round against the target set and
// returns it enriched with the fields the insert needs.
//
// All rejections collapse to errInvalidAgentProbeResults: the Agent submitted a
// batch that does not match the controller's current configuration, and the
// specific reason is deliberately not distinguished on the wire.
//
// The idempotency key prefers the Agent-supplied round id and falls back to a
// content hash, so Agents that predate round ids still get replay protection.
func resolveAgentProbeRound(
	round preparedAgentProbeRound,
	targetsByID map[string]ProbeTarget,
	receivedAt time.Time,
) (preparedAgentProbeRound, error) {
	target, ok := targetsByID[round.targetID]
	if !ok {
		return preparedAgentProbeRound{}, errInvalidAgentProbeResults
	}
	if round.targetType != "" && round.targetType != target.Type {
		return preparedAgentProbeRound{}, errInvalidAgentProbeResults
	}
	if len(round.samples) == 0 || len(round.samples) > target.Count ||
		len(round.samples) > maxAgentProbeSamplesPerRound {
		return preparedAgentProbeRound{}, errInvalidAgentProbeResults
	}
	if !agentTimestampWithinSkew(round.ts, receivedAt, maxAgentProbeTimestampPastSkew) {
		return preparedAgentProbeRound{}, errInvalidAgentProbeResults
	}

	round.target = target
	round.samples = agentProbeSamplesForTarget(round.samples, target)
	if agentRoundID := strings.TrimSpace(round.agentRoundID); agentRoundID != "" {
		round.idempotencyKey = "agent:" + agentRoundID
	} else {
		round.idempotencyKey = "legacy:" + probeRoundIdempotencyKey(round.samples)
	}
	return round, nil
}
