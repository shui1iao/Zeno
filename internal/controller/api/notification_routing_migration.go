package api

import (
	"context"
	"fmt"
	"strings"
)

// migrateNotificationRoutingBindings backfills the routing binding
// (delivery_version, destination_fingerprint) of channels created before it
// existed. notification_states rows record the binding they were notified
// under, and a changed binding re-baselines them silently.
func (s *sqliteNotificationDomain) migrateNotificationRoutingBindings(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite store is closed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { rollbackUnlessCommitted(tx) }()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, destination, delivery_version, destination_fingerprint
		FROM notification_channels
		WHERE delivery_version < 1 OR TRIM(destination_fingerprint) = ''
		ORDER BY id ASC
	`)
	if err != nil {
		return err
	}
	type channelBinding struct {
		id          string
		destination string
		version     int64
		fingerprint string
	}
	bindings := make([]channelBinding, 0)
	for rows.Next() {
		var binding channelBinding
		if err := rows.Scan(&binding.id, &binding.destination, &binding.version, &binding.fingerprint); err != nil {
			_ = rows.Close()
			return err
		}
		if binding.version < 1 {
			binding.version = 1
		}
		if strings.TrimSpace(binding.fingerprint) == "" {
			binding.fingerprint = notificationDestinationFingerprint("telegram", binding.destination)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, binding := range bindings {
		result, err := tx.ExecContext(ctx, `
			UPDATE notification_channels
			SET delivery_version = ?, destination_fingerprint = ?
			WHERE id = ? AND (delivery_version < 1 OR TRIM(destination_fingerprint) = '')
		`, binding.version, binding.fingerprint, binding.id)
		if err != nil {
			return err
		}
		if _, err := result.RowsAffected(); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}
