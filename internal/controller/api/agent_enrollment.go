package api

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"
)

const (
	agentEnrollmentTTL          = 10 * time.Minute
	agentPendingActivationTTL   = 30 * time.Minute
	agentEnrollmentRetention    = 24 * time.Hour
	maxAgentEnrollmentBodyBytes = 8 << 10
)

var errAgentEnrollmentUnavailable = errors.New("agent enrollment unavailable")

type agentEnrollmentStore interface {
	RedeemAgentEnrollment(ctx context.Context, nodeID, enrollmentToken, runtimeToken string) error
}

type AgentEnrollmentRequest struct {
	NodeID          string `json:"node_id"`
	EnrollmentToken string `json:"enrollment_token"`
	RuntimeToken    string `json:"runtime_token"`
}

func validRuntimeToken(token string) bool {
	token = strings.TrimSpace(token)
	if len(token) < 43 || len(token) > 128 {
		return false
	}
	for _, character := range token {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func (s *sqliteAgentAccess) issueAgentEnrollment(ctx context.Context, nodeID string) (string, time.Time, error) {
	enrollmentToken, err := randomToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := time.Now().UTC()
	expiresAt := now.Add(agentEnrollmentTTL)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()

	if err := lockAgentNodeWriteTx(ctx, tx, nodeID); err != nil {
		if errors.Is(err, errNodeNotFound) {
			return "", time.Time{}, errNodeNotFound
		}
		return "", time.Time{}, err
	}
	// A newly generated command is the only active enrollment command for this
	// node. It does not touch token_hash, so the currently installed Agent keeps
	// working. It does supersede an older unactivated enrollment/runtime pair.
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_enrollment_tokens
		SET revoked_at = ?
		WHERE node_id = ? AND used_at IS NULL AND revoked_at IS NULL
	`, now.Unix(), nodeID); err != nil {
		return "", time.Time{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET pending_token_hash = NULL,
			pending_token_expires_at = NULL,
			install_token = NULL,
			updated_at = ?
		WHERE id = ?
	`, now.Unix(), nodeID); err != nil {
		return "", time.Time{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_enrollment_tokens (token_hash, node_id, created_at, expires_at, used_at, revoked_at)
		VALUES (?, ?, ?, ?, NULL, NULL)
	`, hashAgentToken(enrollmentToken), nodeID, now.Unix(), expiresAt.Unix()); err != nil {
		return "", time.Time{}, err
	}
	cutoff := now.Add(-agentEnrollmentRetention).Unix()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM agent_enrollment_tokens
		WHERE expires_at < ? AND (used_at IS NOT NULL OR revoked_at IS NOT NULL)
	`, cutoff); err != nil {
		return "", time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return "", time.Time{}, err
	}
	tx = nil
	return enrollmentToken, expiresAt, nil
}

func (s *sqliteAgentAccess) RedeemAgentEnrollment(ctx context.Context, nodeID, enrollmentToken, runtimeToken string) error {
	nodeID = strings.TrimSpace(nodeID)
	enrollmentToken = strings.TrimSpace(enrollmentToken)
	runtimeToken = strings.TrimSpace(runtimeToken)
	if nodeID == "" || enrollmentToken == "" || !validRuntimeToken(runtimeToken) {
		return errAgentEnrollmentUnavailable
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { rollbackUnlessCommitted(tx) }()

	if err := execRequiringOneRow(ctx, tx, errAgentEnrollmentUnavailable, `
		UPDATE agent_enrollment_tokens
		SET used_at = ?
		WHERE token_hash = ?
		  AND node_id = ?
		  AND used_at IS NULL
		  AND revoked_at IS NULL
		  AND expires_at > ?
		  AND EXISTS (SELECT 1 FROM nodes WHERE id = ? AND disabled = 0)
	`, now.Unix(), hashAgentToken(enrollmentToken), nodeID, now.Unix(), nodeID); err != nil {
		return err
	}
	if err := execRequiringOneRow(ctx, tx, errAgentEnrollmentUnavailable, `
		UPDATE nodes
		SET pending_token_hash = ?,
			pending_token_expires_at = ?,
			install_token = NULL,
			updated_at = ?
		WHERE id = ? AND disabled = 0
	`, hashAgentToken(runtimeToken), now.Add(agentPendingActivationTTL).Unix(), now.Unix(), nodeID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (h *handler) handleAgentEnrollment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		writeError(w, http.StatusForbidden, "cross-site enrollment rejected")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "application/json required")
		return
	}
	limiterKey := "enrollment:" + h.clientIPForRateLimit(r)
	reservation := adminLoginReservation{}
	if h.enrollmentLimiter != nil {
		var allowed bool
		reservation, allowed = h.enrollmentLimiter.reserve(limiterKey)
		if !allowed {
			w.Header().Set("Retry-After", "600")
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
	}
	succeeded := false
	defer func() { reservation.release(succeeded) }()
	store, ok := h.store.(agentEnrollmentStore)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var request AgentEnrollmentRequest
	if !decodeJSONBody(w, r, &request, maxAgentEnrollmentBodyBytes, true) {
		return
	}
	if err := store.RedeemAgentEnrollment(r.Context(), request.NodeID, request.EnrollmentToken, request.RuntimeToken); err != nil {
		if errors.Is(err, errAgentEnrollmentUnavailable) {
			// Expired, superseded, already-used, and unknown credentials have one
			// response to avoid turning the endpoint into a node/token oracle.
			writeError(w, http.StatusGone, "enrollment unavailable")
			return
		}
		logAgentAPIError("enroll", strings.TrimSpace(request.NodeID), "redeem", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	succeeded = true
	w.WriteHeader(http.StatusNoContent)
}
