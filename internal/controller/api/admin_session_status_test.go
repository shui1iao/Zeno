package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func newAdminSessionStatusTestHandler(t *testing.T) http.Handler {
	t.Helper()
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "node-1", DisplayName: "Node 1", AgentToken: "agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	return NewHandler(HandlerOptions{Store: store, AdminPasswordHash: testAdminPasswordHash("admin-pass")})
}

func browserAdminLoginCookie(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	login := secureAdminRequest(http.MethodPost, "/api/admin/v1/login", `{"username":"admin","password":"admin-pass"}`)
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "https://zeno.example.com")
	login.Header.Set(adminCSRFHeaderName, adminCSRFHeaderValue)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, login)
	if recorder.Code != http.StatusOK {
		t.Fatalf("browser login status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == adminSessionCookieName {
			return cookie
		}
	}
	t.Fatal("browser login did not set the admin session cookie")
	return nil
}

func decodeSessionStatus(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("session status code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("session status Cache-Control=%q want no-store", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode session status: %v body=%s", err, recorder.Body.String())
	}
	if len(payload) != 1 {
		t.Fatalf("session status must only expose the authenticated flag, got %v", payload)
	}
	return payload
}

func TestAdminSessionStatusAnswersAnonymousVisitorsWithFalse(t *testing.T) {
	handler := newAdminSessionStatusTestHandler(t)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, secureAdminRequest(http.MethodGet, "/api/admin/v1/session", ""))
	if payload := decodeSessionStatus(t, recorder); payload["authenticated"] != false {
		t.Fatalf("anonymous session status=%v", payload)
	}
	if strings.Contains(recorder.Body.String(), "admin") {
		t.Fatalf("anonymous session status leaked account data: %s", recorder.Body.String())
	}
}

func TestAdminSessionStatusRecognisesSecureCookieSessionWithoutLeakingAccount(t *testing.T) {
	handler := newAdminSessionStatusTestHandler(t)
	cookie := browserAdminLoginCookie(t, handler)

	request := secureAdminRequest(http.MethodGet, "/api/admin/v1/session", "")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if payload := decodeSessionStatus(t, recorder); payload["authenticated"] != true {
		t.Fatalf("cookie session status=%v", payload)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "admin") || strings.Contains(body, cookie.Value) {
		t.Fatalf("session status leaked account or token: %s", body)
	}
	if len(recorder.Result().Cookies()) != 0 {
		t.Fatalf("session status must not issue or refresh cookies: %v", recorder.Result().Cookies())
	}
}

func TestAdminSessionStatusKeepsCookieTransportAndTokenChecks(t *testing.T) {
	handler := newAdminSessionStatusTestHandler(t)
	cookie := browserAdminLoginCookie(t, handler)

	t.Run("plain http cookie is not trusted", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "http://zeno.example.com/api/admin/v1/session", nil)
		request.AddCookie(cookie)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if payload := decodeSessionStatus(t, recorder); payload["authenticated"] != false {
			t.Fatalf("insecure cookie session status=%v", payload)
		}
	})

	t.Run("forged cookie is rejected", func(t *testing.T) {
		request := secureAdminRequest(http.MethodGet, "/api/admin/v1/session", "")
		request.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: "forged-session"})
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if payload := decodeSessionStatus(t, recorder); payload["authenticated"] != false {
			t.Fatalf("forged cookie session status=%v", payload)
		}
	})

	t.Run("header token is not an oracle", func(t *testing.T) {
		login := httptest.NewRequest(http.MethodPost, "/api/admin/v1/login", strings.NewReader(`{"username":"admin","password":"admin-pass"}`))
		login.Header.Set("Content-Type", "application/json")
		loginRecorder := httptest.NewRecorder()
		handler.ServeHTTP(loginRecorder, login)
		var session AdminLoginResponse
		if err := json.NewDecoder(loginRecorder.Body).Decode(&session); err != nil || session.Token == "" {
			t.Fatalf("CLI login response=%+v err=%v", session, err)
		}
		request := secureAdminRequest(http.MethodGet, "/api/admin/v1/session", "")
		request.Header.Set("X-Admin-Token", session.Token)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if payload := decodeSessionStatus(t, recorder); payload["authenticated"] != false {
			t.Fatalf("header-token session status=%v", payload)
		}
	})

	t.Run("revoked cookie reads as signed out", func(t *testing.T) {
		logout := secureAdminRequest(http.MethodPost, "/api/admin/v1/logout", "")
		logout.AddCookie(cookie)
		logout.Header.Set("Origin", "https://zeno.example.com")
		logout.Header.Set(adminCSRFHeaderName, adminCSRFHeaderValue)
		logoutRecorder := httptest.NewRecorder()
		handler.ServeHTTP(logoutRecorder, logout)
		if logoutRecorder.Code != http.StatusNoContent {
			t.Fatalf("logout status=%d", logoutRecorder.Code)
		}
		request := secureAdminRequest(http.MethodGet, "/api/admin/v1/session", "")
		request.AddCookie(cookie)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if payload := decodeSessionStatus(t, recorder); payload["authenticated"] != false {
			t.Fatalf("revoked cookie session status=%v", payload)
		}
	})

	t.Run("only GET is allowed", func(t *testing.T) {
		request := secureAdminRequest(http.MethodPost, "/api/admin/v1/session", "")
		request.AddCookie(cookie)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST session status code=%d", recorder.Code)
		}
	})

	t.Run("account endpoint still requires authentication", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, secureAdminRequest(http.MethodGet, "/api/admin/v1/account", ""))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous account code=%d", recorder.Code)
		}
	})
}

func TestAdminSessionStatusIsHiddenWithoutAdminConfiguration(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, secureAdminRequest(http.MethodGet, "/api/admin/v1/session", ""))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unconfigured admin session status code=%d", recorder.Code)
	}
}
