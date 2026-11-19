package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func allowTestTLSServers(t *testing.T) {
	t.Helper()
	original := http.DefaultTransport
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test servers only
	http.DefaultTransport = transport
	t.Cleanup(func() {
		http.DefaultTransport = original
		transport.CloseIdleConnections()
	})
}

func TestHTTPProbeURLPolicy(t *testing.T) {
	for _, address := range []string{
		"https://example.com/health",
		"http://localhost/health",
		"http://127.0.0.1/health",
		"http://[::1]/health",
		"http://192.0.2.10:8080/health",
		"http://[2001:db8::1]:8080/health",
	} {
		if !validHTTPGetTargetAddress(address) {
			t.Fatalf("safe probe URL rejected: %s", address)
		}
	}
	for _, address := range []string{
		"http://example.com/health",
		"http://example.com:8080/health",
		"http://192.0.2.10/health",
		"https://user:pass@example.com/health",
		"ftp://example.com/file",
	} {
		if validHTTPGetTargetAddress(address) {
			t.Fatalf("unsafe probe URL accepted: %s", address)
		}
	}
}

func TestRunHTTPProbeRejectsMultiHopHTTPSDowngrade(t *testing.T) {
	allowTestTLSServers(t)
	var finalHits atomic.Int64
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		finalHits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer final.Close()
	middle := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, final.URL, http.StatusFound)
	}))
	defer middle.Close()
	start := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, middle.URL+"/next", http.StatusFound)
	}))
	defer start.Close()

	samples, err := RunHTTPProbe(context.Background(), ProbeTarget{ID: "secure", Type: "http_get", Address: start.URL + "/start", Count: 1, TimeoutMS: 1000})
	if err != nil {
		t.Fatalf("run probe: %v", err)
	}
	if len(samples) != 1 || samples[0].Success || samples[0].Error != "redirect_downgrade" {
		t.Fatalf("downgrade sample=%+v", samples)
	}
	if finalHits.Load() != 0 {
		t.Fatalf("downgraded HTTP endpoint received %d requests", finalHits.Load())
	}
}

func TestRunHTTPProbeCapsRedirects(t *testing.T) {
	var hits atomic.Int64
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		http.Redirect(w, request, server.URL+"/loop", http.StatusFound)
	}))
	defer server.Close()
	samples, err := RunHTTPProbe(context.Background(), ProbeTarget{ID: "loop", Type: "http_get", Address: server.URL + "/loop", Count: 1, TimeoutMS: 1000})
	if err != nil {
		t.Fatalf("run probe: %v", err)
	}
	if len(samples) != 1 || samples[0].Success || samples[0].Error != "redirect_limit" {
		t.Fatalf("redirect-loop sample=%+v", samples)
	}
	if hits.Load() > 10 {
		t.Fatalf("redirect loop issued %d requests, want at most 10", hits.Load())
	}
}

func TestRunHTTPProbeCrossOriginRedirectDropsReferer(t *testing.T) {
	allowTestTLSServers(t)
	var referer, authorization, cookie, userAgent string
	final := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		referer = request.Header.Get("Referer")
		authorization = request.Header.Get("Authorization")
		cookie = request.Header.Get("Cookie")
		userAgent = request.Header.Get("User-Agent")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer final.Close()
	start := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, final.URL+"/final", http.StatusFound)
	}))
	defer start.Close()

	samples, err := RunHTTPProbe(context.Background(), ProbeTarget{ID: "headers", Type: "http_get", Address: start.URL + "/start?token=secret", Count: 1, TimeoutMS: 1000})
	if err != nil {
		t.Fatalf("run probe: %v", err)
	}
	if len(samples) != 1 || !samples[0].Success {
		t.Fatalf("safe HTTPS redirect sample=%+v", samples)
	}
	if referer != "" || authorization != "" || cookie != "" {
		t.Fatalf("cross-origin sensitive headers leaked: referer=%q authorization=%q cookie=%q", referer, authorization, cookie)
	}
	if userAgent != "Zeno-Controller" {
		t.Fatalf("user agent=%q", userAgent)
	}
}

func TestRunHTTPProbeRejectsUserinfoBeforeDial(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	unsafeURL := strings.Replace(server.URL, "http://", "http://user:pass@", 1)
	if _, err := RunHTTPProbe(context.Background(), ProbeTarget{ID: "userinfo", Type: "http_get", Address: unsafeURL, Count: 1, TimeoutMS: 1000}); err == nil {
		t.Fatal("userinfo probe URL was accepted")
	}
	if hits.Load() != 0 {
		t.Fatalf("userinfo endpoint received %d requests", hits.Load())
	}
}

func TestRunHTTPProbePreservesFailureStatusCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer server.Close()
	samples, err := RunHTTPProbe(context.Background(), ProbeTarget{ID: "status", Type: "http_get", Address: server.URL, Count: 1, TimeoutMS: 1000})
	if err != nil {
		t.Fatalf("run HTTP probe: %v", err)
	}
	if len(samples) != 1 || samples[0].Success || samples[0].Error != "http_status_404" {
		t.Fatalf("HTTP failure samples = %+v, want http_status_404", samples)
	}
}

