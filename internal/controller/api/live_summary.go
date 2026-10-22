package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type liveUpdateHub struct {
	mu      sync.Mutex
	clients map[string]map[chan []byte]struct{}
	last    map[string][]byte
}

func newLiveUpdateHub() *liveUpdateHub {
	return &liveUpdateHub{clients: map[string]map[chan []byte]struct{}{}, last: map[string][]byte{}}
}

func (hub *liveUpdateHub) subscribe(topic string) (<-chan []byte, func()) {
	updates := make(chan []byte, 1)
	hub.mu.Lock()
	if hub.clients[topic] == nil {
		hub.clients[topic] = map[chan []byte]struct{}{}
	}
	hub.clients[topic][updates] = struct{}{}
	hub.mu.Unlock()
	return updates, func() {
		hub.mu.Lock()
		if clients, ok := hub.clients[topic]; ok {
			if _, ok := clients[updates]; ok {
				delete(clients, updates)
				close(updates)
			}
			if len(clients) == 0 {
				delete(hub.clients, topic)
				delete(hub.last, topic)
			}
		} else {
			close(updates)
		}
		hub.mu.Unlock()
	}
}

func (hub *liveUpdateHub) hasClients(topic string) bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return len(hub.clients[topic]) > 0
}

func (hub *liveUpdateHub) publish(topic string, payload []byte) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	clients := hub.clients[topic]
	if len(clients) == 0 {
		delete(hub.last, topic)
		return
	}
	if bytes.Equal(hub.last[topic], payload) {
		return
	}
	hub.last[topic] = append(hub.last[topic][:0], payload...)
	for updates := range clients {
		select {
		case updates <- payload:
		default:
			select {
			case <-updates:
			default:
			}
			select {
			case updates <- payload:
			default:
			}
		}
	}
}

const summaryLiveTopic = "summary"

const (
	summaryPublishCoalesceDelay = 250 * time.Millisecond
	// Agents report state every three seconds. Rebuilding the full public
	// summary more frequently cannot expose newer state, but repeatedly scans
	// the rolling 24-hour latency aggregates on large databases.
	summaryPublishMinInterval = 3 * time.Second
)

func nodeStateLiveTopic(nodeID, rangeName string) string {
	return "node-state:" + nodeID + ":" + rangeName
}

func nodeLatencyLiveTopic(nodeID, rangeName string) string {
	return "node-latency:" + nodeID + ":" + rangeName
}

func liveWindowNames() []string {
	return []string{"1h", "1d", "7d", "30d"}
}

var summaryWebSocketUpgrader = websocket.Upgrader{
	ReadBufferSize:    32 * 1024,
	WriteBufferSize:   32 * 1024,
	EnableCompression: true,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		parsed, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(parsed.Host, r.Host)
	},
}

const (
	summaryCacheHTTPFreshFor     = 30 * time.Second
	summaryCacheBackgroundDelay  = 350 * time.Millisecond
	summaryHTTPQueryTimeout      = 5 * time.Second
	summaryGenerationMaxRetries  = 1
	detailCacheFreshFor          = 3 * time.Second
	detailCacheSevenDayFreshFor  = time.Minute
	detailCacheThirtyDayFreshFor = 5 * time.Minute
	detailPublishTimeout         = 5 * time.Second
	websocketWriteTimeout        = 5 * time.Second
	websocketReadTimeout         = 75 * time.Second
)

func (h *handler) handleSummaryWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	release, ok := h.acquirePublicWebSocket(r)
	if !ok {
		writeError(w, http.StatusTooManyRequests, "too many websocket connections")
		return
	}
	defer release()
	initial, cached := h.cachedSummaryJSON(0)
	if !cached {
		var err error
		initial, err = h.summaryJSONForHTTP(r.Context())
		if err != nil {
			writeStoreError(w, err)
			return
		}
	}
	updates, unsubscribe := h.liveHub.subscribe(summaryLiveTopic)
	if cached {
		h.scheduleSummaryPublish()
	}
	h.handleLiveJSONWebSocket(w, r, initial, updates, unsubscribe)
}

