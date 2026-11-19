package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONBodyRejectsTrailingContent(t *testing.T) {
	cases := []string{
		`{"name":"first"}{"name":"second"}`,
		`{"name":"first"} trailing`,
	}
	for _, body := range cases {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		var payload struct {
			Name string `json:"name"`
		}
		if decodeJSONBody(recorder, request, &payload, 1024, true) {
			t.Fatalf("decodeJSONBody accepted trailing content %q", body)
		}
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 for %q", recorder.Code, body)
		}
	}
}

func TestDecodeJSONBodyAllowsTrailingWhitespace(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{\"name\":\"ok\"}\n\t "))
	var payload struct {
		Name string `json:"name"`
	}
	if !decodeJSONBody(recorder, request, &payload, 1024, true) {
		t.Fatalf("decodeJSONBody rejected valid JSON with whitespace; status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if payload.Name != "ok" {
		t.Fatalf("decoded name = %q, want ok", payload.Name)
	}
}

func TestWriteRawJSONForRequestOptionalGzipCompression(t *testing.T) {
	h := newMockHandler()

	// 1. Without Accept-Encoding, large public responses stay plain JSON.
	plainRec := httptest.NewRecorder()
	plainReq := httptest.NewRequest(http.MethodGet, "/api/public/v1/nodes/example-node-a/state?range=1d", nil)
	h.ServeHTTP(plainRec, plainReq)
	if plainRec.Code != http.StatusOK {
		t.Fatalf("plain status = %d, want 200", plainRec.Code)
	}
	if enc := plainRec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("plain Content-Encoding = %q, want empty", enc)
	}
	plainBytes := plainRec.Body.Bytes()
	if len(plainBytes) < gzipMinPayloadBytes {
		t.Fatalf("expected large payload >= %d, got %d", gzipMinPayloadBytes, len(plainBytes))
	}

	// 2. With Accept-Encoding: gzip, >= 1024B payload is gzip-compressed and decodes identically.
	gzipRec := httptest.NewRecorder()
	gzipReq := httptest.NewRequest(http.MethodGet, "/api/public/v1/nodes/example-node-a/state?range=1d", nil)
	gzipReq.Header.Set("Accept-Encoding", "br, gzip;q=0.9")
	h.ServeHTTP(gzipRec, gzipReq)
	if gzipRec.Code != http.StatusOK {
		t.Fatalf("gzip status = %d, want 200", gzipRec.Code)
	}
	if got := gzipRec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("gzip Content-Encoding = %q, want gzip", got)
	}
	if !strings.Contains(strings.Join(gzipRec.Header().Values("Vary"), ","), "Accept-Encoding") {
		t.Fatalf("gzip Vary = %v, want Accept-Encoding", gzipRec.Header().Values("Vary"))
	}
	if gzipRec.Body.Len() >= len(plainBytes)/2 {
		t.Fatalf("expected significant compression: compressed=%d plain=%d", gzipRec.Body.Len(), len(plainBytes))
	}
	zr, err := gzip.NewReader(bytes.NewReader(gzipRec.Body.Bytes()))
	if err != nil {
		t.Fatalf("open gzip reader: %v", err)
	}
	decoded, err := io.ReadAll(zr)
	_ = zr.Close()
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	if !bytes.Equal(decoded, plainBytes) {
		t.Fatalf("gunzipped bytes len=%d do not match plain JSON bytes len=%d", len(decoded), len(plainBytes))
	}

	// 3. Explicit q=0 disables gzip.
	qZeroRec := httptest.NewRecorder()
	qZeroReq := httptest.NewRequest(http.MethodGet, "/api/public/v1/nodes/example-node-a/state?range=1d", nil)
	qZeroReq.Header.Set("Accept-Encoding", "gzip;q=0")
	h.ServeHTTP(qZeroRec, qZeroReq)
	if enc := qZeroRec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("q=0 Content-Encoding = %q, want empty", enc)
	}
	if !bytes.Equal(qZeroRec.Body.Bytes(), plainBytes) {
		t.Fatal("q=0 response body did not match plain JSON")
	}

	// 4. Payloads < 1024B remain uncompressed even when Accept-Encoding: gzip is sent.
	smallRec := httptest.NewRecorder()
	smallReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	smallReq.Header.Set("Accept-Encoding", "gzip")
	writeRawJSONForRequest(smallRec, smallReq, http.StatusOK, []byte(`{"ok":true}`))
	if enc := smallRec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("small payload Content-Encoding = %q, want empty", enc)
	}
	if smallRec.Body.String() != `{"ok":true}` {
		t.Fatalf("small payload body = %q", smallRec.Body.String())
	}
}

