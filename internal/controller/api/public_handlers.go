package api

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"
)

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *handler) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	checker, ok := h.store.(interface {
		Ready(ctx context.Context) error
	})
	if !ok {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := checker.Ready(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *handler) handleAgentBinary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.agentBinaryPath == "" {
		writeError(w, http.StatusNotFound, "agent binary not configured")
		return
	}
	if info, err := os.Stat(h.agentBinaryPath); err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "agent binary not found")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="zeno-agent-linux-amd64"`)
	http.ServeFile(w, r, h.agentBinaryPath)
}

func (h *handler) requestBaseURL(r *http.Request) string {
	proto := h.requestProto(r)
	if proto == "" {
		proto = "http"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "127.0.0.1:18980"
	}
	return strings.TrimRight(proto+"://"+host, "/")
}

func (h *handler) requestProto(r *http.Request) string {
	if r == nil {
		return ""
	}
	return h.trustedProxies.requestProto(&httpRequestView{
		remoteAddr:     r.RemoteAddr,
		forwardedProto: r.Header.Get("X-Forwarded-Proto"),
		tls:            r.TLS != nil,
	})
}

func (h *handler) handlePublicSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	settings, err := h.store.PublicSettings(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *handler) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if payload, ok := h.cachedSummaryJSON(summaryCacheHTTPFreshFor); ok {
		if h.performance != nil {
			h.performance.summaryFreshHits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
		return
	}
	// The public summary contains no private state. Serve the last complete
	// snapshot immediately while a single background refresh rebuilds a dirty or
	// expired cache. A newly started process still blocks once because it has no
	// safe snapshot to serve.
	if payload, ok := h.cachedSummaryJSON(0); ok {
		if h.performance != nil {
			h.performance.summaryStaleHits.Add(1)
		}
		writeRawJSON(w, http.StatusOK, payload)
		h.scheduleSummaryRefreshAfter(summaryCacheBackgroundDelay)
		return
	}
	if h.performance != nil {
		h.performance.summaryMisses.Add(1)
	}
	payload, err := h.summaryJSONForHTTP(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func (h *handler) handlePublicNodeResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/public/v1/nodes/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 && len(parts) != 3 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if len(parts) == 3 && parts[2] != "ws" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	nodeID := parts[0]
	rangeName := r.URL.Query().Get("range")
	var resolveWindow func(string) (latencyWindow, bool)
	var topicFor func(string, string) string
	var detailJSON func(context.Context, string, latencyWindow) ([]byte, error)
	switch parts[1] {
	case "latency":
		resolveWindow = resolveLatencyWindow
		topicFor = nodeLatencyLiveTopic
		detailJSON = h.nodeLatencyJSON
	case "state":
		resolveWindow = resolveStateWindow
		topicFor = nodeStateLiveTopic
		detailJSON = h.nodeStateJSON
	default:
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	window, ok := resolveWindow(rangeName)
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported range")
		return
	}
	if extendedHistoryWindow(window) && !h.authorizeExtendedHistoryRequest(w, r) {
		return
	}
	if len(parts) == 3 {
		h.handleDetailLiveJSONWebSocket(w, r, topicFor(nodeID, window.Name), func(ctx context.Context) ([]byte, error) {
			return detailJSON(ctx, nodeID, window)
		})
		return
	}
	payload, err := detailJSON(r.Context(), nodeID, window)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeRawJSON(w, http.StatusOK, payload)
}