func TestRunHTTPProbeCompletesAfterHeadersWithoutWaitingForBody(t *testing.T) {
	requestCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
		close(requestCanceled)
	}))
	defer server.Close()

	started := time.Now()
	samples, err := RunHTTPProbe(context.Background(), ProbeTarget{ID: "headers-only", Type: "http_get", Address: server.URL, Count: 1, TimeoutMS: 1000})
	if err != nil {
		t.Fatalf("run HTTP probe: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("HTTP probe waited %s for response body, want header-only completion", elapsed)
	}
	if len(samples) != 1 || !samples[0].Success {
		t.Fatalf("header-only HTTP samples = %+v, want success", samples)
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("closing the unconsumed response body did not cancel the server request")
	}
}

func TestRunHTTPProbeDrainsSmallBodyAndReusesKeepAliveConnection(t *testing.T) {
	var mu sync.Mutex
	remoteAddrs := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		remoteAddrs = append(remoteAddrs, r.RemoteAddr)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("ok", 256)))
	}))
	defer server.Close()

	samples, err := RunHTTPProbe(context.Background(), ProbeTarget{
		ID: "keepalive", Type: "http_get", Address: server.URL, Count: 3, TimeoutMS: 1000,
	})
	if err != nil {
		t.Fatalf("RunHTTPProbe: %v", err)
	}
	if len(samples) != 3 || !samples[0].Success || !samples[1].Success || !samples[2].Success {
		t.Fatalf("keepalive samples = %+v, want 3 successes", samples)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(remoteAddrs) != 3 || remoteAddrs[0] != remoteAddrs[1] || remoteAddrs[1] != remoteAddrs[2] {
		t.Fatalf("HTTP probe remoteAddrs = %v, want single reused Keep-Alive connection", remoteAddrs)
	}
}

func TestRunPingProbeResolvesSafeLiteralIPAndBlocksInternalRanges(t *testing.T) {
	origLookup := localPingLookupNetIP
	origExec := localPingExecCommand
	t.Cleanup(func() {
		localPingLookupNetIP = origLookup
		localPingExecCommand = origExec
	})

	var execCalls atomic.Int64
	var capturedIPs []string
	localPingExecCommand = func(_ context.Context, ipLiteral string, _ bool, _ time.Duration) ([]byte, error) {
		execCalls.Add(1)
		capturedIPs = append(capturedIPs, ipLiteral)
		return []byte("64 bytes from " + ipLiteral + ": icmp_seq=1 ttl=56 time=12.3 ms\n"), nil
	}

	for _, unsafeAddr := range []string{
		"127.0.0.1", "::1",
		"169.254.169.254", "fe80::1",
		"10.0.0.1", "172.16.0.1", "192.168.1.1", "fc00::1",
		"100.100.100.200", "0.0.0.0", "::",
	} {
		samples, err := RunPingProbe(context.Background(), ProbeTarget{
			ID: "unsafe-literal", Type: "ping", Address: unsafeAddr, Count: 1, TimeoutMS: 1000,
		})
		if err == nil || len(samples) != 1 || samples[0].Success || samples[0].Error != "unsafe_target" {
			t.Fatalf("literal %s = (%+v, %v), want unsafe_target rejection", unsafeAddr, samples, err)
		}
	}

	// Hostname resolving to loopback / private / mixed safe+private must also be blocked before exec.
	for _, resolvedSet := range [][]string{
		{"127.0.0.1"},
		{"169.254.169.254"},
		{"192.168.1.10"},
		{"203.0.113.10", "10.0.0.8"},
	} {
		localPingLookupNetIP = func(_ context.Context, _ string) ([]netip.Addr, error) {
			out := make([]netip.Addr, 0, len(resolvedSet))
			for _, raw := range resolvedSet {
				out = append(out, netip.MustParseAddr(raw))
			}
			return out, nil
		}
		samples, err := RunPingProbe(context.Background(), ProbeTarget{
			ID: "rebind", Type: "ping", Address: "rebind.example.com", Count: 2, TimeoutMS: 1000,
		})
		if err == nil || len(samples) != 2 || samples[0].Success || samples[0].Error != "unsafe_target" {
			t.Fatalf("DNS rebind %v = (%+v, %v), want unsafe_target rejection", resolvedSet, samples, err)
		}
	}
	if got := execCalls.Load(); got != 0 {
		t.Fatalf("unsafe ping targets invoked ping %d times, want 0", got)
	}

	// Safe hostname must pass only the verified literal IP to ping, never the hostname.
	localPingLookupNetIP = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host != "safe.example.com" {
			t.Fatalf("lookup host = %q, want safe.example.com", host)
		}
		return []netip.Addr{netip.MustParseAddr("203.0.113.25")}, nil
	}
	samples, err := RunPingProbe(context.Background(), ProbeTarget{
		ID: "safe-host", Type: "ping", Address: "safe.example.com", Count: 2, TimeoutMS: 1000,
	})
	if err != nil || len(samples) != 2 || !samples[0].Success || !samples[1].Success {
		t.Fatalf("safe ping samples = %+v, err = %v", samples, err)
	}
	if len(capturedIPs) != 2 || capturedIPs[0] != "203.0.113.25" || capturedIPs[1] != "203.0.113.25" {
		t.Fatalf("ping received args = %v, want verified literal IP 203.0.113.25", capturedIPs)
	}

	// Dual-stack host on an IPv4-only machine must fall back to the reachable family and stay on it.
	var familyCaptured []string
	localPingLookupNetIP = func(_ context.Context, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("203.0.113.42")}, nil
	}
	localPingExecCommand = func(_ context.Context, ipLiteral string, isV6 bool, _ time.Duration) ([]byte, error) {
		familyCaptured = append(familyCaptured, ipLiteral)
		if isV6 {
			return nil, errors.New("connect: network is unreachable")
		}
		return []byte("64 bytes from " + ipLiteral + ": icmp_seq=1 ttl=56 time=9.5 ms\n"), nil
	}
	dualSamples, dualErr := RunPingProbe(context.Background(), ProbeTarget{
		ID: "dual-stack", Type: "ping", Address: "dual.example.com", Count: 3, TimeoutMS: 1000,
	})
	if dualErr != nil || len(dualSamples) != 3 || !dualSamples[0].Success || !dualSamples[1].Success || !dualSamples[2].Success {
		t.Fatalf("dual-stack fallback samples = %+v, err = %v", dualSamples, dualErr)
	}
	if len(familyCaptured) != 4 || familyCaptured[0] != "2001:db8::1" || familyCaptured[1] != "203.0.113.42" || familyCaptured[2] != "203.0.113.42" || familyCaptured[3] != "203.0.113.42" {
		t.Fatalf("dual-stack ping sequence = %v, want 1 IPv6 probe followed by 3 IPv4 probes", familyCaptured)
	}
}

