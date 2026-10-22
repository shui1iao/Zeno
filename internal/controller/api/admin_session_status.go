package api

import (
	"net/http"
	"strings"
)

// AdminSessionStatusResponse is the only payload of the cookie-session probe.
// It deliberately carries no username, token, expiry or account metadata.
type AdminSessionStatusResponse struct {
	Authenticated bool `json:"authenticated"`
}

// handleAdminSessionStatus lets the public dashboard learn whether the
// browser already holds a valid HttpOnly admin session without provoking a
// 401 console error for every anonymous visitor. It applies exactly the same
// session checks as authorizeAdminRequest for a cookie-authenticated GET
// (HTTPS required, same session store, same bootstrap fallback) and differs
// only in answering "not signed in" with 200 {"authenticated":false}.
//
// Only the browser cookie is consulted: X-Admin-Token is ignored so the probe
// cannot be used as a header-token validity oracle. The response is not
// cacheable (all /api/admin/ responses are no-store).
func (h *handler) handleAdminSessionStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.adminPasswordHash == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	authenticated, err := h.browserAdminSessionAuthenticated(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, AdminSessionStatusResponse{Authenticated: authenticated})
}

func (h *handler) browserAdminSessionAuthenticated(r *http.Request) (bool, error) {
	cookie, err := r.Cookie(adminSessionCookieName)
	if err != nil {
		return false, nil
	}
	if !h.requestUsesHTTPS(r) {
		return false, nil
	}
	token := strings.TrimSpace(cookie.Value)
	if token == "" {
		return false, nil
	}
	if authStore, ok := h.store.(adminAuthStore); ok {
		authorized, err := authStore.AuthorizeAdminSession(r.Context(), token)
		if err != nil {
			return false, err
		}
		if authorized {
			return true, nil
		}
		configured, err := authStore.AdminAccountConfigured(r.Context())
		if err != nil {
			return false, err
		}
		if configured {
			return false, nil
		}
	}
	return h.bootstrapAdminPasswordMatches(token), nil
}
