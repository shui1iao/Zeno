package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/shui1iao/zeno/internal/shared/probe"
)

const (
	maxAgentProbeRounds            = maxProbeTargetsPerNode
	maxAgentProbeSamplesPerRound   = maxProbeTargetCount
	maxAgentTimestampFutureSkew    = 5 * time.Minute
	maxAgentTimestampPastSkew      = 10 * time.Minute
	maxAgentProbeTimestampPastSkew = 72*time.Hour + maxAgentTimestampFutureSkew
	minAgentStateReportInterval    = time.Second
)

type agentStore interface {
	AuthorizeAgent(ctx context.Context, nodeID, token string) (bool, error)
	EnabledProbeTargets(ctx context.Context, nodeID string) ([]ProbeTarget, error)
	InsertProbeRound(ctx context.Context, nodeID string, target ProbeTarget, ts time.Time, samples []probe.Sample) error
	RecordAgentHeartbeat(ctx context.Context, nodeID string, ts time.Time, status, agentVersion string) error
	UpsertAgentHost(ctx context.Context, nodeID string, host AgentHostRequest) error
	InsertAgentState(ctx context.Context, nodeID string, state AgentStateRequest) error
}

type agentProbeBatchStore interface {
	InsertAgentProbeResults(ctx context.Context, nodeID string, configVersion int64, rounds []preparedAgentProbeRound) (agentProbeInsertResult, error)
}

type agentProbeTargetSnapshotStore interface {
	EnabledProbeTargetsWithConfigVersion(ctx context.Context, nodeID string) ([]ProbeTarget, int64, error)
}

type agentStateReportStore interface {
	RecordAgentStateReport(ctx context.Context, nodeID string, state AgentStateRequest) (bool, notificationStatusTransition, error)
}

type preparedAgentProbeRound struct {
	targetID       string
	targetType     string
	target         ProbeTarget
	ts             time.Time
	idempotencyKey string
	agentRoundID   string
	payloadHash    string
	samples        []probe.Sample
}

type agentProbeInsertResult struct {
	inserted          int
	idempotent        int
	insertedTargetIDs []string
}

var (
	errAgentProbeConfigStale    = errors.New("stale probe config")
	errAgentProbeRoundConflict  = errors.New("probe round id conflict")
	errInvalidAgentProbeResults = errors.New("invalid agent probe results")
	errInvalidAgentStateReport  = errors.New("invalid agent state report")
)