func TestAPIResponsesMarshalEmptySlicesWithoutNull(t *testing.T) {
	cases := []struct {
		name      string
		payload   any
		mustHave  string
	}{
		{name: "AgentProbeTargetsResponse", payload: AgentProbeTargetsResponse{Version: 3}, mustHave: `"targets":[]`},
		{name: "AdminAlertRule", payload: AdminAlertRule{ID: "cpu_high"}, mustHave: `"scope_node_ids":[]`},
		{name: "AdminAlertRulesResponse", payload: AdminAlertRulesResponse{}, mustHave: `"rules":[]`},
		{name: "AdminAlertRuleResponse", payload: AdminAlertRuleResponse{Rule: AdminAlertRule{ID: "node_offline"}}, mustHave: `"scope_node_ids":[]`},
		{name: "AdminNotificationChannelsResponse", payload: AdminNotificationChannelsResponse{}, mustHave: `"channels":[]`},
		{name: "AdminNodesResponse", payload: AdminNodesResponse{}, mustHave: `"nodes":[]`},
		{name: "AdminProbeTargetsResponse", payload: AdminProbeTargetsResponse{}, mustHave: `"targets":[]`},
		{name: "AdminProbeTarget", payload: AdminProbeTarget{ID: "t1"}, mustHave: `"assignments":[]`},
		{name: "SummaryResponse", payload: SummaryResponse{}, mustHave: `"nodes":[]`},
		{name: "LatencyResponse", payload: LatencyResponse{NodeID: "n1", Range: "1h"}, mustHave: `"series":[]`},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(tc.payload)
		if err != nil {
			t.Fatalf("%s json.Marshal failed: %v", tc.name, err)
		}
		encoded := string(raw)
		if !strings.Contains(encoded, tc.mustHave) {
			t.Fatalf("%s json = %s, missing %s", tc.name, encoded, tc.mustHave)
		}
		// AdminProbeTarget.Port is a nullable scalar (*int), whereas array fields must never be null.
		if tc.name != "AdminProbeTarget" && strings.Contains(encoded, ":null") {
			t.Fatalf("%s json contains null field: %s", tc.name, encoded)
		}
		if strings.Contains(encoded, `"targets":null`) || strings.Contains(encoded, `"scope_node_ids":null`) ||
			strings.Contains(encoded, `"assignments":null`) || strings.Contains(encoded, `"nodes":null`) ||
			strings.Contains(encoded, `"rules":null`) || strings.Contains(encoded, `"channels":null`) ||
			strings.Contains(encoded, `"services":null`) || strings.Contains(encoded, `"latency_points":null`) ||
			strings.Contains(encoded, `"series":null`) {
			t.Fatalf("%s json contains null array: %s", tc.name, encoded)
		}
	}
}
