package api

import (
	"math"
	"net"
	"net/url"
	"regexp"
	"strings"
)

type AdminSettingsResponse struct {
	Settings SiteSettings `json:"settings"`
}

type AdminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AdminLoginResponse struct {
	Username string `json:"username"`
	Token    string `json:"token,omitempty"`
}

type AdminAccountResponse struct {
	Account AdminAccount `json:"account"`
}

type AdminAccountUpdateRequest struct {
	Username        string `json:"username"`
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type SiteSettings struct {
	SiteTitle            string  `json:"site_title"`
	LogoURL              string  `json:"logo_url"`
	Theme                string  `json:"theme"`
	AgentControllerURL   string  `json:"agent_controller_url"`
	BackgroundURL        string  `json:"background_url"`
	DesktopBackgroundURL string  `json:"desktop_background_url"`
	MobileBackgroundURL  string  `json:"mobile_background_url"`
	AppearancePreset     string  `json:"appearance_preset"`
	ServerCardTheme      string  `json:"server_card_theme"`
	CardOpacity          float64 `json:"card_opacity"`
	CardBlur             float64 `json:"card_blur"`
	CardRadius           float64 `json:"card_radius"`
	BorderStrength       float64 `json:"border_strength"`
	ShadowStrength       float64 `json:"shadow_strength"`
	BackgroundOverlay    float64 `json:"background_overlay"`
	ThemeColor           string  `json:"theme_color"`
	CustomCode           string  `json:"custom_code"`
	Revision             int64   `json:"revision"`
	UpdatedAt            string  `json:"updated_at,omitempty"`
}

type AdminSettingsUpdateRequest struct {
	ExpectedRevision     *int64   `json:"expected_revision,omitempty"`
	SiteTitle            *string  `json:"site_title,omitempty"`
	LogoURL              *string  `json:"logo_url,omitempty"`
	Theme                *string  `json:"theme,omitempty"`
	AgentControllerURL   *string  `json:"agent_controller_url,omitempty"`
	BackgroundURL        *string  `json:"background_url,omitempty"`
	DesktopBackgroundURL *string  `json:"desktop_background_url,omitempty"`
	MobileBackgroundURL  *string  `json:"mobile_background_url,omitempty"`
	AppearancePreset     *string  `json:"appearance_preset,omitempty"`
	ServerCardTheme      *string  `json:"server_card_theme,omitempty"`
	CardOpacity          *float64 `json:"card_opacity,omitempty"`
	CardBlur             *float64 `json:"card_blur,omitempty"`
	CardRadius           *float64 `json:"card_radius,omitempty"`
	BorderStrength       *float64 `json:"border_strength,omitempty"`
	ShadowStrength       *float64 `json:"shadow_strength,omitempty"`
	BackgroundOverlay    *float64 `json:"background_overlay,omitempty"`
	ThemeColor           *string  `json:"theme_color,omitempty"`
	CustomCode           *string  `json:"custom_code,omitempty"`
}

const (
	maxSettingsCustomCodeRunes    = 60000
	defaultCardOpacity            = 0.70
	previousDefaultCardOpacity    = 0.82
	legacyDefaultCardOpacity      = 0.72
	previousDefaultBorderStrength = 0.26
	previousDefaultShadowStrength = 0.22
)

var settingsThemeColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func defaultSiteSettings() SiteSettings {
	return SiteSettings{
		SiteTitle:            "Zeno",
		LogoURL:              "/assets/logo/id.png",
		Theme:                "system",
		AgentControllerURL:   "",
		BackgroundURL:        "",
		DesktopBackgroundURL: "",
		MobileBackgroundURL:  "",
		AppearancePreset:     "default",
		ServerCardTheme:      "classic",
		CardOpacity:          defaultCardOpacity,
		CardBlur:             0,
		CardRadius:           20,
		BorderStrength:       0.30,
		ShadowStrength:       0.20,
		BackgroundOverlay:    0,
		ThemeColor:           "#2563eb",
		CustomCode:           "",
	}
}

func (request *AdminSettingsUpdateRequest) normalize() error {
	if request.ExpectedRevision != nil && *request.ExpectedRevision < 0 {
		return errInvalidAdminSettingsUpdate
	}
	normalizer := newPatchNormalizer(errInvalidAdminSettingsUpdate)
	normalizer.text(&request.SiteTitle, trimRequiredMaxRunes(64))
	normalizer.text(&request.LogoURL, trimOptionalValid(validSettingsAssetURL))
	normalizer.text(&request.Theme, trimLowerValid(validSettingsTheme))
	// A trailing slash would make the stored controller URL differ from the
	// value agents build request paths from.
	normalizer.text(&request.AgentControllerURL, func(value string) (string, bool) {
		trimmed := strings.TrimRight(strings.TrimSpace(value), "/")
		return trimmed, trimmed == "" || validAgentControllerURL(trimmed)
	})
	normalizer.text(&request.BackgroundURL, trimOptionalValid(validSettingsAssetURL))
	normalizer.text(&request.DesktopBackgroundURL, trimOptionalValid(validSettingsAssetURL))
	normalizer.text(&request.MobileBackgroundURL, trimOptionalValid(validSettingsAssetURL))
	normalizer.text(&request.AppearancePreset, trimLowerValid(validAppearancePreset))
	normalizer.text(&request.ServerCardTheme, trimLowerValid(validServerCardTheme))
	normalizer.float(&request.CardOpacity, settingsFloatRange(0.2, 1))
	normalizer.float(&request.CardBlur, settingsFloatRange(0, 40))
	normalizer.float(&request.CardRadius, settingsFloatRange(8, 36))
	normalizer.float(&request.BorderStrength, settingsFloatRange(0, 1))
	normalizer.float(&request.ShadowStrength, settingsFloatRange(0, 1))
	normalizer.float(&request.BackgroundOverlay, settingsFloatRange(0, 0.8))
	normalizer.text(&request.ThemeColor, func(value string) (string, bool) {
		trimmed := strings.TrimSpace(value)
		return trimmed, settingsThemeColorPattern.MatchString(trimmed)
	})
	normalizer.text(&request.CustomCode, trimMaxRunes(maxSettingsCustomCodeRunes))
	return normalizer.result()
}

func validAppearancePreset(value string) bool {
	return value == "default" || value == "gaussian_blur"
}

func validServerCardTheme(value string) bool {
	return value == "classic" || value == "capsule"
}

func validSettingsFloat(value, min, max float64) bool {
	return value >= min && value <= max && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validSettingsTheme(theme string) bool {
	switch theme {
	case "system", "dark", "light":
		return true
	default:
		return false
	}
}

func validSettingsAssetURL(value string) bool {
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		return true
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "https"
}

func validAgentControllerURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (loopbackURLHost(parsed.Hostname()) || directIPURLHost(parsed))) {
		return false
	}
	return parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func directIPURLHost(parsed *url.URL) bool {
	host := strings.TrimSpace(strings.Trim(parsed.Hostname(), "[]"))
	return net.ParseIP(host) != nil && parsed.Port() != ""
}

func loopbackURLHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
