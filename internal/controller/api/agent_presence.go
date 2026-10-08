package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type agentPresenceSession struct {
	nodeID string
	id     uint64
	send   chan []byte
	done   chan struct{}
}

type agentPresenceOfflineCheck struct {
	nodeID string
	ctx    context.Context
	cancel context.CancelFunc
}

type agentPresenceManager struct {
	mu            sync.Mutex
	nextID        uint64
	sessions      map[string]*agentPresenceSession
	offlineChecks map[string]*agentPresenceOfflineCheck
}

func newAgentPresenceManager() *agentPresenceManager {
	return &agentPresenceManager{
		sessions:      map[string]*agentPresenceSession{},
		offlineChecks: map[string]*agentPresenceOfflineCheck{},
	}
}

const agentPresenceSendQueueLimit = 2

func (m *agentPresenceManager) connect(nodeID string) *agentPresenceSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelOfflineCheckLocked(nodeID)
	m.nextID++
	session := &agentPresenceSession{nodeID: nodeID, id: m.nextID, send: make(chan []byte, agentPresenceSendQueueLimit), done: make(chan struct{})}
	if previous := m.sessions[nodeID]; previous != nil {
		close(previous.done)
	}
	m.sessions[nodeID] = session
	return session
}

func (m *agentPresenceManager) disconnect(session *agentPresenceSession) bool {
	if session == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.sessions[session.nodeID]
	if current == nil || current.id != session.id {
		return false
	}
	delete(m.sessions, session.nodeID)
	return true
}

func (m *agentPresenceManager) reserveOfflineCheck(parent context.Context, nodeID string) (*agentPresenceOfflineCheck, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[nodeID] != nil {
		return nil, false
	}
	m.cancelOfflineCheckLocked(nodeID)
	ctx, cancel := context.WithCancel(parent)
	check := &agentPresenceOfflineCheck{nodeID: nodeID, ctx: ctx, cancel: cancel}
	m.offlineChecks[nodeID] = check
	return check, true
}

func (m *agentPresenceManager) offlineCheckReady(check *agentPresenceOfflineCheck) bool {
	if m == nil || check == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.offlineChecks[check.nodeID] == check && m.sessions[check.nodeID] == nil && check.ctx.Err() == nil
}

func (m *agentPresenceManager) finishOfflineCheck(check *agentPresenceOfflineCheck) {
	if m == nil || check == nil {
		return
	}
	m.mu.Lock()
	if m.offlineChecks[check.nodeID] == check {
		delete(m.offlineChecks, check.nodeID)
	}
	m.mu.Unlock()
	check.cancel()
}

func (m *agentPresenceManager) cancelOfflineCheckLocked(nodeID string) {
	check := m.offlineChecks[nodeID]
	if check == nil {
		return
	}
	delete(m.offlineChecks, nodeID)
	check.cancel()
}

func (m *agentPresenceManager) cancelOfflineChecks() {
	if m == nil {
		return
	}
	m.mu.Lock()
	for nodeID, check := range m.offlineChecks {
		delete(m.offlineChecks, nodeID)
		check.cancel()
	}
	m.mu.Unlock()
}

func (m *agentPresenceManager) notifyConfigChanged(nodeID string, version int64) bool {
	if m == nil {
		return false
	}
	payload, err := json.Marshal(agentPresenceServerMessage{Type: "config_changed", Version: version})
	if err != nil {
		return false
	}
	m.mu.Lock()
	session := m.sessions[nodeID]
	m.mu.Unlock()
	if session == nil {
		return false
	}
	select {
	case session.send <- payload:
	default:
		select {
		case <-session.send:
		default:
		}
		select {
		case session.send <- payload:
		default:
		}
	}
	return true
}

func (m *agentPresenceManager) notifyAllConfigChanged(version int64) int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	nodeIDs := make([]string, 0, len(m.sessions))
	for nodeID := range m.sessions {
		nodeIDs = append(nodeIDs, nodeID)
	}
	m.mu.Unlock()
	count := 0
	for _, nodeID := range nodeIDs {
		if m.notifyConfigChanged(nodeID, version) {
			count++
		}
	}
	return count
}

type agentPresenceServerMessage struct {
	Type    string `json:"type"`
	Version int64  `json:"version,omitempty"`
}

type agentPresenceClientMessage struct {
	Type    string `json:"type"`
	Version int64  `json:"version,omitempty"`
}

type probeConfigAppliedStore interface {
	RecordProbeConfigApplied(ctx context.Context, nodeID string, version int64, now time.Time) error
}

type staleAgentOfflineStore interface {
	agentStore
	StaleAgentOfflineNodeIDs(ctx context.Context, now time.Time) ([]string, error)
	RecordStaleAgentOfflineTransition(ctx context.Context, nodeID string, now time.Time) (notificationStatusTransition, bool, error)
}

func (h *handler) runStaleAgentOfflineScanner(ctx context.Context, interval time.Duration) {
	h.runStaleAgentOfflineScannerWithGrace(ctx, interval, nodeHeartbeatOfflineAfter)
}

