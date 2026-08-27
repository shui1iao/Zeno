package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	settingKeySettingsRevision     = "settings_revision"
	settingKeySiteTitle            = "site_title"
	settingKeyLogoURL              = "logo_url"
	settingKeyTheme                = "theme"
	settingKeyAgentControllerURL   = "agent_controller_url"
	settingKeyBackgroundURL        = "background_url"
	settingKeyDesktopBackgroundURL = "desktop_background_url"
	settingKeyMobileBackgroundURL  = "mobile_background_url"
	settingKeyAppearancePreset     = "appearance_preset"
	settingKeyServerCardTheme      = "server_card_theme"
	settingKeyCardOpacity          = "card_opacity"
	settingKeyCardBlur             = "card_blur"
	settingKeyCardRadius           = "card_radius"
	settingKeyBorderStrength       = "border_strength"
	settingKeyShadowStrength       = "shadow_strength"
	settingKeyBackgroundOverlay    = "background_overlay"
	settingKeyThemeColor           = "theme_color"
	settingKeyCustomCode           = "custom_code"
)

var errAdminSettingsConflict = errors.New("admin settings conflict")

type sqliteSettings struct {
	db *sql.DB
}

func (s *sqliteSettings) PublicSettings(ctx context.Context) (SiteSettings, error) {
	return s.siteSettings(ctx)
}

func (s *sqliteSettings) AdminSettings(ctx context.Context) (SiteSettings, error) {
	return s.siteSettings(ctx)
}

func (s *sqliteSettings) UpdateAdminSettings(ctx context.Context, update AdminSettingsUpdateRequest) (SiteSettings, error) {
	if err := update.normalize(); err != nil {
		return SiteSettings{}, err
	}
	values := adminSettingsUpdateValues(update)
	if len(values) == 0 {
		return s.siteSettings(ctx)
	}
	now := time.Now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SiteSettings{}, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()
	// The first write acquires SQLite's write reservation before the revision is
	// read. Concurrent settings writers therefore serialize before comparing
	// their expected revision instead of both validating the same snapshot.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, '0', ?)
		ON CONFLICT(key) DO NOTHING
	`, settingKeySettingsRevision, now); err != nil {
		return SiteSettings{}, err
	}
	currentRevision, err := settingsRevisionTx(ctx, tx)
	if err != nil {
		return SiteSettings{}, err
	}
	if update.ExpectedRevision != nil && currentRevision != *update.ExpectedRevision {
		return SiteSettings{}, errAdminSettingsConflict
	}
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, key, value, now); err != nil {
			return SiteSettings{}, err
		}
	}
	if currentRevision == int64(^uint64(0)>>1) {
		return SiteSettings{}, fmt.Errorf("settings revision exhausted")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE settings SET value = ?, updated_at = ? WHERE key = ?
	`, strconv.FormatInt(currentRevision+1, 10), now, settingKeySettingsRevision); err != nil {
		return SiteSettings{}, err
	}
	if err := tx.Commit(); err != nil {
		return SiteSettings{}, err
	}
	tx = nil
	// Re-read after commit so the response includes concurrent disjoint updates
	// rather than the stale pre-PATCH snapshot.
	return s.siteSettings(ctx)
}

func settingsRevisionTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	var stored string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingKeySettingsRevision).Scan(&stored); err != nil {
		return 0, err
	}
	revision, err := strconv.ParseInt(strings.TrimSpace(stored), 10, 64)
	if err != nil || revision < 0 {
		return 0, fmt.Errorf("invalid settings revision %q", stored)
	}
	return revision, nil
}