func (h *handler) handleDetailLiveJSONWebSocket(w http.ResponseWriter, r *http.Request, topic string, load func(context.Context) ([]byte, error)) {
	release, ok := h.acquirePublicWebSocket(r)
	if !ok {
		writeError(w, http.StatusTooManyRequests, "too many websocket connections")
		return
	}
	defer release()
	payload, err := load(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	updates, unsubscribe := h.liveHub.subscribe(topic)
	unsubscribe = h.unsubscribeAndEvictDetailTopic(topic, unsubscribe)
	h.handleLiveJSONWebSocket(w, r, payload, updates, unsubscribe)
}

func (h *handler) unsubscribeAndEvictDetailTopic(topic string, unsubscribe func()) func() {
	return func() {
		unsubscribe()
		if h.liveHub != nil && h.liveHub.hasClients(topic) {
			return
		}
		h.detailCache.evict(topic)
	}
}

func (h *handler) handleLiveJSONWebSocket(w http.ResponseWriter, r *http.Request, initial []byte, updates <-chan []byte, unsubscribe func()) {
	defer unsubscribe()
	conn, err := summaryWebSocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.EnableWriteCompression(true)
	refreshReadDeadline := func() error {
		return conn.SetReadDeadline(time.Now().Add(websocketReadTimeout))
	}
	if err := refreshReadDeadline(); err != nil {
		return
	}
	conn.SetPongHandler(func(string) error { return refreshReadDeadline() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn.SetReadLimit(1024)
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
			if err := refreshReadDeadline(); err != nil {
				return
			}
		}
	}()

	if err := writeWebSocketMessage(conn, websocket.TextMessage, initial); err != nil {
		return
	}
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-done:
			return
		case payload, ok := <-updates:
			if !ok {
				return
			}
			if err := writeWebSocketMessage(conn, websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ping.C:
			if err := writeWebSocketControl(conn, websocket.PingMessage, []byte{}, websocketWriteTimeout); err != nil {
				return
			}
		}
	}
}

func writeWebSocketMessage(conn *websocket.Conn, messageType int, payload []byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(websocketWriteTimeout)); err != nil {
		return err
	}
	err := conn.WriteMessage(messageType, payload)
	clearErr := conn.SetWriteDeadline(time.Time{})
	if err != nil {
		return err
	}
	return clearErr
}

func writeWebSocketControl(conn *websocket.Conn, messageType int, payload []byte, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = websocketWriteTimeout
	}
	return conn.WriteControl(messageType, payload, time.Now().Add(timeout))
}

func (h *handler) acquirePublicWebSocket(r *http.Request) (func(), bool) {
	key := h.clientIPForRateLimit(r)
	if key == "" {
		key = "unknown"
	}
	return h.publicWSGate.acquireFor(key)
}

func (h *handler) summaryJSON(ctx context.Context) ([]byte, error) {
	return h.loadSummaryJSON(ctx, 0, false)
}

func (h *handler) summaryJSONForHTTP(ctx context.Context) ([]byte, error) {
	queryCtx, cancel := context.WithTimeout(ctx, summaryHTTPQueryTimeout)
	defer cancel()
	return h.loadSummaryJSON(queryCtx, summaryCacheHTTPFreshFor, true)
}

