package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicSettingsDefaultsAndReflectsAdminPatch(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminPasswordHash: testAdminPasswordHash("admin-pass")})

	defaultRecorder := httptest.NewRecorder()
	handler.ServeHTTP(defaultRecorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/settings", nil))
	if defaultRecorder.Code != http.StatusOK {
		t.Fatalf("default public settings status = %d, want 200; body=%s", defaultRecorder.Code, defaultRecorder.Body.String())
	}
	var defaults SiteSettings
	if err := json.NewDecoder(bytes.NewBufferString(defaultRecorder.Body.String())).Decode(&defaults); err != nil {
		t.Fatalf("decode default settings: %v", err)
	}
	if defaults.SiteTitle != "Zeno" || defaults.LogoURL != "/assets/logo/id.png" || defaults.Theme != "system" || defaults.DesktopBackgroundURL != "" || defaults.MobileBackgroundURL != "" || defaults.AppearancePreset != "default" || defaults.CardOpacity != defaultCardOpacity || defaults.CardBlur != 0 || defaults.ThemeColor != "#2563eb" || !strings.Contains(defaultRecorder.Body.String(), `"server_card_theme":"classic"`) {
		t.Fatalf("default settings = %+v, want Zeno defaults", defaults)
	}
	if strings.Contains(defaultRecorder.Body.String(), `"avatar_url"`) {
		t.Fatalf("default settings should use logo_url only, got retired avatar_url field: %s", defaultRecorder.Body.String())
	}
	assertNoSensitiveSettingsLeak(t, defaultRecorder.Body.String())

	patchRecorder := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/settings", bytes.NewBufferString(`{
		"expected_revision": 0,
		"site_title": "  水饺监控  ",
		"logo_url": "/assets/logo/custom.png",
		"theme": "dark",
		"agent_controller_url": "  https://zeno.example.com/  ",
		"background_url": "https://example.com/legacy-bg.webp",
		"desktop_background_url": "https://example.com/desktop-bg.webp",
		"mobile_background_url": "https://example.com/mobile-bg.webp",
		"appearance_preset": "gaussian_blur",
		"server_card_theme": "capsule",
		"card_opacity": 0.58,
		"card_blur": 18,
		"card_radius": 24,
		"border_strength": 0.34,
		"shadow_strength": 0.34,
		"background_overlay": 0.08,
		"theme_color": "#6366f1",
		"custom_code": "  <style>.home-top-card { border-color: #2563eb; }</style><script>window.ZenoCustomLoaded = true;</script>  "
	}`))
	patchRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(patchRecorder, patchRequest)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("patch settings status = %d, want 200; body=%s", patchRecorder.Code, patchRecorder.Body.String())
	}
	assertNoSensitiveSettingsLeak(t, patchRecorder.Body.String())
	var patchResponse struct {
		Settings SiteSettings `json:"settings"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(patchRecorder.Body.String())).Decode(&patchResponse); err != nil {
		t.Fatalf("decode patched settings: %v", err)
	}
	if patchResponse.Settings.SiteTitle != "水饺监控" || patchResponse.Settings.LogoURL != "/assets/logo/custom.png" || patchResponse.Settings.Theme != "dark" || patchResponse.Settings.AgentControllerURL != "https://zeno.example.com" || patchResponse.Settings.BackgroundURL != "https://example.com/desktop-bg.webp" || patchResponse.Settings.DesktopBackgroundURL != "https://example.com/desktop-bg.webp" || patchResponse.Settings.MobileBackgroundURL != "https://example.com/mobile-bg.webp" || patchResponse.Settings.AppearancePreset != "gaussian_blur" || !strings.Contains(patchRecorder.Body.String(), `"server_card_theme":"capsule"`) || patchResponse.Settings.CardOpacity != 0.58 || patchResponse.Settings.CardBlur != 18 || patchResponse.Settings.CardRadius != 24 || patchResponse.Settings.BorderStrength != 0.34 || patchResponse.Settings.ShadowStrength != 0.34 || patchResponse.Settings.BackgroundOverlay != 0.08 || patchResponse.Settings.ThemeColor != "#6366f1" || patchResponse.Settings.CustomCode != "<style>.home-top-card { border-color: #2563eb; }</style><script>window.ZenoCustomLoaded = true;</script>" {
		t.Fatalf("patched settings = %+v, want trimmed persisted settings", patchResponse.Settings)
	}
	if strings.Contains(patchRecorder.Body.String(), `"avatar_url"`) || strings.Contains(patchRecorder.Body.String(), `"site_subtitle"`) {
		t.Fatalf("patched settings should not expose retired settings fields: %s", patchRecorder.Body.String())
	}

	publicRecorder := httptest.NewRecorder()
	handler.ServeHTTP(publicRecorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/settings", nil))
	if publicRecorder.Code != http.StatusOK {
		t.Fatalf("public settings after patch status = %d, want 200; body=%s", publicRecorder.Code, publicRecorder.Body.String())
	}
	if !strings.Contains(publicRecorder.Body.String(), `"site_title":"水饺监控"`) || !strings.Contains(publicRecorder.Body.String(), `"logo_url":"/assets/logo/custom.png"`) || !strings.Contains(publicRecorder.Body.String(), `"agent_controller_url":"https://zeno.example.com"`) || !strings.Contains(publicRecorder.Body.String(), `"desktop_background_url":"https://example.com/desktop-bg.webp"`) || !strings.Contains(publicRecorder.Body.String(), `"mobile_background_url":"https://example.com/mobile-bg.webp"`) || !strings.Contains(publicRecorder.Body.String(), `"appearance_preset":"gaussian_blur"`) || !strings.Contains(publicRecorder.Body.String(), `"server_card_theme":"capsule"`) || !strings.Contains(publicRecorder.Body.String(), `"card_blur":18`) || !strings.Contains(publicRecorder.Body.String(), `"theme_color":"#6366f1"`) || !strings.Contains(publicRecorder.Body.String(), `"custom_code":"\u003cstyle\u003e.home-top-card { border-color: #2563eb; }\u003c/style\u003e\u003cscript\u003ewindow.ZenoCustomLoaded = true;\u003c/script\u003e"`) {
		t.Fatalf("public settings after patch did not reflect admin update: %s", publicRecorder.Body.String())
	}
	if strings.Contains(publicRecorder.Body.String(), `"avatar_url"`) {
		t.Fatalf("public settings should not expose retired avatar_url field: %s", publicRecorder.Body.String())
	}
}