func (s *sqliteSettings) siteSettings(ctx context.Context) (SiteSettings, error) {
	settings := defaultSiteSettings()
	bindings := siteSettingsBindings()
	query, args := siteSettingsQuery(bindings)
	decoder := newSettingsDecoder(bindings)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return SiteSettings{}, err
	}
	defer rows.Close()
	var latest sql.NullInt64
	for rows.Next() {
		var key, value string
		var updatedAt sql.NullInt64
		if err := rows.Scan(&key, &value, &updatedAt); err != nil {
			return SiteSettings{}, err
		}
		decoder.decode(&settings, key, value)
		if updatedAt.Valid && (!latest.Valid || updatedAt.Int64 > latest.Int64) {
			latest = updatedAt
		}
	}
	if err := rows.Err(); err != nil {
		return SiteSettings{}, err
	}
	if settings.DesktopBackgroundURL == "" {
		settings.DesktopBackgroundURL = settings.BackgroundURL
	}
	if settings.BackgroundURL == "" {
		settings.BackgroundURL = settings.DesktopBackgroundURL
	}
	if latest.Valid && latest.Int64 > 0 {
		settings.UpdatedAt = time.Unix(latest.Int64, 0).UTC().Format(time.RFC3339)
	}
	return settings, nil
}

func (s *sqliteSchemaStore) defaultCardOpacityMigrationCurrent(ctx context.Context) (bool, error) {
	legacy, err := s.legacyDefaultAppearanceStored(ctx)
	return !legacy, err
}

func (s *sqliteSchemaStore) migrateDefaultCardOpacity(ctx context.Context) error {
	legacy, err := s.legacyDefaultAppearanceStored(ctx)
	if err != nil || !legacy {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE settings
		SET value = ?, updated_at = ?
		WHERE key = ? AND CAST(value AS REAL) = ?
	`, formatSettingsFloat(previousDefaultCardOpacity), time.Now().UTC().Unix(), settingKeyCardOpacity, legacyDefaultCardOpacity)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("migrate default card opacity: updated %d rows, want 1", rows)
	}
	return nil
}

func (s *sqliteSchemaStore) legacyDefaultAppearanceStored(ctx context.Context) (bool, error) {
	return s.defaultAppearanceValuesStored(ctx, legacyDefaultCardOpacity, previousDefaultBorderStrength, previousDefaultShadowStrength)
}

func (s *sqliteSchemaStore) previousDefaultAppearanceStored(ctx context.Context) (bool, error) {
	return s.defaultAppearanceValuesStored(ctx, previousDefaultCardOpacity, previousDefaultBorderStrength, previousDefaultShadowStrength)
}

func (s *sqliteSchemaStore) defaultAppearanceValuesStored(ctx context.Context, cardOpacity, borderStrength, shadowStrength float64) (bool, error) {
	settings, err := (&sqliteSettings{db: s.db}).siteSettings(ctx)
	if err != nil {
		return false, err
	}
	return settings.AppearancePreset == "default" &&
		settings.CardOpacity == cardOpacity &&
		settings.CardBlur == 0 &&
		settings.CardRadius == 20 &&
		settings.BorderStrength == borderStrength &&
		settings.ShadowStrength == shadowStrength &&
		settings.BackgroundOverlay == 0 &&
		strings.EqualFold(settings.ThemeColor, "#2563eb"), nil
}

func (s *sqliteSchemaStore) defaultAppearanceV3MigrationCurrent(ctx context.Context) (bool, error) {
	previous, err := s.previousDefaultAppearanceStored(ctx)
	return !previous, err
}

func (s *sqliteSchemaStore) migrateDefaultAppearanceV3(ctx context.Context) error {
	previous, err := s.previousDefaultAppearanceStored(ctx)
	if err != nil || !previous {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE settings
		SET value = CASE key
			WHEN ? THEN ?
			WHEN ? THEN ?
			WHEN ? THEN ?
		END,
		updated_at = ?
		WHERE key IN (?, ?, ?)
	`,
		settingKeyCardOpacity, formatSettingsFloat(defaultCardOpacity),
		settingKeyBorderStrength, formatSettingsFloat(defaultSiteSettings().BorderStrength),
		settingKeyShadowStrength, formatSettingsFloat(defaultSiteSettings().ShadowStrength),
		time.Now().UTC().Unix(),
		settingKeyCardOpacity, settingKeyBorderStrength, settingKeyShadowStrength,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 3 {
		return fmt.Errorf("migrate default appearance v3: updated %d rows, want 3", rows)
	}
	return nil
}
