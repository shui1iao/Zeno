package api

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

var adminNotificationTypeCatalog = []AdminNotificationType{
	{EventType: "node_offline", Label: "离线"},
	{EventType: "probe_unhealthy", Label: "异常"},
	{EventType: "renewal_due", Label: "续费"},
}

func (s *sqliteNotificationDomain) AdminNotificationChannels(ctx context.Context) ([]AdminNotificationChannel, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, destination, credential, enabled, created_at, updated_at
		FROM notification_channels
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	channels := make([]AdminNotificationChannel, 0)
	for rows.Next() {
		var channel AdminNotificationChannel
		var credential string
		var enabled int
		var createdAt, updatedAt sql.NullInt64
		if err := rows.Scan(&channel.ID, &channel.Name, &channel.Destination, &credential, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		channel.CredentialSet = strings.TrimSpace(credential) != ""
		channel.Enabled = enabled != 0
		channel.CreatedAt = unixStringOr(createdAt, time.Now().UTC())
		channel.UpdatedAt = unixStringOr(updatedAt, time.Now().UTC())
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return channels, nil
}

func (s *sqliteNotificationDomain) CreateAdminNotificationChannel(ctx context.Context, create AdminNotificationChannelCreateRequest) (AdminNotificationChannel, error) {
	if err := create.normalize(); err != nil {
		return AdminNotificationChannel{}, err
	}
	channelID := create.ID
	if channelID == "" {
		generated, err := generatedAdminNodeID(create.Name)
		if err != nil {
			return AdminNotificationChannel{}, err
		}
		channelID = generated
	}
	enabled := 1
	if create.Enabled != nil && !*create.Enabled {
		enabled = 0
	}
	now := time.Now().UTC().Unix()
	credential, err := s.encryptNotificationCredentialForStorage(channelID, "telegram", create.Credential)
	if err != nil {
		return AdminNotificationChannel{}, err
	}
	destinationFingerprint := notificationDestinationFingerprint("telegram", create.Destination)
	result, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO notification_channels (
			id, name, destination, credential, delivery_version,
			destination_fingerprint, enabled, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?)
	`, channelID, create.Name, create.Destination, credential, destinationFingerprint, enabled, now, now)
	if err != nil {
		return AdminNotificationChannel{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return AdminNotificationChannel{}, err
	}
	if affected == 0 {
		return AdminNotificationChannel{}, errNotificationChannelAlreadyExists
	}
	return s.adminNotificationChannelByID(ctx, channelID)
}

func (s *sqliteNotificationDomain) UpdateAdminNotificationChannel(ctx context.Context, channelID string, update AdminNotificationChannelUpdateRequest) (AdminNotificationChannel, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return AdminNotificationChannel{}, errNotificationChannelNotFound
	}
	if err := update.normalize(); err != nil {
		return AdminNotificationChannel{}, err
	}
	var encryptedCredential string
	if update.Credential != nil {
		credential, err := s.encryptNotificationCredentialForStorage(channelID, "telegram", *update.Credential)
		if err != nil {
			return AdminNotificationChannel{}, err
		}
		encryptedCredential = credential
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminNotificationChannel{}, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	currentDestination, currentVersion, err := notificationChannelRoutingStateTx(ctx, tx, channelID)
	if err != nil {
		return AdminNotificationChannel{}, err
	}
	patch := buildNotificationChannelUpdatePatch(update, encryptedCredential, currentDestination, currentVersion)
	if patch.empty() {
		return AdminNotificationChannel{}, errInvalidAdminNotificationChannelWrite
	}
	nowUnix := time.Now().UTC().Unix()
	patch.set("updated_at", nowUnix)
	statement, args := patch.updateStatement("notification_channels", "id = ?", channelID)
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return AdminNotificationChannel{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return AdminNotificationChannel{}, err
	}
	if affected != 1 {
		return AdminNotificationChannel{}, errNotificationChannelNotFound
	}
	if err := tx.Commit(); err != nil {
		return AdminNotificationChannel{}, err
	}
	tx = nil
	return s.adminNotificationChannelByID(ctx, channelID)
}

func notificationChannelRoutingStateTx(ctx context.Context, tx *sql.Tx, channelID string) (string, int64, error) {
	var destination string
	var version int64
	if err := tx.QueryRowContext(ctx, `
		SELECT destination, delivery_version
		FROM notification_channels WHERE id = ?
	`, channelID).Scan(&destination, &version); err != nil {
		if err == sql.ErrNoRows {
			return "", 0, errNotificationChannelNotFound
		}
		return "", 0, err
	}
	if version < 1 {
		version = 1
	}
	return destination, version, nil
}

func buildNotificationChannelUpdatePatch(update AdminNotificationChannelUpdateRequest, encryptedCredential, currentDestination string, currentVersion int64) *sqlPatch {
	patch := newSQLPatch(8)
	patch.addString("name", update.Name)
	patch.addString("destination", update.Destination)
	if update.Credential != nil {
		patch.set("credential", encryptedCredential)
	}
	patch.addBoolInt("enabled", update.Enabled)

	routingChanged := update.Destination != nil || update.Credential != nil
	if routingChanged {
		newDestination := currentDestination
		if update.Destination != nil {
			newDestination = *update.Destination
		}
		// Version and fingerprint move together: the reconcile loop silently
		// re-baselines a channel whose routing binding changed, so a new
		// recipient is never told about an older incident.
		patch.set("delivery_version", currentVersion+1)
		patch.set("destination_fingerprint", notificationDestinationFingerprint("telegram", newDestination))
	}
	return patch
}

func (s *sqliteNotificationDomain) DeleteAdminNotificationChannel(ctx context.Context, channelID string) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return errNotificationChannelNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM notification_states WHERE channel_id = ?`, channelID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM notification_channels WHERE id = ?`, channelID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errNotificationChannelNotFound
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *sqliteNotificationDomain) adminNotificationChannelByID(ctx context.Context, channelID string) (AdminNotificationChannel, error) {
	return adminResourceByID(ctx, channelID, s.AdminNotificationChannels, func(channel AdminNotificationChannel) string { return channel.ID }, errNotificationChannelNotFound)
}

func (s *sqliteNotificationDomain) AdminNotificationDispatchChannel(ctx context.Context, channelID string) (notificationDispatchChannel, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return notificationDispatchChannel{}, errNotificationChannelNotFound
	}
	var channel notificationDispatchChannel
	var storedCredential string
	if err := s.db.QueryRowContext(ctx, `
		SELECT id, name, destination, credential, delivery_version, destination_fingerprint
		FROM notification_channels
		WHERE id = ? AND enabled = 1
	`, channelID).Scan(&channel.ID, &channel.Name, &channel.Destination, &storedCredential,
		&channel.DeliveryVersion, &channel.DestinationFingerprint); err != nil {
		if err == sql.ErrNoRows {
			return notificationDispatchChannel{}, errNotificationChannelNotFound
		}
		return notificationDispatchChannel{}, err
	}
	credential, err := s.decryptNotificationCredentialFromStorage(channel.ID, "telegram", storedCredential)
	if err != nil {
		return notificationDispatchChannel{}, err
	}
	if strings.TrimSpace(credential) == "" {
		return notificationDispatchChannel{}, errInvalidAdminNotificationChannelWrite
	}
	channel.Type = "telegram"
	channel.Credential = credential
	return channel, nil
}

func (s *sqliteNotificationDomain) UpdateAdminNotificationType(ctx context.Context, eventType string, update AdminNotificationTypeUpdateRequest) (AdminNotificationType, error) {
	eventType = strings.TrimSpace(eventType)
	if err := update.normalize(); err != nil {
		return AdminNotificationType{}, err
	}
	label, ok := adminNotificationTypeLabel(eventType)
	if !ok {
		return AdminNotificationType{}, errNotificationTypeNotFound
	}
	if eventType != "node_offline" && eventType != "renewal_due" {
		// Resource warnings share probe_unhealthy but are independently managed
		// alert rules. Pretending this legacy endpoint updated that shared event
		// type would be a successful no-op, so require the alert-rules API.
		return AdminNotificationType{}, errNotificationTypeGone
	}
	enabled := 0
	if *update.Enabled {
		enabled = 1
	}
	now := time.Now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminNotificationType{}, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO notification_types (event_type, enabled, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(event_type) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at
	`, eventType, enabled, now); err != nil {
		return AdminNotificationType{}, err
	}
	// Compatibility endpoint: only event types that have a one-to-one default
	// alert rule (currently node_offline and renewal_due) update that rule. Do
	// not fan out by notification_event_type; resource rules share
	// probe_unhealthy and must remain independently configurable.
	result, err := tx.ExecContext(ctx, `
		UPDATE alert_rules
		SET enabled = ?, updated_at = ?
		WHERE id = ?
	`, enabled, now, eventType)
	if err != nil {
		return AdminNotificationType{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return AdminNotificationType{}, err
	}
	if affected != 1 {
		return AdminNotificationType{}, errNotificationTypeNotFound
	}
	if err := tx.Commit(); err != nil {
		return AdminNotificationType{}, err
	}
	tx = nil
	return AdminNotificationType{EventType: eventType, Label: label, Enabled: *update.Enabled, UpdatedAt: time.Unix(now, 0).UTC().Format(time.RFC3339)}, nil
}

func adminNotificationTypeLabel(eventType string) (string, bool) {
	for _, catalogType := range adminNotificationTypeCatalog {
		if catalogType.EventType == eventType {
			return catalogType.Label, true
		}
	}
	return "", false
}
