package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The status code returned for each admin error is part of the API contract the
// web UI branches on, so every mapping is pinned explicitly rather than trusting
// the table to be read correctly.
func TestWriteAdminErrorStatusMapping(t *testing.T) {
	cases := []struct {
		err     error
		status  int
		message string
	}{
		{errNotificationTypeGone, http.StatusGone, "notification type is managed by alert rules"},
		{errNodeNotFound, http.StatusNotFound, "not found"},
		{errProbeTargetNotFound, http.StatusNotFound, "not found"},
		{errNotificationChannelNotFound, http.StatusNotFound, "not found"},
		{errNotificationTypeNotFound, http.StatusNotFound, "not found"},
		{errAlertRuleNotFound, http.StatusNotFound, "not found"},
		{errInvalidAdminSettingsUpdate, http.StatusBadRequest, "bad request"},
		{errInvalidAdminNodeUpdate, http.StatusBadRequest, "bad request"},
		{errInvalidAdminNodeCreate, http.StatusBadRequest, "bad request"},
		{errInvalidAdminTargetWrite, http.StatusBadRequest, "bad request"},
		{errInvalidAdminNotificationChannelWrite, http.StatusBadRequest, "bad request"},
		{errInvalidAdminNotificationTypeWrite, http.StatusBadRequest, "bad request"},
		{errInvalidAdminAlertRuleUpdate, http.StatusBadRequest, "bad request"},
		{errInvalidAdminPasswordUpdate, http.StatusBadRequest, "bad request"},
		{errAdminSettingsConflict, http.StatusConflict, "settings changed"},
		{errNotificationCredentialKeyRequired, http.StatusConflict, "notification key unavailable"},
		{errNodeAlreadyExists, http.StatusConflict, "already exists"},
		{errProbeTargetAlreadyExists, http.StatusConflict, "already exists"},
		{errNotificationChannelAlreadyExists, http.StatusConflict, "already exists"},
	}

	for _, testCase := range cases {
		t.Run(testCase.err.Error(), func(t *testing.T) {
			status, message := captureAdminError(t, testCase.err)
			if status != testCase.status || message != testCase.message {
				t.Fatalf("got %d %q, want %d %q", status, message, testCase.status, testCase.message)
			}
		})
	}

	// A wrapped error must still map: stores add context as errors travel up.
	status, message := captureAdminError(t, fmt.Errorf("updating node: %w", errNodeNotFound))
	if status != http.StatusNotFound || message != "not found" {
		t.Fatalf("wrapped error: got %d %q, want 404 not found", status, message)
	}

	// An unrecognised error must never be reported as the client's fault.
	status, _ = captureAdminError(t, errors.New("some internal failure"))
	if status != http.StatusInternalServerError {
		t.Fatalf("unknown error status = %d, want 500", status)
	}
}

// Every admin error declared in the package must appear in the response table.
// Without this, adding an error and forgetting to map it would silently return
// 500 for what is actually a client-correctable condition -- and no
// hand-maintained test list would catch it.
func TestAdminErrorResponsesCoverAllAdminErrors(t *testing.T) {
	mapped := map[string]struct{}{}
	for _, response := range adminErrorResponses {
		for _, candidate := range response.errs {
			mapped[candidate.Error()] = struct{}{}
		}
	}

	declaration := regexp.MustCompile(`\berr(?:Admin|Node|ProbeTarget|Notification|AlertRule|InvalidAdmin)\w*\s*=\s*errors\.New\("([^"]+)"\)`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	missing := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, match := range declaration.FindAllStringSubmatch(string(source), -1) {
			text := match[1]
			if _, ok := mapped[text]; !ok {
				missing = append(missing, fmt.Sprintf("%s (%s)", text, name))
			}
		}
	}

	// Errors that deliberately never reach writeAdminError. Each was checked
	// against its call sites; if one of these ever starts being returned from an
	// admin handler, it must be added to the table instead of listed here.
	allowedUnmapped := map[string]string{
		// The login handler answers 401 directly and never distinguishes a wrong
		// username from a wrong password, so routing it through the shared table
		// would risk leaking which one was wrong.
		"invalid admin login": "admin_auth_store.go",
		// Internal to the deletion batch loop: it means another batch remains to
		// process, and is consumed as control flow rather than returned.
		"admin deletion history remains": "admin_delete_batches.go",
		// Reconcile loop signal, never surfaced on an admin request: a channel
		// credential could not be decrypted, so that channel's deliveries fail and
		// are retried.
		"notification credential unavailable": "notification_reconcile_store.go",
	}
	filtered := missing[:0]
	for _, item := range missing {
		text := item
		if index := strings.Index(item, " ("); index >= 0 {
			text = item[:index]
		}
		if _, ok := allowedUnmapped[text]; !ok {
			filtered = append(filtered, item)
		}
	}
	sort.Strings(filtered)
	if len(filtered) > 0 {
		t.Fatalf("admin errors missing from adminErrorResponses (map them or document why they are 500):\n  %s",
			strings.Join(filtered, "\n  "))
	}
}

func captureAdminError(t *testing.T, err error) (int, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	writeAdminError(recorder, err)
	var body struct {
		Error string `json:"error"`
	}
	if decodeErr := json.NewDecoder(recorder.Body).Decode(&body); decodeErr != nil {
		t.Fatalf("decode response body: %v", decodeErr)
	}
	return recorder.Code, body.Error
}
