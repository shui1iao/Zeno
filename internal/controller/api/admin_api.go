package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type adminStore interface {
	AdminSettings(ctx context.Context) (SiteSettings, error)
	UpdateAdminSettings(ctx context.Context, update AdminSettingsUpdateRequest) (SiteSettings, error)
	AdminNodes(ctx context.Context) ([]AdminNode, error)
	AdminProbeTargets(ctx context.Context) ([]AdminProbeTarget, error)
	AdminNotificationChannels(ctx context.Context) ([]AdminNotificationChannel, error)
	AdminAlertRules(ctx context.Context) ([]AdminAlertRule, error)
	AdminNotificationDispatchChannel(ctx context.Context, channelID string) (notificationDispatchChannel, error)
	CreateAdminNode(ctx context.Context, create AdminNodeCreateRequest) (AdminNode, error)
	UpdateAdminNode(ctx context.Context, nodeID string, update AdminNodeUpdateRequest) (AdminNode, error)
	DeleteAdminNode(ctx context.Context, nodeID string) error
	AdminNodeInstallCommand(ctx context.Context, nodeID, controllerURL, agentVersion string) (AgentInstallCommands, error)
	CreateAdminProbeTarget(ctx context.Context, create AdminProbeTargetCreateRequest) (AdminProbeTarget, error)
	UpdateAdminProbeTarget(ctx context.Context, targetID string, update AdminProbeTargetUpdateRequest) (AdminProbeTarget, error)
	DeleteAdminProbeTarget(ctx context.Context, targetID string) error
	CreateAdminNotificationChannel(ctx context.Context, create AdminNotificationChannelCreateRequest) (AdminNotificationChannel, error)
	UpdateAdminNotificationChannel(ctx context.Context, channelID string, update AdminNotificationChannelUpdateRequest) (AdminNotificationChannel, error)
	DeleteAdminNotificationChannel(ctx context.Context, channelID string) error
	UpdateAdminNotificationType(ctx context.Context, eventType string, update AdminNotificationTypeUpdateRequest) (AdminNotificationType, error)
	UpdateAdminAlertRule(ctx context.Context, ruleID string, update AdminAlertRuleUpdateRequest) (AdminAlertRule, error)
}

type adminAuthStore interface {
	AdminLogin(ctx context.Context, username, password, fallbackHash string) (AdminSession, error)
	AuthorizeAdminSession(ctx context.Context, token string) (bool, error)
	RevokeAdminSession(ctx context.Context, token string) error
	AdminAccount(ctx context.Context) (AdminAccount, error)
	UpdateAdminAccount(ctx context.Context, username, currentPassword, newPassword, fallbackHash string) (AdminSession, error)
	AdminAccountConfigured(ctx context.Context) (bool, error)
}

type adminNodeOrderStore interface {
	ReorderAdminNodes(ctx context.Context, request AdminNodeReorderRequest) error
}

type adminProbeTargetOrderStore interface {
	ReorderAdminProbeTargets(ctx context.Context, request AdminProbeTargetReorderRequest) error
}

const (
	adminSessionCookieName = "__Host-zeno_admin_session"
	adminCSRFHeaderName    = "X-Zeno-CSRF"
	adminCSRFHeaderValue   = "1"
)

func (h *handler) browserAdminRequest(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get(adminCSRFHeaderName)) == adminCSRFHeaderValue
}

func (h *handler) sameOriginRequest(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return strings.EqualFold(parsed.Scheme, h.requestProto(r)) && strings.EqualFold(parsed.Host, r.Host)
}

func (h *handler) allowBrowserAdminMutation(r *http.Request) bool {
	return h.requestUsesHTTPS(r) && h.browserAdminRequest(r) && h.sameOriginRequest(r)
}

func setAdminSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: adminSessionCookieName, Value: token, Path: "/", MaxAge: int(adminSessionAbsoluteTimeout.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func clearAdminSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: adminSessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func adminRequestToken(r *http.Request) (string, bool) {
	if token := strings.TrimSpace(r.Header.Get("X-Admin-Token")); token != "" {
		return token, false
	}
	cookie, err := r.Cookie(adminSessionCookieName)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(cookie.Value), true
}

func (h *handler) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.adminPasswordHash == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		writeError(w, http.StatusForbidden, "cross-site login rejected")
		return
	}
	browserRequest := h.browserAdminRequest(r)
	if browserRequest && !h.allowBrowserAdminMutation(r) {
		writeError(w, http.StatusForbidden, "cross-site or insecure login rejected")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "application/json required")
		return
	}
	var request AdminLoginRequest
	if !decodeJSONBody(w, r, &request, adminJSONBodyLimit, true) {
		return
	}
	releaseArgon2, admitted := reserveAdminArgon2Request()
	if !admitted {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	defer releaseArgon2()
	accountKey := h.adminLoginRateLimitKey(r, request.Username)
	ipKey := h.adminLoginIPRateLimitKey(r)
	accountReservation := adminLoginReservation{}
	ipReservation := adminLoginReservation{}
	if h.loginLimiter != nil {
		var reserved bool
		ipReservation, reserved = h.loginLimiter.reserve(ipKey)
		if !reserved {
			writeError(w, http.StatusTooManyRequests, "too many attempts")
			return
		}
		accountReservation, reserved = h.loginLimiter.reserve(accountKey)
		if !reserved {
			// The account key may already be locked while the per-IP reservation
			// above succeeded. This request never reaches password verification, so
			// do not consume an unrelated IP failure slot.
			ipReservation.cancel()
			writeError(w, http.StatusTooManyRequests, "too many attempts")
			return
		}
	}
	loginSucceeded := false
	loginAttemptCounted := true
	defer func() {
		if loginAttemptCounted {
			accountReservation.release(loginSucceeded)
			ipReservation.release(loginSucceeded)
			return
		}
		accountReservation.cancel()
		ipReservation.cancel()
	}()
	if authStore, ok := h.store.(adminAuthStore); ok {
		session, err := authStore.AdminLogin(r.Context(), request.Username, request.Password, h.adminPasswordHash)
		if err != nil {
			if errors.Is(err, errInvalidAdminLogin) {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			loginAttemptCounted = false
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		loginSucceeded = true
		if browserRequest {
			setAdminSessionCookie(w, session.Token)
			writeJSON(w, http.StatusOK, AdminLoginResponse{Username: session.Username})
		} else {
			writeJSON(w, http.StatusOK, AdminLoginResponse(session))
		}
		return
	}
	passwordOK := adminPasswordMatches("", h.adminPasswordHash, request.Password)
	if strings.TrimSpace(request.Username) != "admin" || !passwordOK {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	loginSucceeded = true
	if browserRequest {
		setAdminSessionCookie(w, strings.TrimSpace(request.Password))
		writeJSON(w, http.StatusOK, AdminLoginResponse{Username: "admin"})
	} else {
		writeJSON(w, http.StatusOK, AdminLoginResponse{Username: "admin", Token: strings.TrimSpace(request.Password)})
	}
}

func (h *handler) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	token, cookieAuth := adminRequestToken(r)
	if cookieAuth && !h.allowBrowserAdminMutation(r) {
		writeError(w, http.StatusForbidden, "cross-site request rejected")
		return
	}
	if authStore, ok := h.store.(adminAuthStore); ok {
		if err := authStore.RevokeAdminSession(r.Context(), token); err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	if cookieAuth {
		clearAdminSessionCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleAdminAccount(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorizeAdminRequest(w, r); !ok {
		return
	}
	authStore, ok := h.store.(adminAuthStore)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		account, err := authStore.AdminAccount(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, AdminAccountResponse{Account: account})
	case http.MethodPost:
		var request AdminAccountUpdateRequest
		if !decodeJSONBody(w, r, &request, adminJSONBodyLimit, true) {
			return
		}
		releaseArgon2, admitted := reserveAdminArgon2Request()
		if !admitted {
			writeError(w, http.StatusTooManyRequests, "too many attempts")
			return
		}
		defer releaseArgon2()
		session, err := authStore.UpdateAdminAccount(r.Context(), request.Username, request.CurrentPassword, request.NewPassword, h.adminPasswordHash)
		if err != nil {
			writeAdminError(w, err)
			return
		}
		_, cookieAuth := adminRequestToken(r)
		if cookieAuth {
			setAdminSessionCookie(w, session.Token)
			writeJSON(w, http.StatusOK, AdminLoginResponse{Username: session.Username})
		} else {
			writeJSON(w, http.StatusOK, AdminLoginResponse(session))
		}
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *handler) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	handleAdminReadMutation(h, w, r, http.MethodPatch, http.StatusOK, readAdminSettings, updateAdminSettings)
}

func readAdminSettings(ctx context.Context, store adminStore) (AdminSettingsResponse, error) {
	settings, err := store.AdminSettings(ctx)
	return AdminSettingsResponse{Settings: settings}, err
}

func updateAdminSettings(ctx context.Context, store adminStore, update AdminSettingsUpdateRequest) (AdminSettingsResponse, error) {
	if update.ExpectedRevision == nil {
		return AdminSettingsResponse{}, errInvalidAdminSettingsUpdate
	}
	settings, err := store.UpdateAdminSettings(ctx, update)
	return AdminSettingsResponse{Settings: settings}, err
}

func handleAdminReadMutation[Request, ReadResponse, MutationResponse any](
	h *handler, w http.ResponseWriter, r *http.Request, mutationMethod string, mutationStatus int,
	read func(context.Context, adminStore) (ReadResponse, error),
	mutate func(context.Context, adminStore, Request) (MutationResponse, error),
) {
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		response, err := read(r.Context(), store)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, response)
	case mutationMethod:
		var request Request
		if !decodeJSONBody(w, r, &request, adminJSONBodyLimit, true) {
			return
		}
		response, err := mutate(r.Context(), store, request)
		if err != nil {
			writeAdminError(w, err)
			return
		}
		writeJSON(w, mutationStatus, response)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleAdminPatchResource[Request, Response any](
	h *handler, w http.ResponseWriter, r *http.Request, pathPrefix string,
	update func(context.Context, adminStore, string, Request) (Response, error),
) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, pathPrefix), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	var request Request
	if !decodeJSONBody(w, r, &request, adminJSONBodyLimit, true) {
		return
	}
	response, err := update(r.Context(), store, parts[0], request)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) handleAdminProbeTargets(w http.ResponseWriter, r *http.Request) {
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		targets, err := store.AdminProbeTargets(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, AdminProbeTargetsResponse{Targets: targets})
	case http.MethodPost:
		var create AdminProbeTargetCreateRequest
		if !decodeJSONBody(w, r, &create, adminJSONBodyLimit, true) {
			return
		}
		target, err := store.CreateAdminProbeTarget(r.Context(), create)
		if err != nil {
			writeAdminError(w, err)
			return
		}
		h.notifyProbeConfigChanged(r.Context())
		h.publishSummaryNowFresh(r.Context())
		writeJSON(w, http.StatusCreated, AdminProbeTargetResponse{Target: target})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *handler) handleAdminProbeTargetResource(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/v1/probe-targets/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		if err := store.DeleteAdminProbeTarget(r.Context(), parts[0]); err != nil {
			writeAdminError(w, err)
			return
		}
		h.notifyProbeConfigChanged(r.Context())
		h.publishSummaryNowFresh(r.Context())
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var update AdminProbeTargetUpdateRequest
	if !decodeJSONBody(w, r, &update, adminJSONBodyLimit, true) {
		return
	}
	target, err := store.UpdateAdminProbeTarget(r.Context(), parts[0], update)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	h.notifyProbeConfigChanged(r.Context())
	h.publishSummaryNowFresh(r.Context())
	writeJSON(w, http.StatusOK, AdminProbeTargetResponse{Target: target})
}

func (h *handler) handleAdminProbeTargetReorder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := h.authorizeAdminRequest(w, r); !ok {
		return
	}
	store, ok := h.store.(adminProbeTargetOrderStore)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var request AdminProbeTargetReorderRequest
	if !decodeJSONBody(w, r, &request, adminJSONBodyLimit, true) {
		return
	}
	if err := store.ReorderAdminProbeTargets(r.Context(), request); err != nil {
		writeAdminError(w, err)
		return
	}
	h.notifyProbeConfigChanged(r.Context())
	h.publishSummaryNowFresh(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleAdminNodes(w http.ResponseWriter, r *http.Request) {
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		nodes, err := store.AdminNodes(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, AdminNodesResponse{Nodes: nodes})
	case http.MethodPost:
		var create AdminNodeCreateRequest
		if !decodeJSONBody(w, r, &create, adminJSONBodyLimit, true) {
			return
		}
		node, err := store.CreateAdminNode(r.Context(), create)
		if err != nil {
			writeAdminError(w, err)
			return
		}
		h.publishSummaryNowFresh(r.Context())
		writeJSON(w, http.StatusCreated, AdminNodeResponse{Node: node})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *handler) handleAdminNodeResource(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/v1/nodes/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "install-command" {
		h.handleAdminNodeInstallCommand(w, r, parts[0])
		return
	}
	if len(parts) != 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	nodeID := parts[0]
	if r.Method == http.MethodDelete {
		if err := store.DeleteAdminNode(r.Context(), nodeID); err != nil {
			writeAdminError(w, err)
			return
		}
		h.publishSummaryNowFresh(r.Context())
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var update AdminNodeUpdateRequest
	if !decodeJSONBody(w, r, &update, adminJSONBodyLimit, true) {
		return
	}
	node, err := store.UpdateAdminNode(r.Context(), nodeID, update)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	if update.ProbeTargetIDs != nil {
		h.notifyProbeConfigChanged(r.Context())
	}
	h.publishSummaryNowFresh(r.Context())
	writeJSON(w, http.StatusOK, AdminNodeResponse{Node: node})
}

func (h *handler) handleAdminNodeReorder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := h.authorizeAdminRequest(w, r); !ok {
		return
	}
	store, ok := h.store.(adminNodeOrderStore)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var request AdminNodeReorderRequest
	if !decodeJSONBody(w, r, &request, adminJSONBodyLimit, true) {
		return
	}
	if err := store.ReorderAdminNodes(r.Context(), request); err != nil {
		writeAdminError(w, err)
		return
	}
	h.publishSummaryNowFresh(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleAdminNodeInstallCommand(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	nodes, err := store.AdminNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	found := false
	for _, node := range nodes {
		if node.ID == nodeID {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	settings, err := store.AdminSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	controllerURL := strings.TrimSpace(settings.AgentControllerURL)
	if controllerURL != "" && !validAgentControllerURL(controllerURL) {
		writeError(w, http.StatusConflict, "configure a secure agent controller url before generating install commands")
		return
	}
	if controllerURL == "" {
		var input AdminNodeInstallCommandRequest
		if r.Body != nil && r.ContentLength != 0 {
			if !decodeJSONBody(w, r, &input, adminJSONBodyLimit, true) {
				return
			}
		}
		fallbackURL := strings.TrimRight(strings.TrimSpace(input.ControllerURL), "/")
		if fallbackURL != "" {
			if !validAgentControllerURL(fallbackURL) {
				writeError(w, http.StatusConflict, "current admin url cannot be used as the agent controller url")
				return
			}
		} else {
			fallbackURL = h.requestBaseURL(r)
			parsedFallback, parseErr := url.Parse(fallbackURL)
			if parseErr != nil || (!loopbackURLHost(parsedFallback.Hostname()) && !directIPURLHost(parsedFallback)) || !validAgentControllerURL(fallbackURL) {
				writeError(w, http.StatusConflict, "configure agent controller url before generating install commands")
				return
			}
		}
		controllerURL = fallbackURL
	}
	commands, err := store.AdminNodeInstallCommand(r.Context(), nodeID, controllerURL, h.agentVersion)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, AdminNodeInstallCommandResponse{
		NodeID:                  nodeID,
		Command:                 commands.Linux,
		Commands:                commands.Map(),
		EnrollmentExpiresAt:     commands.EnrollmentExpiresAt.UTC().Format(time.RFC3339),
		EnrollmentOneTime:       true,
		SupersedesPreviousToken: true,
	})
}

func (h *handler) authorizeAdminRequest(w http.ResponseWriter, r *http.Request) (adminStore, bool) {
	if h.adminPasswordHash == "" {
		writeError(w, http.StatusNotFound, "not found")
		return nil, false
	}
	store, ok := h.store.(adminStore)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return nil, false
	}
	provided, cookieAuth := adminRequestToken(r)
	if cookieAuth {
		if !h.requestUsesHTTPS(r) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return nil, false
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !h.allowBrowserAdminMutation(r) {
			writeError(w, http.StatusForbidden, "cross-site request rejected")
			return nil, false
		}
	}
	if authStore, ok := h.store.(adminAuthStore); ok {
		authorized, err := authStore.AuthorizeAdminSession(r.Context(), provided)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return nil, false
		}
		if authorized {
			return store, true
		}
		configured, err := authStore.AdminAccountConfigured(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return nil, false
		}
		if configured {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return nil, false
		}
	}
	if !h.bootstrapAdminPasswordMatches(provided) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	return store, true
}

// authorizeExtendedHistoryRequest protects the heavier 7-day and 30-day
// history endpoints while allowing the public dashboard to pass its existing
// opaque admin session in X-Admin-Token.
func (h *handler) authorizeExtendedHistoryRequest(w http.ResponseWriter, r *http.Request) bool {
	if h.adminPasswordHash == "" {
		writeError(w, http.StatusNotFound, "not found")
		return false
	}
	provided, cookieAuth := adminRequestToken(r)
	if cookieAuth && !h.requestUsesHTTPS(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if authStore, ok := h.store.(adminAuthStore); ok {
		authorized, err := authStore.AuthorizeAdminSession(r.Context(), provided)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return false
		}
		if authorized {
			return true
		}
		configured, err := authStore.AdminAccountConfigured(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return false
		}
		if configured {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return false
		}
	}
	if h.bootstrapAdminPasswordMatches(provided) {
		return true
	}
	writeError(w, http.StatusUnauthorized, "unauthorized")
	return false
}

func (h *handler) bootstrapAdminPasswordMatches(password string) bool {
	if h.adminPasswordHash == "" {
		return false
	}
	releaseArgon2, admitted := reserveAdminArgon2Request()
	if !admitted {
		return false
	}
	defer releaseArgon2()
	return adminPasswordMatches("", h.adminPasswordHash, password)
}

func (h *handler) adminLoginIPRateLimitKey(r *http.Request) string {
	return "ip:" + h.clientIPForRateLimit(r)
}

func (h *handler) adminLoginRateLimitKey(r *http.Request, username string) string {
	remote := h.clientIPForRateLimit(r)
	// Keep attacker-controlled usernames out of the limiter map. Login bodies can
	// be tens of KiB, while the digest is fixed-size and preserves exact keys.
	usernameDigest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username))))
	return remote + ":" + hex.EncodeToString(usernameDigest[:])
}