func TestPublicSettingsInvalidServerCardThemeFallsBackToClassic(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, ?, 1)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, settingKeyServerCardTheme, "rack"); err != nil {
		t.Fatalf("seed invalid server card theme: %v", err)
	}
	settings, err := store.PublicSettings(context.Background())
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if settings.ServerCardTheme != "classic" {
		t.Fatalf("invalid stored server card theme = %q, want classic", settings.ServerCardTheme)
	}
}

func TestLegacyDefaultCardOpacityMigratesOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zeno.db")
	store, err := OpenSQLiteStore(path)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	for key, value := range map[string]string{
		settingKeyCardOpacity:    "0.72",
		settingKeyBorderStrength: "0.26",
		settingKeyShadowStrength: "0.22",
	} {
		if _, err := store.db.Exec(`
			INSERT INTO settings (key, value, updated_at)
			VALUES (?, ?, 1)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, key, value); err != nil {
			store.Close()
			t.Fatalf("seed legacy appearance %s: %v", key, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	store, err = OpenSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer store.Close()
	settings, err := store.PublicSettings(context.Background())
	if err != nil {
		t.Fatalf("public settings after migration: %v", err)
	}
	if settings.CardOpacity != defaultCardOpacity {
		t.Fatalf("card opacity after migration = %.2f, want %.2f", settings.CardOpacity, defaultCardOpacity)
	}
	if settings.BorderStrength != 0.30 || settings.ShadowStrength != 0.20 {
		t.Fatalf("appearance strengths after migration = border %.2f shadow %.2f, want 0.30/0.20", settings.BorderStrength, settings.ShadowStrength)
	}
	var marker int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = '20260803_default_card_opacity_v2'`).Scan(&marker); err != nil {
		t.Fatalf("read opacity migration marker: %v", err)
	}
	if marker != 1 {
		t.Fatalf("opacity migration markers = %d, want 1", marker)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = '20260807_default_appearance_v3'`).Scan(&marker); err != nil {
		t.Fatalf("read appearance v3 migration marker: %v", err)
	}
	if marker != 1 {
		t.Fatalf("appearance v3 migration markers = %d, want 1", marker)
	}
}

func TestPreviousDefaultAppearanceMigratesToCurrentDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zeno.db")
	store, err := OpenSQLiteStore(path)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	for key, value := range map[string]string{
		settingKeyCardOpacity:    "0.82",
		settingKeyBorderStrength: "0.26",
		settingKeyShadowStrength: "0.22",
	} {
		if _, err := store.db.Exec(`
			INSERT INTO settings (key, value, updated_at)
			VALUES (?, ?, 1)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, key, value); err != nil {
			store.Close()
			t.Fatalf("seed previous appearance %s: %v", key, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close previous-default store: %v", err)
	}

	store, err = OpenSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen previous-default store: %v", err)
	}
	defer store.Close()
	settings, err := store.PublicSettings(context.Background())
	if err != nil {
		t.Fatalf("public settings after previous-default migration: %v", err)
	}
	if settings.CardOpacity != 0.70 || settings.BorderStrength != 0.30 || settings.ShadowStrength != 0.20 {
		t.Fatalf("appearance after migration = opacity %.2f border %.2f shadow %.2f, want 0.70/0.30/0.20", settings.CardOpacity, settings.BorderStrength, settings.ShadowStrength)
	}
}

func TestRetiredSiteSubtitleIsPrunedOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zeno.db")
	store, err := OpenSQLiteStore(path)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('site_subtitle', 'legacy subtitle', 1)`); err != nil {
		store.Close()
		t.Fatalf("seed retired site subtitle: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	store, err = OpenSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer store.Close()
	var retiredRows int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'site_subtitle'`).Scan(&retiredRows); err != nil {
		t.Fatalf("read retired setting count: %v", err)
	}
	if retiredRows != 0 {
		t.Fatalf("retired rows=%d, want 0", retiredRows)
	}
}

func TestLegacyCardOpacityIsPreservedForCustomizedAppearance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zeno.db")
	store, err := OpenSQLiteStore(path)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	for key, value := range map[string]string{
		settingKeyCardOpacity: "0.72",
		settingKeyThemeColor:  "#123456",
	} {
		if _, err := store.db.Exec(`
			INSERT INTO settings (key, value, updated_at)
			VALUES (?, ?, 1)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, key, value); err != nil {
			store.Close()
			t.Fatalf("seed customized appearance %s: %v", key, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close customized store: %v", err)
	}

	store, err = OpenSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen customized store: %v", err)
	}
	defer store.Close()
	settings, err := store.PublicSettings(context.Background())
	if err != nil {
		t.Fatalf("public settings after reopen: %v", err)
	}
	if settings.CardOpacity != legacyDefaultCardOpacity {
		t.Fatalf("customized card opacity after reopen = %.2f, want %.2f", settings.CardOpacity, legacyDefaultCardOpacity)
	}
}

func TestAdminSettingsRequiresTokenAndRejectsInvalidValues(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminPasswordHash: testAdminPasswordHash("admin-pass")})

	unauthRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unauthRecorder, httptest.NewRequest(http.MethodGet, "/api/admin/v1/settings", nil))
	if unauthRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauth settings status = %d, want 401; body=%s", unauthRecorder.Code, unauthRecorder.Body.String())
	}
	assertNoSensitiveSettingsLeak(t, unauthRecorder.Body.String())

	cases := []struct {
		name string
		body string
	}{
		{name: "blank site title", body: `{"site_title":"   "}`},
		{name: "unsupported theme", body: `{"theme":"neon"}`},
		{name: "javascript logo", body: `{"logo_url":"javascript:alert(1)"}`},
		{name: "retired avatar field", body: `{"avatar_url":"/assets/avatar/custom.webp"}`},
		{name: "agent controller URL with credentials", body: `{"agent_controller_url":"https://user:pass@example.com"}`},
		{name: "agent controller URL with query", body: `{"agent_controller_url":"https://example.com/?token=1"}`},
		{name: "remote agent controller URL over HTTP", body: `{"agent_controller_url":"http://example.com"}`},
		{name: "agent controller URL unsupported scheme", body: `{"agent_controller_url":"javascript:alert(1)"}`},
		{name: "javascript background", body: `{"background_url":"data:text/html,<script>alert(1)</script>"}`},
		{name: "javascript desktop background", body: `{"desktop_background_url":"data:text/html,<script>alert(1)</script>"}`},
		{name: "javascript mobile background", body: `{"mobile_background_url":"//evil.example/bg.webp"}`},
		{name: "unsupported appearance preset", body: `{"appearance_preset":"neon"}`},
		{name: "unsupported server card theme", body: `{"server_card_theme":"rack"}`},
		{name: "too low opacity", body: `{"card_opacity":0.1}`},
		{name: "too high blur", body: `{"card_blur":41}`},
		{name: "too low radius", body: `{"card_radius":7}`},
		{name: "too high border", body: `{"border_strength":1.1}`},
		{name: "too high shadow", body: `{"shadow_strength":1.1}`},
		{name: "too high overlay", body: `{"background_overlay":0.9}`},
		{name: "invalid theme color", body: `{"theme_color":"blue"}`},
		{name: "oversized custom code", body: `{"custom_code":"` + strings.Repeat("a", maxSettingsCustomCodeRunes+1) + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			body := `{"expected_revision":0,` + strings.TrimPrefix(tc.body, "{")
			request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/settings", bytes.NewBufferString(body))
			request.Header.Set("X-Admin-Token", "admin-pass")
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			assertNoSensitiveSettingsLeak(t, recorder.Body.String())
		})
	}

	settings, err := store.PublicSettings(context.Background())
	if err != nil {
		t.Fatalf("public settings after invalid patches: %v", err)
	}
	if settings.SiteTitle != "Zeno" || settings.Theme != "system" {
		t.Fatalf("invalid patches should not mutate defaults, got %+v", settings)
	}
}