func (h *handler) runStaleAgentOfflineScannerWithGrace(ctx context.Context, interval, startupGrace time.Duration) {
	if h == nil || interval <= 0 || startupGrace <= 0 {
		return
	}
	// A freshly started Controller cannot distinguish an actually offline Agent
	// from one that simply has not reconnected after the Controller restart yet.
	// Give Agents one full liveness window before the first scan so deployments
	// and short Controller outages do not immediately create false incidents.
	startupTimer := time.NewTimer(startupGrace)
	defer startupTimer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-startupTimer.C:
		h.dispatchStaleAgentOfflineChecks(ctx)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.dispatchStaleAgentOfflineChecks(ctx)
		}
	}
}

func (h *handler) dispatchStaleAgentOfflineChecks(ctx context.Context) {
	if h == nil {
		return
	}
	store, ok := h.store.(staleAgentOfflineStore)
	if !ok {
		return
	}
	now := time.Now().UTC()
	nodeIDs, err := store.StaleAgentOfflineNodeIDs(ctx, now)
	if err != nil || len(nodeIDs) == 0 {
		return
	}
	changed := false
	for _, nodeID := range nodeIDs {
		if h.dispatchStaleAgentOfflineNode(ctx, store, nodeID, now) {
			changed = true
		}
	}
	if !changed {
		return
	}
	h.markSummaryCacheDirty()
	h.publishSummary(ctx)
}

func (h *handler) dispatchStaleAgentOfflineNode(ctx context.Context, store staleAgentOfflineStore, nodeID string, now time.Time) bool {
	_, ok, err := store.RecordStaleAgentOfflineTransition(ctx, nodeID, now)
	if err != nil || !ok {
		return false
	}
	h.wakeNotificationReconcile()
	return true
}

func (h *handler) handleAgentPresenceWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, nodeID, ok := h.authorizeAgentRequest(w, r)
	if !ok {
		return
	}
	if !h.admitAgentRequest(w, nodeID, agentQuotaPresence) {
		return
	}
	releaseNodePresence, retryAfter, ok := h.agentQuotas.acquirePresence(nodeID)
	if !ok {
		writeAgentRateLimit(w, retryAfter)
		return
	}
	defer releaseNodePresence()
	release, ok := h.agentWSGate.acquireFor("")
	if !ok {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "too many websocket connections")
		return
	}
	defer release()
	conn, err := summaryWebSocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.EnableWriteCompression(true)
	refreshReadDeadline := func() error {
		return conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	}
	if err := refreshReadDeadline(); err != nil {
		return
	}
	conn.SetPongHandler(func(string) error {
		return refreshReadDeadline()
	})

	session := h.presence.connect(nodeID)
	h.markSummaryCacheDirty()
	h.publishSummary(r.Context())
	defer func() {
		if h.presence.disconnect(session) {
			h.scheduleAgentPresenceOfflineCheck(store, nodeID)
			h.markSummaryCacheDirty()
			h.publishSummary(r.Context())
		}
	}()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		h.readAgentPresenceMessages(r.Context(), conn, store, nodeID, refreshReadDeadline)
	}()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-session.done:
			_ = writeWebSocketControl(conn, websocketCloseMessage, []byte{}, 2*time.Second)
			return
		case <-readDone:
			return
		case payload := <-session.send:
			if err := writeWebSocketMessage(conn, websocketTextMessage, payload); err != nil {
				return
			}
		case <-ping.C:
			if err := writeWebSocketControl(conn, websocketPingMessage, []byte{}, websocketWriteTimeout); err != nil {
				return
			}
		}
	}
}

func (h *handler) scheduleAgentPresenceOfflineCheck(store agentStore, nodeID string) {
	h.scheduleAgentPresenceOfflineCheckAfter(store, nodeID, nodeHeartbeatOfflineAfter)
}

func (h *handler) scheduleAgentPresenceOfflineCheckAfter(store agentStore, nodeID string, delay time.Duration) {
	if h == nil || h.presence == nil || delay <= 0 {
		return
	}
	offlineStore, ok := store.(staleAgentOfflineStore)
	if !ok {
		return
	}
	backgroundCtx, ok := h.beginBackground()
	if !ok {
		return
	}
	check, ok := h.presence.reserveOfflineCheck(backgroundCtx, nodeID)
	if !ok {
		h.backgroundWG.Done()
		return
	}
	go func() {
		defer h.backgroundWG.Done()
		defer h.presence.finishOfflineCheck(check)
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-check.ctx.Done():
			return
		case <-timer.C:
		}
		if !h.presence.offlineCheckReady(check) {
			return
		}
		// A reconnect cancels check.ctx. Store operations use that context, so a
		// reconnect racing the debounce boundary can still abort stale work.
		if !h.dispatchStaleAgentOfflineNode(check.ctx, offlineStore, nodeID, time.Now().UTC()) {
			return
		}
		h.markSummaryCacheDirty()
		h.publishSummary(check.ctx)
	}()
}

const (
	websocketTextMessage  = 1
	websocketCloseMessage = 8
	websocketPingMessage  = 9
)

func (h *handler) notifyProbeConfigChanged(ctx context.Context) {
	store, ok := h.store.(probeConfigVersionStore)
	if !ok {
		return
	}
	version, err := store.ProbeConfigVersion(ctx)
	if err != nil {
		return
	}
	h.presence.notifyAllConfigChanged(version)
}
