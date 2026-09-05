package api

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sync"
	"time"
)

// Admin deletion only tombstones the entity in the request transaction. Raw
// history is then removed in bounded transactions by this durable
// worker. Each write takes its own turn in the shared fair writer scheduler;
// orchestration methods must not acquire a second, enclosing permit.
// The job row is the durable queue: pending and interrupted running
// jobs are both resumed after a Controller restart.
var errAdminDeletionHistoryRemaining = errors.New("admin deletion history remains")

const (
	adminDeleteBatchSize      = 1000
	adminDeletionIdleInterval = 250 * time.Millisecond
	adminDeletionBusyInterval = 5 * time.Millisecond
	adminDeletionErrorBackoff = time.Second
)

const (
	// Limit candidate rounds before looking for samples. LIMIT on joined
	// samples alone revisits every previously emptied round on every batch.
	selectNodeAdminProbeRoundsBatchSQL = `
		SELECT id FROM probe_rounds WHERE node_id = ? ORDER BY ts, id LIMIT ?`
	selectTargetAdminProbeRoundsBatchSQL = `
		SELECT id FROM probe_rounds WHERE target_id = ? ORDER BY ts, id LIMIT ?`
	deleteNodeProbeSamplesBatchSQL = `
		DELETE FROM probe_samples
		WHERE (round_id, seq) IN (
			SELECT round_id, seq FROM probe_samples
			WHERE round_id IN (` + selectNodeAdminProbeRoundsBatchSQL + `)
			LIMIT ?
		)`
	deleteTargetProbeSamplesBatchSQL = `
		DELETE FROM probe_samples
		WHERE (round_id, seq) IN (
			SELECT round_id, seq FROM probe_samples
			WHERE round_id IN (` + selectTargetAdminProbeRoundsBatchSQL + `)
			LIMIT ?
		)`
	deleteNodeProbeRoundsBatchSQL = `
		DELETE FROM probe_rounds
		WHERE id IN (` + selectNodeAdminProbeRoundsBatchSQL + `)
		AND NOT EXISTS (SELECT 1 FROM probe_samples WHERE round_id = probe_rounds.id)`
	deleteTargetProbeRoundsBatchSQL = `
		DELETE FROM probe_rounds
		WHERE id IN (` + selectTargetAdminProbeRoundsBatchSQL + `)
		AND NOT EXISTS (SELECT 1 FROM probe_samples WHERE round_id = probe_rounds.id)`
	deleteNodeStateSamplesBatchSQL = `
		DELETE FROM state_samples
		WHERE id IN (
			SELECT id FROM state_samples WHERE node_id = ? LIMIT ?
		)`
)

type adminDeletionJob struct {
	kind string
	id   string
}

type sqliteAdminDeletion struct {
	db     *sql.DB
	writes *sqliteWriteState
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (s *sqliteAdminDeletion) enqueueAdminNodeDeletion(ctx context.Context, nodeID string) error {
	return s.writes.withAgentWrite(ctx, adminDeletionWriteKey, func(ctx context.Context) error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { rollbackUnlessCommitted(tx) }()

		// Reserve the SQLite writer before existence/job reads. This serializes
		// the tombstone with Agent writes and probe-config mutations.
		if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
			return err
		}
		var jobState string
		err = tx.QueryRowContext(ctx, `
			SELECT state FROM admin_deletion_jobs
			WHERE entity_kind = 'node' AND entity_id = ?
		`, nodeID).Scan(&jobState)
		if err == nil && (jobState == "pending" || jobState == "running") {
			if err := tx.Commit(); err != nil {
				return err
			}
			tx = nil
			return nil
		}
		if err != nil && err != sql.ErrNoRows {
			return err
		}

		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id = ?`, nodeID).Scan(&exists); err != nil {
			if err == sql.ErrNoRows {
				return errNodeNotFound
			}
			return err
		}
		now := time.Now().UTC().Unix()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO admin_deletion_jobs
				(entity_kind, entity_id, state, attempts, last_error, created_at, updated_at, completed_at)
			VALUES ('node', ?, 'pending', 0, '', ?, ?, NULL)
			ON CONFLICT(entity_kind, entity_id) DO UPDATE SET
				state = 'pending', attempts = 0, last_error = '',
				created_at = excluded.created_at, updated_at = excluded.updated_at,
				completed_at = NULL
		`, nodeID, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE nodes
			SET disabled = 1,
				pending_token_hash = NULL,
				pending_token_expires_at = NULL,
				install_token = NULL,
				updated_at = ?
			WHERE id = ?
		`, now, nodeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE agent_enrollment_tokens
			SET revoked_at = COALESCE(revoked_at, ?)
			WHERE node_id = ? AND used_at IS NULL
		`, now, nodeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE node_probe_targets SET enabled = 0 WHERE node_id = ?`, nodeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE nodes SET home_probe_target_id = NULL WHERE id = ?`, nodeID); err != nil {
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
	})
}

