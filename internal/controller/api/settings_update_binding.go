package api

// adminSettingsUpdateValues converts a sparse settings PATCH into the exact set
// of setting keys to upsert.
//
// PATCH is deliberately sparse: persisting a read/modify/write snapshot of every
// setting would let two disjoint concurrent requests overwrite each other with
// stale values. Only keys represented by this request are returned.
//
// The mapping is a table because it was 19 near-identical nil checks, which is
// where a field can silently go missing: a new setting that is added to the
// request type but not here would be accepted by the API and never persisted.
func adminSettingsUpdateValues(update AdminSettingsUpdateRequest) map[string]string {
	values := make(map[string]string)

	for _, binding := range adminSettingsTextBindings(update) {
		if binding.value != nil {
			values[binding.key] = *binding.value
		}
	}
	for _, binding := range adminSettingsFloatBindings(update) {
		if binding.value != nil {
			values[binding.key] = formatSettingsFloat(*binding.value)
		}
	}

	applyAdminSettingsBackgroundAliases(update, values)
	return values
}

// adminSettingsTextBinding pairs a settings key with the request field that
// carries it verbatim.
type adminSettingsTextBinding struct {
	key   string
	value *string
}

// adminSettingsFloatBinding pairs a settings key with a numeric request field.
// These are formatted through formatSettingsFloat so stored values keep a stable
// representation instead of varying with Go's default float formatting.
type adminSettingsFloatBinding struct {
	key   string
	value *float64
}

func adminSettingsTextBindings(update AdminSettingsUpdateRequest) []adminSettingsTextBinding {
	return []adminSettingsTextBinding{
		{settingKeySiteTitle, update.SiteTitle},
		{settingKeyLogoURL, update.LogoURL},
		{settingKeyTheme, update.Theme},
		{settingKeyAgentControllerURL, update.AgentControllerURL},
		{settingKeyMobileBackgroundURL, update.MobileBackgroundURL},
		{settingKeyAppearancePreset, update.AppearancePreset},
		{settingKeyServerCardTheme, update.ServerCardTheme},
		{settingKeyThemeColor, update.ThemeColor},
		{settingKeyCustomCode, update.CustomCode},
	}
}

func adminSettingsFloatBindings(update AdminSettingsUpdateRequest) []adminSettingsFloatBinding {
	return []adminSettingsFloatBinding{
		{settingKeyCardOpacity, update.CardOpacity},
		{settingKeyCardBlur, update.CardBlur},
		{settingKeyCardRadius, update.CardRadius},
		{settingKeyBorderStrength, update.BorderStrength},
		{settingKeyShadowStrength, update.ShadowStrength},
		{settingKeyBackgroundOverlay, update.BackgroundOverlay},
	}
}

// applyAdminSettingsBackgroundAliases keeps the legacy desktop background alias
// in sync.
//
// background_url predates the split into desktop and mobile backgrounds, so the
// two keys must agree or older clients would read a stale image. An explicit
// desktop value wins over the legacy field when both are present, and no
// unrelated setting is touched.
func applyAdminSettingsBackgroundAliases(update AdminSettingsUpdateRequest, values map[string]string) {
	if update.BackgroundURL != nil {
		values[settingKeyBackgroundURL] = *update.BackgroundURL
		if update.DesktopBackgroundURL == nil {
			values[settingKeyDesktopBackgroundURL] = *update.BackgroundURL
		}
	}
	if update.DesktopBackgroundURL != nil {
		values[settingKeyDesktopBackgroundURL] = *update.DesktopBackgroundURL
		values[settingKeyBackgroundURL] = *update.DesktopBackgroundURL
	}
}