func TestAdminSettingsAllowsBlankLogoURL(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminPasswordHash: testAdminPasswordHash("admin-pass")})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/settings", strings.NewReader(`{"expected_revision":0,"logo_url":"   "}`))
	request.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("blank logo patch status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Settings SiteSettings `json:"settings"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode blank logo response: %v", err)
	}
	if response.Settings.LogoURL != "" {
		t.Fatalf("blank logo URL = %q, want empty", response.Settings.LogoURL)
	}

	settings, err := store.PublicSettings(context.Background())
	if err != nil {
		t.Fatalf("public settings after blank logo patch: %v", err)
	}
	if settings.LogoURL != "" {
		t.Fatalf("persisted blank logo URL = %q, want empty", settings.LogoURL)
	}
}

func TestAdminSettingsRevisionRejectsStaleWritesWithoutPartialChanges(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	initial, err := store.AdminSettings(ctx)
	if err != nil {
		t.Fatalf("read initial settings: %v", err)
	}
	if initial.Revision != 0 {
		t.Fatalf("initial revision = %d, want 0", initial.Revision)
	}

	expectedZero := int64(0)
	title := "First writer"
	first, err := store.UpdateAdminSettings(ctx, AdminSettingsUpdateRequest{ExpectedRevision: &expectedZero, SiteTitle: &title})
	if err != nil {
		t.Fatalf("first settings update: %v", err)
	}
	if first.Revision != 1 || first.SiteTitle != title {
		t.Fatalf("first settings = %+v, want revision 1 and updated title", first)
	}

	logo := "/assets/logo/stale.png"
	if _, err := store.UpdateAdminSettings(ctx, AdminSettingsUpdateRequest{ExpectedRevision: &expectedZero, LogoURL: &logo}); !errors.Is(err, errAdminSettingsConflict) {
		t.Fatalf("stale settings update error = %v, want conflict", err)
	}
	afterConflict, err := store.AdminSettings(ctx)
	if err != nil {
		t.Fatalf("read settings after conflict: %v", err)
	}
	if afterConflict.Revision != 1 || afterConflict.SiteTitle != title || afterConflict.LogoURL == logo {
		t.Fatalf("settings after conflict = %+v, want first update preserved without stale logo", afterConflict)
	}

	expectedOne := int64(1)
	second, err := store.UpdateAdminSettings(ctx, AdminSettingsUpdateRequest{ExpectedRevision: &expectedOne, LogoURL: &logo})
	if err != nil {
		t.Fatalf("second settings update: %v", err)
	}
	if second.Revision != 2 || second.LogoURL != logo {
		t.Fatalf("second settings = %+v, want revision 2 and updated logo", second)
	}
}

