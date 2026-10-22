package api

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestLiveDetailInitialPayloadMatchesCompactContractBytes(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantBytes  int
		wantSHA256 string
		handle     func(*handler, http.ResponseWriter, *http.Request)
	}{
		{name: "node-state", path: "/api/public/v1/nodes/example-node-a/state/ws?range=1d", wantBytes: 468859, wantSHA256: "e9b01e10276ba317d02db5f4f230fc66fe460dfa3995952c6e73ca695eb9998b", handle: (*handler).handlePublicNodeResource},
		{name: "node-latency", path: "/api/public/v1/nodes/example-node-a/latency/ws?range=1h", wantBytes: 5621, wantSHA256: "6bf6802b92ea41d5e13e28207a5d52efc34461c8ee1f7d65f17629624078c63c", handle: (*handler).handlePublicNodeResource},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := &handler{
				store:          mockStore{},
				liveHub:        newLiveUpdateHub(),
				publicWSGate:   newWebSocketGateWithPerKey(8, 2),
				detailCache:    newDetailJSONCache(),
				trustedProxies: TrustedProxySet{},
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				test.handle(h, w, r)
			}))
			defer server.Close()

			conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+test.path, nil)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			if response != nil && response.Body != nil {
				defer response.Body.Close()
			}
			defer conn.Close()

			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("read initial payload: %v", err)
			}
			gotSHA256 := fmt.Sprintf("%x", sha256.Sum256(payload))
			if len(payload) != test.wantBytes || gotSHA256 != test.wantSHA256 {
				t.Fatalf("initial payload bytes=%d sha256=%s, want bytes=%d sha256=%s", len(payload), gotSHA256, test.wantBytes, test.wantSHA256)
			}
		})
	}
}
