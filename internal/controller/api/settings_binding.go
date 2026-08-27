package api

import "strconv"

// settingsBinding maps one persisted settings key onto the field it populates.
//
// The row-scanning loop previously carried a 17-arm switch that was pure
// key-to-field dispatch rather than real branching, which dominated the
// function's complexity and made it easy to add a key to the SELECT while
// forgetting to decode it. Declaring the bindings once means the query column
// list and the decoder are derived from the same table and cannot drift.
type settingsBinding struct {
	key   string
	apply func(*SiteSettings, string)
}

// assignString stores the value verbatim.
func assignString(field func(*SiteSettings) *string) func(*SiteSettings, string) {
	return func(settings *SiteSettings, value string) {
		*field(settings) = value
	}
}

// assignValidatedString stores the value only when it passes validation, so a
// hand-edited or downgraded row falls back to the default instead of
// propagating an unusable value to the UI.
func assignValidatedString(field func(*SiteSettings) *string, valid func(string) bool) func(*SiteSettings, string) {
	return func(settings *SiteSettings, value string) {
		if valid(value) {
			*field(settings) = value
		}
	}
}

// assignFloat parses the value, keeping the already-populated default when the
// stored text is not a number.
func assignFloat(field func(*SiteSettings) *float64) func(*SiteSettings, string) {
	return func(settings *SiteSettings, value string) {
		target := field(settings)
		*target = parseSettingsFloat(value, *target)
	}
}

func assignNonNegativeInt64(field func(*SiteSettings) *int64) func(*SiteSettings, string) {
	return func(settings *SiteSettings, value string) {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err == nil && parsed >= 0 {
			*field(settings) = parsed
		}
	}
}

// siteSettingsBindings is the single source of truth for which settings keys
// are loaded and how each is decoded.
func siteSettingsBindings() []settingsBinding {
	return []settingsBinding{
		{settingKeySettingsRevision, assignNonNegativeInt64(func(s *SiteSettings) *int64 { return &s.Revision })},
		{settingKeySiteTitle, assignString(func(s *SiteSettings) *string { return &s.SiteTitle })},
		{settingKeyLogoURL, assignString(func(s *SiteSettings) *string { return &s.LogoURL })},
		{settingKeyTheme, assignString(func(s *SiteSettings) *string { return &s.Theme })},
		{settingKeyAgentControllerURL, assignString(func(s *SiteSettings) *string { return &s.AgentControllerURL })},
		{settingKeyBackgroundURL, assignString(func(s *SiteSettings) *string { return &s.BackgroundURL })},
		{settingKeyDesktopBackgroundURL, assignString(func(s *SiteSettings) *string { return &s.DesktopBackgroundURL })},
		{settingKeyMobileBackgroundURL, assignString(func(s *SiteSettings) *string { return &s.MobileBackgroundURL })},
		{settingKeyAppearancePreset, assignValidatedString(
			func(s *SiteSettings) *string { return &s.AppearancePreset }, validAppearancePreset)},
		{settingKeyServerCardTheme, assignValidatedString(
			func(s *SiteSettings) *string { return &s.ServerCardTheme }, validServerCardTheme)},
		{settingKeyCardOpacity, assignFloat(func(s *SiteSettings) *float64 { return &s.CardOpacity })},
		{settingKeyCardBlur, assignFloat(func(s *SiteSettings) *float64 { return &s.CardBlur })},
		{settingKeyCardRadius, assignFloat(func(s *SiteSettings) *float64 { return &s.CardRadius })},
		{settingKeyBorderStrength, assignFloat(func(s *SiteSettings) *float64 { return &s.BorderStrength })},
		{settingKeyShadowStrength, assignFloat(func(s *SiteSettings) *float64 { return &s.ShadowStrength })},
		{settingKeyBackgroundOverlay, assignFloat(func(s *SiteSettings) *float64 { return &s.BackgroundOverlay })},
		{settingKeyThemeColor, assignValidatedString(
			func(s *SiteSettings) *string { return &s.ThemeColor }, settingsThemeColorPattern.MatchString)},
		{settingKeyCustomCode, assignString(func(s *SiteSettings) *string { return &s.CustomCode })},
	}
}

// siteSettingsQuery renders the SELECT and its arguments from the bindings, so
// the placeholder count always matches the number of decoded keys.
func siteSettingsQuery(bindings []settingsBinding) (string, []any) {
	placeholders := make([]byte, 0, len(bindings)*3)
	args := make([]any, 0, len(bindings))
	for index, binding := range bindings {
		if index > 0 {
			placeholders = append(placeholders, ", "...)
		}
		placeholders = append(placeholders, '?')
		args = append(args, binding.key)
	}
	query := `
		SELECT key, value, updated_at
		FROM settings
		WHERE key IN (` + string(placeholders) + `)
	`
	return query, args
}

// settingsDecoder applies stored rows to a SiteSettings value.
type settingsDecoder map[string]func(*SiteSettings, string)

func newSettingsDecoder(bindings []settingsBinding) settingsDecoder {
	decoder := make(settingsDecoder, len(bindings))
	for _, binding := range bindings {
		decoder[binding.key] = binding.apply
	}
	return decoder
}

// decode applies one row, ignoring keys the current build does not know about
// so a downgrade cannot fail on rows written by a newer version.
func (d settingsDecoder) decode(settings *SiteSettings, key, value string) {
	if apply, ok := d[key]; ok {
		apply(settings, value)
	}
}

func formatSettingsFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func parseSettingsFloat(value string, fallback float64) float64 {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