func TestAdminSettingsPatchRequiresExpectedRevisionAndReturnsConflict(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminPasswordHash: testAdminPasswordHash("admin-pass")})

	missing := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/settings", strings.NewReader(`{"site_title":"missing revision"}`))
	missingRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(missing, missingRequest)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing revision status = %d, want 400; body=%s", missing.Code, missing.Body.String())
	}

	first := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/settings", strings.NewReader(`{"expected_revision":0,"site_title":"first"}`))
	firstRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(first, firstRequest)
	if first.Code != http.StatusOK {
		t.Fatalf("first revision status = %d, want 200; body=%s", first.Code, first.Body.String())
	}

	stale := httptest.NewRecorder()
	staleRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/settings", strings.NewReader(`{"expected_revision":0,"logo_url":"/assets/logo/stale.png"}`))
	staleRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(stale, staleRequest)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "settings changed") {
		t.Fatalf("stale revision status = %d, want 409; body=%s", stale.Code, stale.Body.String())
	}
}

func TestValidAgentControllerURLRequiresHTTPSOutsideLoopback(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{url: "https://zeno.example.com", want: true},
		{url: "http://127.0.0.1:18980", want: true},
		{url: "http://[::1]:18980", want: true},
		{url: "http://localhost:18980", want: true},
		{url: "http://203.0.113.10:18980", want: true},
		{url: "http://[2001:db8::10]:18980", want: true},
		{url: "http://203.0.113.10", want: false},
		{url: "http://zeno.example.com", want: false},
		{url: "https://user:pass@zeno.example.com", want: false},
		{url: "https://zeno.example.com/?token=secret", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			if got := validAgentControllerURL(tc.url); got != tc.want {
				t.Fatalf("validAgentControllerURL(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

func assertNoSensitiveSettingsLeak(t *testing.T, raw string) {
	t.Helper()
	lower := bytes.ToLower([]byte(raw))
	for _, word := range [][]byte{[]byte("token"), []byte("secret"), []byte("credential"), []byte("hash")} {
		if bytes.Contains(lower, word) {
			t.Fatalf("settings response leaked sensitive wording: %s", raw)
		}
	}
}