// loadSummaryJSON coalesces the complete Summary query and JSON encoding. HTTP
// callers allow a fresh cache hit; the check under summaryCacheMu is the
// leader's second check after handleSummary's fast path, closing the usual
// cache-miss race. Live publishers bypass the cached value but still join an
// in-flight build for the same generation.
func (h *handler) loadSummaryJSON(ctx context.Context, maxAge time.Duration, allowCached bool) ([]byte, error) {
	retries := 0
	for {
		now := time.Now()
		h.summaryCacheMu.Lock()
		if allowCached && len(h.summaryCache) > 0 && !h.summaryCacheDirty && (maxAge <= 0 || now.Sub(h.summaryCacheUpdated) <= maxAge) {
			payload := append([]byte(nil), h.summaryCache...)
			h.summaryCacheMu.Unlock()
			return payload, nil
		}
		generation := h.summaryCacheGeneration
		dirtyRevision := h.summaryCacheDirtyRevision
		if flight := h.summaryCacheFlight; flight != nil {
			h.summaryCacheMu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-flight.done:
			}
			h.summaryCacheMu.RLock()
			currentGeneration := h.summaryCacheGeneration
			h.summaryCacheMu.RUnlock()
			if currentGeneration != flight.generation {
				if retries < summaryGenerationMaxRetries {
					retries++
					continue
				}
				return append([]byte(nil), flight.payload...), flight.err
			}
			return append([]byte(nil), flight.payload...), flight.err
		}

		flight := &jsonCacheFlight{generation: generation, done: make(chan struct{})}
		h.summaryCacheFlight = flight
		h.summaryCacheMu.Unlock()

		started := time.Now()
		summary, err := h.store.Summary(ctx)
		var payload []byte
		if err == nil {
			payload, err = json.Marshal(summary)
		}
		if h.performance != nil {
			h.performance.recordSummaryBuild(time.Since(started), len(payload), err)
		}

		h.summaryCacheMu.Lock()
		currentGeneration := h.summaryCacheGeneration
		if err == nil && currentGeneration == generation {
			h.summaryCache = append(h.summaryCache[:0], payload...)
			h.summaryCacheUpdated = time.Now()
			// Commit this complete point-in-time snapshot even if another soft
			// invalidation raced the build, but preserve that newer dirty revision
			// so a later refresh cannot be lost.
			h.summaryCacheDirty = h.summaryCacheDirtyRevision != dirtyRevision
		}
		flight.payload = append([]byte(nil), payload...)
		flight.err = err
		if h.summaryCacheFlight == flight {
			h.summaryCacheFlight = nil
		}
		close(flight.done)
		h.summaryCacheMu.Unlock()

		if currentGeneration != generation {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if retries < summaryGenerationMaxRetries {
				retries++
				continue
			}
			// A completed build remains a valid point-in-time snapshot. Do not
			// cache it under a newer generation, but return it after the bounded
			// retry so sustained writes cannot keep an HTTP/live caller looping.
			return append([]byte(nil), payload...), err
		}
		return append([]byte(nil), payload...), err
	}
}

func cachedDetailJSON[T any](h *handler, ctx context.Context, key, subjectID string, window latencyWindow, load func(context.Context, string, latencyWindow) (T, error)) ([]byte, error) {
	return h.detailCache.get(ctx, key, detailCacheFreshness(window), func() ([]byte, error) {
		value, err := load(ctx, subjectID, window)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	})
}

func detailCacheFreshness(window latencyWindow) time.Duration {
	switch window.Name {
	case "7d":
		return detailCacheSevenDayFreshFor
	case "30d":
		return detailCacheThirtyDayFreshFor
	default:
		return detailCacheFreshFor
	}
}

func (h *handler) nodeStateJSON(ctx context.Context, nodeID string, window latencyWindow) ([]byte, error) {
	return cachedDetailJSON(h, ctx, nodeStateLiveTopic(nodeID, window.Name), nodeID, window, h.store.NodeState)
}

func (h *handler) nodeLatencyJSON(ctx context.Context, nodeID string, window latencyWindow) ([]byte, error) {
	return cachedDetailJSON(h, ctx, nodeLatencyLiveTopic(nodeID, window.Name), nodeID, window, h.store.NodeLatency)
}

func (h *handler) scheduleNodeStatePublish(nodeID string) {
	h.scheduleDetailFor(nodeID, "node-state:", nodeStateLiveTopic, h.publishNodeState)
}

func (h *handler) scheduleNodeLatencyPublish(nodeID string) {
	h.scheduleDetailFor(nodeID, "node-latency:", nodeLatencyLiveTopic, h.publishNodeLatency)
}

func (h *handler) scheduleDetailFor(subjectID, keyPrefix string, topicFor func(string, string) string, publish func(context.Context, string)) {
	if !h.hasDetailClients(subjectID, topicFor) {
		return
	}
	h.scheduleDetailPublish(keyPrefix+subjectID, func(ctx context.Context) {
		publish(ctx, subjectID)
	})
}