func (s *sqliteAdminDeletion) enqueueAdminProbeTargetDeletion(ctx context.Context, targetID string) error {
	return s.writes.withAgentWrite(ctx, adminDeletionWriteKey, func(ctx context.Context) error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { rollbackUnlessCommitted(tx) }()
		if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
			return err
		}

		var jobState string
		err = tx.QueryRowContext(ctx, `
			SELECT state FROM admin_deletion_jobs
			WHERE entity_kind = 'probe_target' AND entity_id = ?
		`, targetID).Scan(&jobState)
		if err == nil && (jobState == "pending" || jobState == "running") {
			if err := tx.Commit(); err != nil {
				return err
			}
			tx = nil
			return nil
		}
		if err != nil && err != sql.ErrNoRows {
			return err
		}

		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM probe_targets WHERE id = ?`, targetID).Scan(&exists); err != nil {
			if err == sql.ErrNoRows {
				return errProbeTargetNotFound
			}
			return err
		}
		now := time.Now().UTC().Unix()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO admin_deletion_jobs
				(entity_kind, entity_id, state, attempts, last_error, created_at, updated_at, completed_at)
			VALUES ('probe_target', ?, 'pending', 0, '', ?, ?, NULL)
			ON CONFLICT(entity_kind, entity_id) DO UPDATE SET
				state = 'pending', attempts = 0, last_error = '',
				created_at = excluded.created_at, updated_at = excluded.updated_at,
				completed_at = NULL
		`, targetID, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE probe_targets SET updated_at = ? WHERE id = ?`, now, targetID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE node_probe_targets SET enabled = 0 WHERE target_id = ?`, targetID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE nodes SET home_probe_target_id = NULL WHERE home_probe_target_id = ?`, targetID); err != nil {
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
	})
}

func (s *sqliteAdminDeletion) startAdminDeletionWorker() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runAdminDeletionWorker(ctx)
	}()
}

func (s *sqliteAdminDeletion) stopAdminDeletionWorker() {
	if s.cancel == nil {
		return
	}
	s.cancel()
	s.wg.Wait()
	s.cancel = nil
}

func (s *sqliteAdminDeletion) runAdminDeletionWorker(ctx context.Context) {
	for {
		processed, err := s.processNextAdminDeletionBatch(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("admin deletion worker failed: %v", err)
		}
		if ctx.Err() != nil {
			return
		}
		wait := adminDeletionIdleInterval
		if processed && err == nil {
			wait = adminDeletionBusyInterval
		} else if err != nil {
			wait = adminDeletionErrorBackoff
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}

// processNextAdminDeletionBatch performs at most one non-empty history batch,
// or one bounded final metadata transaction. A running job is eligible so a
// process crash never requires an in-memory repair step.
func (s *sqliteAdminDeletion) processNextAdminDeletionBatch(ctx context.Context) (bool, error) {
	job, found, err := s.nextAdminDeletionJob(ctx)
	if err != nil || !found {
		return false, err
	}
	if err := s.markAdminDeletionJobRunning(ctx, job); err != nil {
		return false, err
	}

	var processed bool
	switch job.kind {
	case "node":
		processed, err = s.processAdminNodeDeletionBatch(ctx, job.id)
	case "probe_target":
		processed, err = s.processAdminProbeTargetDeletionBatch(ctx, job.id)
	default:
		err = nil
		processed = false
	}
	if err != nil {
		s.recordAdminDeletionError(job, err)
	}
	return processed, err
}

func (s *sqliteAdminDeletion) nextAdminDeletionJob(ctx context.Context) (adminDeletionJob, bool, error) {
	var job adminDeletionJob
	err := s.db.QueryRowContext(ctx, `
		SELECT entity_kind, entity_id
		FROM admin_deletion_jobs
		WHERE state IN ('pending', 'running')
		ORDER BY updated_at ASC, entity_kind ASC, entity_id ASC
		LIMIT 1
	`).Scan(&job.kind, &job.id)
	if err == sql.ErrNoRows {
		return adminDeletionJob{}, false, nil
	}
	return job, err == nil, err
}

func (s *sqliteAdminDeletion) markAdminDeletionJobRunning(ctx context.Context, job adminDeletionJob) error {
	return s.writes.withAgentWrite(ctx, adminDeletionWriteKey, func(ctx context.Context) error {
		_, err := s.db.ExecContext(ctx, `
			UPDATE admin_deletion_jobs
			SET state = 'running',
				attempts = attempts + CASE WHEN state = 'pending' THEN 1 ELSE 0 END,
				last_error = '', updated_at = ?
			WHERE entity_kind = ? AND entity_id = ?
			  AND state IN ('pending', 'running')
		`, time.Now().UTC().Unix(), job.kind, job.id)
		return err
	})
}

func (s *sqliteAdminDeletion) recordAdminDeletionError(job adminDeletionJob, processErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.writes.withAgentWrite(ctx, adminDeletionWriteKey, func(ctx context.Context) error {
		_, err := s.db.ExecContext(ctx, `
			UPDATE admin_deletion_jobs
			SET state = 'pending', last_error = ?, updated_at = ?
			WHERE entity_kind = ? AND entity_id = ? AND state <> 'completed'
		`, boundedAdminDeletionError(processErr), time.Now().UTC().Unix(), job.kind, job.id)
		return err
	})
}

func boundedAdminDeletionError(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) > 512 {
		value = value[:512]
	}
	return value
}

func (s *sqliteAdminDeletion) processAdminNodeDeletionBatch(ctx context.Context, nodeID string) (bool, error) {
	removed, err := s.deleteAdminProbeHistoryBatch(ctx, deleteNodeProbeSamplesBatchSQL, deleteNodeProbeRoundsBatchSQL, nodeID)
	if err != nil || removed > 0 {
		return removed > 0, err
	}
	removed, err = s.deleteAdminRowsBatch(ctx, deleteNodeStateSamplesBatchSQL, nodeID)
	if err != nil || removed > 0 {
		return removed > 0, err
	}
	return true, s.finalizeAdminNodeDeletion(ctx, nodeID)
}

func (s *sqliteAdminDeletion) processAdminProbeTargetDeletionBatch(ctx context.Context, targetID string) (bool, error) {
	removed, err := s.deleteAdminProbeHistoryBatch(ctx, deleteTargetProbeSamplesBatchSQL, deleteTargetProbeRoundsBatchSQL, targetID)
	if err != nil || removed > 0 {
		return removed > 0, err
	}
	return true, s.finalizeAdminProbeTargetDeletion(ctx, targetID)
}

// Bound both examined rounds and deleted samples, including jobs resumed after
// the old worker left an arbitrarily large prefix of empty rounds. Removing the
// newly empty rounds in the same transaction advances the next batch's window
// without a durable cursor or an unbounded ON DELETE CASCADE for dense rounds.
func (s *sqliteAdminDeletion) deleteAdminProbeHistoryBatch(ctx context.Context, samplesSQL, roundsSQL, id string) (int64, error) {
	return withAgentWriteResult(s.writes, ctx, adminDeletionWriteKey, func(ctx context.Context) (int64, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return 0, err
		}
		defer func() { rollbackUnlessCommitted(tx) }()
		result, err := tx.ExecContext(ctx, samplesSQL, id, adminDeleteBatchSize, adminDeleteBatchSize)
		if err != nil {
			return 0, err
		}
		samples, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		result, err = tx.ExecContext(ctx, roundsSQL, id, adminDeleteBatchSize)
		if err != nil {
			return 0, err
		}
		rounds, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		tx = nil
		return samples + rounds, nil
	})
}

func (s *sqliteAdminDeletion) finalizeAdminNodeDeletion(ctx context.Context, nodeID string) error {
	return s.writes.withAgentWrite(ctx, adminDeletionWriteKey, func(ctx context.Context) error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { rollbackUnlessCommitted(tx) }()
		if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
			return err
		}
		var historyRows int
		if err := tx.QueryRowContext(ctx, `
			SELECT
				EXISTS (SELECT 1 FROM state_samples WHERE node_id = ?) +
				EXISTS (SELECT 1 FROM probe_rounds WHERE node_id = ?)
		`, nodeID, nodeID).Scan(&historyRows); err != nil {
			return err
		}
		if historyRows != 0 {
			return errAdminDeletionHistoryRemaining
		}
		for _, statement := range []string{
			`DELETE FROM traffic_monthly WHERE node_id = ?`,
			`DELETE FROM traffic_lifetime WHERE node_id = ?`,
			`DELETE FROM node_probe_targets WHERE node_id = ?`,
			`DELETE FROM alert_rule_states WHERE node_id = ?`,
			`DELETE FROM host_info WHERE node_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, statement, nodeID); err != nil {
				return err
			}
		}
		now := time.Now().UTC().Unix()
		if _, err := tx.ExecContext(ctx, `
			UPDATE alert_rules
			SET enabled = 0, updated_at = ?
			WHERE id IN (
				SELECT scope.rule_id
				FROM alert_rule_node_scopes scope
				WHERE scope.node_id = ?
				  AND (SELECT COUNT(*) FROM alert_rule_node_scopes all_scope WHERE all_scope.rule_id = scope.rule_id) = 1
			)
		`, now, nodeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, nodeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE admin_deletion_jobs
			SET state = 'completed', last_error = '', completed_at = ?, updated_at = ?
			WHERE entity_kind = 'node' AND entity_id = ?
		`, now, now, nodeID); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		tx = nil
		return nil
	})
}

func (s *sqliteAdminDeletion) finalizeAdminProbeTargetDeletion(ctx context.Context, targetID string) error {
	return s.writes.withAgentWrite(ctx, adminDeletionWriteKey, func(ctx context.Context) error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { rollbackUnlessCommitted(tx) }()
		if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
			return err
		}
		var historyRows int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM probe_rounds WHERE target_id = ?)`, targetID).Scan(&historyRows); err != nil {
			return err
		}
		if historyRows != 0 {
			return errAdminDeletionHistoryRemaining
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM node_probe_targets WHERE target_id = ?`, targetID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE nodes SET home_probe_target_id = NULL WHERE home_probe_target_id = ?`, targetID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM probe_targets WHERE id = ?`, targetID); err != nil {
			return err
		}
		now := time.Now().UTC().Unix()
		if _, err := tx.ExecContext(ctx, `
			UPDATE admin_deletion_jobs
			SET state = 'completed', last_error = '', completed_at = ?, updated_at = ?
			WHERE entity_kind = 'probe_target' AND entity_id = ?
		`, now, now, targetID); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		tx = nil
		return nil
	})
}

func (s *sqliteAdminDeletion) deleteAdminRowsBatch(ctx context.Context, query string, value any) (int64, error) {
	var removed int64
	err := s.writes.withAgentWrite(ctx, adminDeletionWriteKey, func(ctx context.Context) error {
		result, err := s.db.ExecContext(ctx, query, value, adminDeleteBatchSize)
		if err != nil {
			return err
		}
		removed, err = result.RowsAffected()
		return err
	})
	return removed, err
}
