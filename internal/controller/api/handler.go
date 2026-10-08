package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type HandlerOptions struct {
	StaticDir                string
	Store                    Store
	AdminPasswordHash        string
	TrustedProxies           TrustedProxySet
	AgentBinaryPath          string
	AgentVersion             string
	NotificationClient       *http.Client
	TelegramAPIBaseURL       string
	StaleOfflineScanInterval time.Duration
	HistoryRetentionInterval time.Duration
	// NotificationReconcileInterval enables the notification reconcile loop.
	NotificationReconcileInterval time.Duration
	ExchangeRateRefreshInterval   time.Duration
	ExchangeRateClient            *http.Client
	ExchangeRateURL               string
	DisableNotifications          bool
	BackgroundContext             context.Context
}

type handler struct {
	store                       Store
	adminPasswordHash           string
	agentBinaryPath             string
	agentVersion                string
	notificationSender          notificationSender
	loginLimiter                *adminLoginLimiter
	enrollmentLimiter           *adminLoginLimiter
	trustedProxies              TrustedProxySet
	agentQuotas                 *agentQuotaManager
	agentAuthAdmission          *agentAuthAdmissionManager
	liveHub                     *liveUpdateHub
	presence                    *agentPresenceManager
	publicWSGate                *websocketGate
	agentWSGate                 *websocketGate
	summaryScheduleMu           sync.Mutex
	summaryPublishTimer         *time.Timer
	summaryRefreshTimer         *time.Timer
	summaryLastPublished        time.Time
	summaryCacheMu              sync.RWMutex
	summaryCache                []byte
	summaryCacheUpdated         time.Time
	summaryCacheDirty           bool
	summaryCacheDirtyRevision   uint64
	summaryCacheGeneration      uint64
	summaryCacheFlight          *jsonCacheFlight
	detailCache                 *detailJSONCache
	detailPublishMu             sync.Mutex
	detailPublishPending        map[string]bool
	detailPublishGate           chan struct{}
	backgroundMu                sync.Mutex
	backgroundClosing           bool
	backgroundCtx               context.Context
	backgroundCancel            context.CancelFunc
	backgroundWG                sync.WaitGroup
	notificationReconcilerOnce  sync.Once
	notificationReconcilerState *notificationReconciler
	// Test seams for the reconcile loop; zero values use the wall clock, the
	// process time zone and notificationSendTimeout.
	notificationClock       func() time.Time
	notificationLoc         *time.Location
	notificationSendTimeout time.Duration
	performance             *runtimePerformance
	router                  http.Handler
}

const (
	adminJSONBodyLimit      int64 = 64 << 10
	agentStateJSONBodyLimit int64 = 64 << 10
	agentProbeJSONBodyLimit int64 = 1 << 20
)

func decodeJSONBody(w http.ResponseWriter, r *http.Request, target any, limit int64, disallowUnknown bool) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if disallowUnknown {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "bad request")
		return false
	}
	return true
}

const (
	publicWebSocketMaxConnections      = 128
	publicWebSocketMaxConnectionsPerIP = 16
	agentWebSocketMaxConnections       = 256
	detailPublishMaxConcurrent         = 2
)