func (h *handler) scheduleDetailPublish(key string, publish func(context.Context)) {
	if h == nil || key == "" || publish == nil || h.backgroundContext().Err() != nil {
		return
	}
	h.detailPublishMu.Lock()
	if h.detailPublishPending == nil {
		h.detailPublishPending = make(map[string]bool)
	}
	if _, pending := h.detailPublishPending[key]; pending {
		h.detailPublishPending[key] = true
		h.detailPublishMu.Unlock()
		return
	}
	if h.detailPublishGate == nil {
		h.detailPublishGate = make(chan struct{}, detailPublishMaxConcurrent)
	}
	h.detailPublishPending[key] = false
	gate := h.detailPublishGate
	h.detailPublishMu.Unlock()

	backgroundCtx, ok := h.beginBackground()
	if !ok {
		h.detailPublishMu.Lock()
		delete(h.detailPublishPending, key)
		h.detailPublishMu.Unlock()
		return
	}
	go func() {
		defer h.backgroundWG.Done()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-backgroundCtx.Done():
			h.detailPublishMu.Lock()
			delete(h.detailPublishPending, key)
			h.detailPublishMu.Unlock()
			return
		}
		for {
			ctx, cancel := context.WithTimeout(backgroundCtx, detailPublishTimeout)
			publish(ctx)
			cancel()

			h.detailPublishMu.Lock()
			if h.detailPublishPending[key] && backgroundCtx.Err() == nil {
				h.detailPublishPending[key] = false
				h.detailPublishMu.Unlock()
				continue
			}
			delete(h.detailPublishPending, key)
			h.detailPublishMu.Unlock()
			return
		}
	}()
}

func (h *handler) hasDetailClients(subjectID string, topicFor func(string, string) string) bool {
	if h == nil || h.liveHub == nil {
		return false
	}
	for _, rangeName := range liveWindowNames() {
		if h.liveHub.hasClients(topicFor(subjectID, rangeName)) {
			return true
		}
	}
	return false
}

func (h *handler) cachedSummaryJSON(maxAge time.Duration) ([]byte, bool) {
	h.summaryCacheMu.RLock()
	defer h.summaryCacheMu.RUnlock()
	if len(h.summaryCache) == 0 {
		return nil, false
	}
	if maxAge > 0 && (h.summaryCacheDirty || time.Since(h.summaryCacheUpdated) > maxAge) {
		return nil, false
	}
	return append([]byte(nil), h.summaryCache...), true
}

func (h *handler) markSummaryCacheDirty() {
	h.summaryCacheMu.Lock()
	h.summaryCacheDirtyRevision++
	h.summaryCacheDirty = true
	h.summaryCacheMu.Unlock()
}

func (h *handler) invalidateSummaryCache() {
	h.summaryCacheMu.Lock()
	h.summaryCacheGeneration++
	h.summaryCache = nil
	h.summaryCacheUpdated = time.Time{}
	h.summaryCacheDirty = false
	h.summaryCacheMu.Unlock()
}

func (h *handler) invalidateSummaryAggregates() {
	if store, ok := h.store.(interface{ invalidateSummaryAggregates() }); ok {
		store.invalidateSummaryAggregates()
	}
}

func (h *handler) publishNodeState(ctx context.Context, nodeID string) {
	publishDetail(h, ctx, nodeID, resolveStateWindow, nodeStateLiveTopic, h.store.NodeState)
}

func (h *handler) publishNodeLatency(ctx context.Context, nodeID string) {
	publishDetail(h, ctx, nodeID, resolveLatencyWindow, nodeLatencyLiveTopic, h.store.NodeLatency)
}

func publishDetail[T any](
	h *handler,
	ctx context.Context,
	subjectID string,
	resolveWindow func(string) (latencyWindow, bool),
	topicFor func(string, string) string,
	load func(context.Context, string, latencyWindow) (T, error),
) {
	if h.liveHub == nil {
		return
	}
	for _, rangeName := range liveWindowNames() {
		window, ok := resolveWindow(rangeName)
		if !ok {
			continue
		}
		topic := topicFor(subjectID, window.Name)
		if !h.liveHub.hasClients(topic) {
			continue
		}
		payload, err := h.detailCache.refreshWithAge(topic, detailCacheFreshness(window), func() ([]byte, error) {
			value, err := load(ctx, subjectID, window)
			if err != nil {
				return nil, err
			}
			return json.Marshal(value)
		})
		if err == nil {
			h.liveHub.publish(topic, payload)
		}
	}
}