func (h *handler) handleAgentProbeTargets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, nodeID, ok := h.authorizeAgentRequest(w, r)
	if !ok {
		return
	}
	if !h.admitAgentRequest(w, nodeID, agentQuotaProbeTargets) {
		return
	}
	var version int64
	var targets []ProbeTarget
	var err error
	if snapshotStore, ok := store.(agentProbeTargetSnapshotStore); ok {
		targets, version, err = snapshotStore.EnabledProbeTargetsWithConfigVersion(r.Context(), nodeID)
	} else {
		targets, err = store.EnabledProbeTargets(r.Context(), nodeID)
		if err == nil {
			if versionStore, ok := h.store.(probeConfigVersionStore); ok {
				version, err = versionStore.ProbeConfigVersion(r.Context())
			}
		}
	}
	if err != nil {
		logAgentAPIError("probe-targets", nodeID, "load_targets_snapshot", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	response := AgentProbeTargetsResponse{Targets: make([]AgentProbeTarget, 0, len(targets)), Version: version}
	for _, target := range targets {
		response.Targets = append(response.Targets, AgentProbeTarget{
			ID:          target.ID,
			Name:        target.Name,
			Type:        target.Type,
			Address:     target.Address,
			Port:        target.Port,
			Count:       target.Count,
			TimeoutMS:   target.TimeoutMS,
			IntervalSec: target.IntervalSec,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) handleAgentProbeResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, nodeID, ok := h.authorizeAgentRequest(w, r)
	if !ok {
		return
	}
	if !h.admitAgentRequest(w, nodeID, agentQuotaProbeResults) {
		return
	}

	var request AgentProbeResultsRequest
	if !decodeJSONBody(w, r, &request, agentProbeJSONBodyLimit, false) {
		return
	}
	configVersion, validConfigVersion := request.effectiveConfigVersion()
	if !validConfigVersion {
		writeError(w, http.StatusBadRequest, "invalid config version")
		return
	}

	prepared, validationErr := validateAgentProbeRounds(request)
	if validationErr != nil {
		writeError(w, validationErr.status, validationErr.message)
		return
	}
	releaseWrite, ok := h.admitAgentWrite(w, nodeID, agentProbeWriteUnits(request, r.ContentLength))
	if !ok {
		return
	}
	defer releaseWrite()

	var insertResult agentProbeInsertResult
	if batchStore, ok := store.(agentProbeBatchStore); ok {
		var err error
		insertResult, err = batchStore.InsertAgentProbeResults(r.Context(), nodeID, configVersion, prepared)
		if err != nil {
			if errors.Is(err, errAgentProbeConfigStale) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "stale_probe_config"})
				return
			}
			if errors.Is(err, errAgentProbeRoundConflict) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "probe_round_conflict"})
				return
			}
			if errors.Is(err, errInvalidAgentProbeResults) {
				writeError(w, http.StatusBadRequest, "invalid probe round")
				return
			}
			if errors.Is(err, errTelemetryStoragePressure) {
				writeTelemetryStoragePressure(w)
				return
			}
			logAgentAPIError("probe-results", nodeID, "insert_results", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else {
		logAgentAPIError("probe-results", nodeID, "store_capability", errors.New("agent probe batch store unavailable"))
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if insertResult.inserted > 0 {
		// Rolling aggregates intentionally use their short fixed TTL; only the
		// lightweight JSON snapshot is marked dirty on the Agent cadence. A fully
		// idempotent retry has no publish/cache side effects.
		h.markSummaryCacheDirty()
		h.publishSummary(r.Context())
		h.scheduleNodeLatencyPublish(nodeID)
		for _, targetID := range insertResult.insertedTargetIDs {
			if targetID != "" {
				h.scheduleServiceLatencyPublish(targetID)
			}
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":         true,
		"accepted":   len(prepared),
		"inserted":   insertResult.inserted,
		"idempotent": insertResult.idempotent,
	})
}

func (h *handler) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, nodeID, ok := h.authorizeAgentRequest(w, r)
	if !ok {
		return
	}
	if !h.admitAgentRequest(w, nodeID, agentQuotaHeartbeat) {
		return
	}
	var request AgentHeartbeatRequest
	if !decodeJSONBody(w, r, &request, agentStateJSONBodyLimit, false) {
		return
	}
	if request.TS <= 0 {
		writeError(w, http.StatusBadRequest, "invalid timestamp")
		return
	}
	receivedAt := time.Now().UTC()
	agentTS := time.Unix(request.TS, 0).UTC()
	if !agentTimestampWithinSkew(agentTS, receivedAt, maxAgentTimestampPastSkew) {
		writeError(w, http.StatusBadRequest, "timestamp skew too large")
		return
	}
	status := strings.TrimSpace(request.Status)
	if status == "" {
		status = "online"
	}
	if !validAgentStatus(status) {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	status = normalizeHeartbeatStatus(status)
	releaseWrite, ok := h.admitAgentWrite(w, nodeID, 1)
	if !ok {
		return
	}
	defer releaseWrite()
	// Liveness is authoritative at the Controller receive time. The Agent
	// timestamp is validated above, but never allowed to move last_seen_at into
	// the future or keep a node online after its clock is corrected.
	heartbeatTS := receivedAt
	if transitionStore, ok := store.(heartbeatTransitionStore); ok {
		if _, err := transitionStore.RecordAgentHeartbeatTransition(r.Context(), nodeID, heartbeatTS, status, strings.TrimSpace(request.AgentVersion)); err != nil {
			logAgentAPIError("heartbeat", nodeID, "record_transition", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else if err := store.RecordAgentHeartbeat(r.Context(), nodeID, heartbeatTS, status, strings.TrimSpace(request.AgentVersion)); err != nil {
		logAgentAPIError("heartbeat", nodeID, "record", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// The stored status is committed; let the reconcile loop compare it with
	// what the user was last told.
	h.wakeNotificationReconcile()
	h.markSummaryCacheDirty()
	h.publishSummary(r.Context())
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (h *handler) handleAgentHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, nodeID, ok := h.authorizeAgentRequest(w, r)
	if !ok {
		return
	}
	if !h.admitAgentRequest(w, nodeID, agentQuotaHost) {
		return
	}
	var request AgentHostRequest
	if !decodeJSONBody(w, r, &request, agentStateJSONBodyLimit, false) {
		return
	}
	if strings.TrimSpace(request.OSName) == "" || strings.TrimSpace(request.Arch) == "" {
		writeError(w, http.StatusBadRequest, "host os and arch required")
		return
	}
	if request.CPUCores < 0 || request.MemoryTotalBytes < 0 || request.DiskTotalBytes < 0 || request.BootTime < 0 {
		writeError(w, http.StatusBadRequest, "invalid host values")
		return
	}
	releaseWrite, ok := h.admitAgentWrite(w, nodeID, 2)
	if !ok {
		return
	}
	defer releaseWrite()
	if err := store.UpsertAgentHost(r.Context(), nodeID, request); err != nil {
		logAgentAPIError("host", nodeID, "upsert_host", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.markSummaryCacheDirty()
	h.publishSummary(r.Context())
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (h *handler) handleAgentState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, nodeID, ok := h.authorizeAgentRequest(w, r)
	if !ok {
		return
	}
	if !h.admitAgentRequest(w, nodeID, agentQuotaState) {
		return
	}
	var request AgentStateRequest
	if !decodeJSONBody(w, r, &request, agentStateJSONBodyLimit, false) {
		return
	}
	if request.TS <= 0 {
		writeError(w, http.StatusBadRequest, "invalid timestamp")
		return
	}
	stateTS := time.Unix(request.TS, 0).UTC()
	if !agentTimestampWithinSkew(stateTS, time.Now().UTC(), maxAgentTimestampPastSkew) {
		writeError(w, http.StatusBadRequest, "timestamp skew too large")
		return
	}
	if sampleID := strings.TrimSpace(request.effectiveSampleID()); sampleID != "" && !validAgentStateSampleID(sampleID) {
		writeError(w, http.StatusBadRequest, "invalid state id")
		return
	}
	if !validAgentStateValues(request) {
		writeError(w, http.StatusBadRequest, "invalid state values")
		return
	}
	releaseWrite, ok := h.admitAgentWrite(w, nodeID, 1)
	if !ok {
		return
	}
	defer releaseWrite()
	if reportStore, ok := store.(agentStateReportStore); ok {
		accepted, _, err := reportStore.RecordAgentStateReport(r.Context(), nodeID, request)
		if err != nil {
			if errors.Is(err, errInvalidAgentStateReport) {
				writeError(w, http.StatusBadRequest, "invalid state report")
				return
			}
			if errors.Is(err, errTelemetryStoragePressure) {
				writeTelemetryStoragePressure(w)
				return
			}
			logAgentAPIError("state", nodeID, "record_report", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if accepted {
			h.wakeNotificationReconcile()
			h.scheduleNodeStatePublish(nodeID)
		}
		h.publishSummary(r.Context())
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "accepted": accepted})
		return
	}
	if err := store.InsertAgentState(r.Context(), nodeID, request); err != nil {
		if errors.Is(err, errTelemetryStoragePressure) {
			writeTelemetryStoragePressure(w)
			return
		}
		logAgentAPIError("state", nodeID, "insert_state", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if transitionStore, ok := store.(stateAlertRuleTransitionStore); ok {
		if _, err := transitionStore.RecordAgentStateAlertRuleTransition(r.Context(), nodeID, stateTS, request); err != nil {
			logAgentAPIError("state", nodeID, "record_alert_transition", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		h.wakeNotificationReconcile()
	}
	h.publishSummary(r.Context())
	h.scheduleNodeStatePublish(nodeID)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func writeTelemetryStoragePressure(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	writeError(w, http.StatusInsufficientStorage, "telemetry storage pressure")
}

func agentTimestampWithinSkew(ts, now time.Time, pastSkew time.Duration) bool {
	if ts.After(now.Add(maxAgentTimestampFutureSkew)) {
		return false
	}
	if ts.Before(now.Add(-pastSkew)) {
		return false
	}
	return true
}

func validAgentProbeRoundID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func validAgentStatus(status string) bool {
	switch status {
	case "online", "offline", "warning", "no_data":
		return true
	default:
		return false
	}
}

func normalizeHeartbeatStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "warning":
		return "warning"
	default:
		// A request that successfully reaches the Controller with a valid Agent
		// token is, by definition, fresh liveness. Older Agents may still send
		// "offline" or "no_data" in this field, but accepting that value would let
		// a delayed heartbeat overwrite the server-side stale/offline state machine.
		return "online"
	}
}

func validAgentStateSampleID(value string) bool {
	return validAgentProbeRoundID(value)
}

func validNetworkCounterSource(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') {
			continue
		}
		return false
	}
	return true
}

func invalidFloat(value float64) bool {
	return math.IsNaN(value) || math.IsInf(value, 0)
}

func optionalFloatInvalidOrNegative(value *float64) bool {
	return value != nil && (invalidFloat(*value) || *value < 0)
}

func optionalIntNegative(value *int64) bool {
	return value != nil && *value < 0
}

func (h *handler) authorizeAgentRequest(w http.ResponseWriter, r *http.Request) (agentStore, string, bool) {
	store, ok := h.store.(agentStore)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return nil, "", false
	}
	nodeID := strings.TrimSpace(r.Header.Get("X-Node-ID"))
	if nodeID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, "", false
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(authorization, "Bearer ") {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	release, retryAfter, admitted := h.agentAuthAdmission.admit(h.clientIPForRateLimit(r))
	if !admitted {
		writeAgentRateLimit(w, retryAfter)
		return nil, "", false
	}
	defer release()
	allowed, err := store.AuthorizeAgent(r.Context(), nodeID, token)
	if err != nil {
		logAgentAPIError(agentEndpointName(r.URL.Path), nodeID, "authorize", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return nil, "", false
	}
	if !allowed {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, "", false
	}
	return store, nodeID, true
}

func logAgentAPIError(endpoint, nodeID, stage string, err error) {
	if err == nil {
		return
	}
	log.Printf("agent_api_error endpoint=%s node_id=%s stage=%s error=%s", safeLogToken(endpoint), safeLogToken(nodeID), safeLogToken(stage), sanitizeAgentAPIError(err))
}

func agentEndpointName(path string) string {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return "unknown"
	}
	parts := strings.Split(path, "/")
	return safeLogToken(parts[len(parts)-1])
}

func sanitizeAgentAPIError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "error"
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "bearer ") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "credential") || strings.Contains(lower, "authorization") {
		return "redacted"
	}
	if len(message) > 200 {
		message = message[:200]
	}
	return message
}

func safeLogToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == ':' {
			builder.WriteRune(r)
			continue
		}
		builder.WriteByte('_')
	}
	result := builder.String()
	if result == "" {
		return "unknown"
	}
	if len(result) > 128 {
		return result[:128]
	}
	return result
}