func (h *handler) clientIPForRateLimit(r *http.Request) string {
	remoteIP := parseRemoteIP(r.RemoteAddr)
	if remoteIP != nil && h.trustedProxies.contains(remoteIP) {
		forwardedValue := r.Header.Get("X-Forwarded-For")
		if strings.TrimSpace(forwardedValue) != "" {
			if forwarded, valid := h.trustedProxies.forwardedClientIP(forwardedValue); valid && forwarded != nil {
				return forwarded.String()
			}
			// A malformed XFF chain is not allowed to fall through to another
			// attacker-controlled forwarding header.
			return remoteIP.String()
		}
		if realIP := parseRemoteIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); realIP != nil {
			return realIP.String()
		}
	}
	if remoteIP != nil {
		return remoteIP.String()
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// adminErrorResponses maps admin store errors onto their HTTP response.
//
// This is a lookup table rather than a chain of conditionals because it is pure
// data: adding an error means adding a row, and the whole admin error contract
// can be read in one place. Order matters -- the first matching group wins --
// so more specific errors must precede any broader group that would also match.
var adminErrorResponses = []struct {
	status  int
	message string
	errs    []error
}{
	// A type managed by alert rules is deliberately distinguished from a missing
	// one: the client asked for something that existed and was taken over, and
	// 404 would send it looking for a typo instead.
	{http.StatusGone, "notification type is managed by alert rules", []error{errNotificationTypeGone}},
	{http.StatusNotFound, "not found", []error{
		errNodeNotFound, errProbeTargetNotFound, errNotificationChannelNotFound,
		errNotificationTypeNotFound, errAlertRuleNotFound,
	}},
	// Validation failures collapse to a single opaque message on purpose: the
	// admin UI validates client-side, so a detailed server message would only
	// help someone probing the API.
	{http.StatusBadRequest, "bad request", []error{
		errInvalidAdminSettingsUpdate, errInvalidAdminNodeUpdate, errInvalidAdminNodeCreate,
		errInvalidAdminTargetWrite, errInvalidAdminNotificationChannelWrite,
		errInvalidAdminNotificationTypeWrite, errInvalidAdminAlertRuleUpdate,
		errInvalidAdminPasswordUpdate,
	}},
	{http.StatusConflict, "settings changed", []error{errAdminSettingsConflict}},
	{http.StatusConflict, "notification key unavailable", []error{errNotificationCredentialKeyRequired}},
	{http.StatusConflict, "already exists", []error{
		errNodeAlreadyExists, errProbeTargetAlreadyExists, errNotificationChannelAlreadyExists,
	}},
}

// writeAdminError translates a store error into its admin API response,
// defaulting to 500 so an unrecognised error is never reported as the client's
// fault.
func writeAdminError(w http.ResponseWriter, err error) {
	for _, response := range adminErrorResponses {
		for _, candidate := range response.errs {
			if errors.Is(err, candidate) {
				writeError(w, response.status, response.message)
				return
			}
		}
	}
	writeError(w, http.StatusInternalServerError, "internal error")
}
