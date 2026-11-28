package api

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type probeConfigVersionStore interface {
	BumpProbeConfigVersion(ctx context.Context) (int64, error)
	ProbeConfigVersion(ctx context.Context) (int64, error)
}

var errProbeConfigAckInvalid = errors.New("invalid probe config ack")

func (s *sqliteAgentDomain) BumpProbeConfigVersion(ctx context.Context) (int64, error) {
	if _, err := s.db.ExecContext(ctx, bumpProbeConfigVersionSQL, time.Now().UTC().Unix()); err != nil {
		return 0, err
	}
	return s.ProbeConfigVersion(ctx)
}

const bumpProbeConfigVersionSQL = `
	INSERT INTO probe_config_meta (id, version, updated_at)
	VALUES (1, 1, ?)
	ON CONFLICT(id) DO UPDATE SET
		version = version + 1,
		updated_at = excluded.updated_at
`

func bumpProbeConfigVersionTx(ctx context.Context, tx *sql.Tx) error {
	now := time.Now().UTC().Unix()
	_, err := tx.ExecContext(ctx, bumpProbeConfigVersionSQL, now)
	return err
}

func (s *sqliteAgentDomain) ProbeConfigVersion(ctx context.Context) (int64, error) {
	var version int64
	err := s.db.QueryRowContext(ctx, `SELECT version FROM probe_config_meta WHERE id = 1`).Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}

func (s *sqliteAgentDomain) RecordProbeConfigApplied(ctx context.Context, nodeID string, version int64, now time.Time) error {
	if version <= 0 {
		return errProbeConfigAckInvalid
	}
	return s.writes.withAgentWrite(ctx, nodeID, func(ctx context.Context) error {
		return s.recordProbeConfigAppliedOnce(ctx, nodeID, version, now)
	})
}

func (s *sqliteAgentDomain) recordProbeConfigAppliedOnce(ctx context.Context, nodeID string, version int64, now time.Time) error {
	if version <= 0 {
		return errProbeConfigAckInvalid
	}
	nowUnix := now.UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	var currentVersion int64
	if err := tx.QueryRowContext(ctx, `SELECT version FROM probe_config_meta WHERE id = 1`).Scan(&currentVersion); err != nil {
		return err
	}
	if version > currentVersion {
		return errProbeConfigAckInvalid
	}
	if err := execRequiringRows(ctx, tx, errNodeNotFound, `
		UPDATE nodes
		SET probe_config_applied_version = CASE
		      WHEN COALESCE(probe_config_applied_version, 0) <= ? THEN ?
		      ELSE probe_config_applied_version
		    END,
		    probe_config_applied_at = CASE
		      WHEN COALESCE(probe_config_applied_version, 0) <= ? THEN ?
		      ELSE probe_config_applied_at
		    END,
		    updated_at = CASE
		      WHEN COALESCE(probe_config_applied_version, 0) <= ? THEN ?
		      ELSE updated_at
		    END
		WHERE id = ? AND disabled = 0
	`, version, version, version, nowUnix, version, nowUnix, nodeID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}