func NewHandler(options ...HandlerOptions) http.Handler {
	opts := HandlerOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	store := opts.Store
	if store == nil {
		store = unconfiguredStore{}
	}
	backgroundParent := opts.BackgroundContext
	if backgroundParent == nil {
		backgroundParent = context.Background()
	}
	backgroundCtx, backgroundCancel := context.WithCancel(backgroundParent)
	h := &handler{
		store:                       store,
		adminPasswordHash:           opts.AdminPasswordHash,
		agentBinaryPath:             opts.AgentBinaryPath,
		agentVersion:                opts.AgentVersion,
		notificationSender:          newHTTPNotificationSender(opts.NotificationClient, opts.TelegramAPIBaseURL),
		loginLimiter:                newAdminLoginLimiter(),
		enrollmentLimiter:           newAdminLoginLimiter(),
		trustedProxies:              opts.TrustedProxies,
		agentQuotas:                 newAgentQuotaManager(),
		agentAuthAdmission:          newAgentAuthAdmissionManager(),
		liveHub:                     newLiveUpdateHub(),
		presence:                    newAgentPresenceManager(),
		publicWSGate:                newWebSocketGateWithPerKey(publicWebSocketMaxConnections, publicWebSocketMaxConnectionsPerIP),
		agentWSGate:                 newWebSocketGateWithPerKey(agentWebSocketMaxConnections, 0),
		detailCache:                 newDetailJSONCache(),
		detailPublishPending:        make(map[string]bool),
		detailPublishGate:           make(chan struct{}, detailPublishMaxConcurrent),
		notificationReconcilerState: newNotificationReconciler(),
		performance:                 newRuntimePerformance(),
		backgroundCtx:               backgroundCtx,
		backgroundCancel:            backgroundCancel,
	}
	if opts.DisableNotifications {
		h.notificationSender = nil
	}
	if opts.StaleOfflineScanInterval > 0 {
		h.startBackground(func(ctx context.Context) { h.runStaleAgentOfflineScanner(ctx, opts.StaleOfflineScanInterval) })
	}
	if opts.HistoryRetentionInterval > 0 {
		h.startBackground(func(ctx context.Context) { h.runHistoryRetention(ctx, opts.HistoryRetentionInterval) })
	}
	if opts.NotificationReconcileInterval > 0 {
		h.startBackground(func(ctx context.Context) { h.runNotificationReconcileLoop(ctx, opts.NotificationReconcileInterval) })
	}
	if opts.ExchangeRateRefreshInterval > 0 {
		h.startBackground(func(ctx context.Context) {
			h.runExchangeRateRefresher(ctx, opts.ExchangeRateRefreshInterval, opts.ExchangeRateClient, opts.ExchangeRateURL)
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/ready", h.handleReady)
	mux.HandleFunc("/api/public/v1/agent/linux-amd64", h.handleAgentBinary)
	mux.HandleFunc("/api/public/v1/settings", h.handlePublicSettings)
	mux.HandleFunc("/api/public/v1/summary", h.handleSummary)
	mux.HandleFunc("/api/public/v1/summary/ws", h.handleSummaryWebSocket)
	mux.HandleFunc("/api/public/v1/services/", h.handlePublicServiceResource)
	mux.HandleFunc("/api/public/v1/nodes/", h.handlePublicNodeResource)
	mux.HandleFunc("/api/admin/v1/login", h.handleAdminLogin)
	mux.HandleFunc("/api/admin/v1/logout", h.handleAdminLogout)
	mux.HandleFunc("/api/admin/v1/account", h.handleAdminAccount)
	mux.HandleFunc("/api/admin/v1/settings", h.handleAdminSettings)
	mux.HandleFunc("/api/admin/v1/performance", h.handleAdminPerformance)
	mux.HandleFunc("/api/admin/v1/notification-channels", h.handleAdminNotificationChannels)
	mux.HandleFunc("/api/admin/v1/notification-channels/", h.handleAdminNotificationChannelResource)
	mux.HandleFunc("/api/admin/v1/alert-rules", h.handleAdminAlertRules)
	mux.HandleFunc("/api/admin/v1/alert-rules/", h.handleAdminAlertRuleResource)
	mux.HandleFunc("/api/admin/v1/notification-types/", h.handleAdminNotificationTypeResource)
	mux.HandleFunc("/api/admin/v1/probe-targets", h.handleAdminProbeTargets)
	mux.HandleFunc("/api/admin/v1/probe-targets/reorder", h.handleAdminProbeTargetReorder)
	mux.HandleFunc("/api/admin/v1/probe-targets/", h.handleAdminProbeTargetResource)
	mux.HandleFunc("/api/admin/v1/nodes", h.handleAdminNodes)
	mux.HandleFunc("/api/admin/v1/nodes/reorder", h.handleAdminNodeReorder)
	mux.HandleFunc("/api/admin/v1/nodes/", h.handleAdminNodeResource)
	mux.HandleFunc("/api/agent/v1/enroll", h.handleAgentEnrollment)
	mux.HandleFunc("/api/agent/v1/probe-targets", h.handleAgentProbeTargets)
	mux.HandleFunc("/api/agent/v1/presence/ws", h.handleAgentPresenceWebSocket)
	mux.HandleFunc("/api/agent/v1/probe-results", h.handleAgentProbeResults)
	mux.HandleFunc("/api/agent/v1/heartbeat", h.handleAgentHeartbeat)
	mux.HandleFunc("/api/agent/v1/host", h.handleAgentHost)
	mux.HandleFunc("/api/agent/v1/state", h.handleAgentState)
	if opts.StaticDir != "" {
		mux.HandleFunc("/", handleStatic(opts.StaticDir))
	}
	h.router = h.withSecurityHeaders(mux)
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.router.ServeHTTP(w, r)
}

func (h *handler) startBackground(fn func(context.Context)) {
	if h == nil || fn == nil {
		return
	}
	ctx, ok := h.beginBackground()
	if !ok {
		return
	}
	go func() {
		defer h.backgroundWG.Done()
		fn(ctx)
	}()
}

func (h *handler) backgroundContext() context.Context {
	if h == nil {
		return context.Background()
	}
	h.backgroundMu.Lock()
	defer h.backgroundMu.Unlock()
	if h.backgroundCtx == nil {
		return context.Background()
	}
	return h.backgroundCtx
}

func (h *handler) beginBackground() (context.Context, bool) {
	if h == nil {
		return nil, false
	}
	h.backgroundMu.Lock()
	defer h.backgroundMu.Unlock()
	if h.backgroundClosing {
		return nil, false
	}
	if h.backgroundCtx == nil {
		h.backgroundCtx, h.backgroundCancel = context.WithCancel(context.Background())
	}
	h.backgroundWG.Add(1)
	return h.backgroundCtx, true
}

func (h *handler) Cleanup(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.backgroundMu.Lock()
	h.backgroundClosing = true
	if h.backgroundCancel != nil {
		h.backgroundCancel()
	}
	h.backgroundMu.Unlock()
	if h.presence != nil {
		h.presence.cancelOfflineChecks()
	}
	h.summaryScheduleMu.Lock()
	if h.summaryPublishTimer != nil {
		h.summaryPublishTimer.Stop()
	}
	if h.summaryRefreshTimer != nil {
		h.summaryRefreshTimer.Stop()
	}
	h.summaryScheduleMu.Unlock()
	done := make(chan struct{})
	go func() {
		h.backgroundWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}

}

func (h *handler) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, adminCookieErr := r.Cookie(adminSessionCookieName)
		hasAdminCookie := adminCookieErr == nil
		hasAdminHeader := strings.TrimSpace(r.Header.Get("X-Admin-Token")) != ""
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data:; font-src 'self' https: data:; connect-src 'self'")
		if h.requestUsesHTTPS(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(r.URL.Path, "/api/admin/") || r.URL.Path == "/api/agent/v1/enroll" || hasAdminHeader || hasAdminCookie {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.URL.Path == "/api/agent/v1/enroll" {
			w.Header().Set("Pragma", "no-cache")
		}
		if hasAdminHeader {
			w.Header().Add("Vary", "X-Admin-Token")
		}
		if hasAdminCookie {
			w.Header().Add("Vary", "Cookie")
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handler) requestUsesHTTPS(r *http.Request) bool {
	if r == nil {
		return false
	}
	return h.requestProto(r) == "https"
}
