package api

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const gzipMinPayloadBytes = 1024

var gzipWriterPool = sync.Pool{
	New: func() any {
		writer, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return writer
	},
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeRawJSONForRequest(w http.ResponseWriter, r *http.Request, status int, payload []byte) {
	header := w.Header()
	header.Set("Content-Type", "application/json; charset=utf-8")
	if len(payload) >= gzipMinPayloadBytes && header.Get("Content-Encoding") == "" {
		addVaryValue(header, "Accept-Encoding")
		if requestAcceptsGzip(r) {
			header.Set("Content-Encoding", "gzip")
			header.Del("Content-Length")
			w.WriteHeader(status)
			gz := gzipWriterPool.Get().(*gzip.Writer)
			gz.Reset(w)
			_, _ = gz.Write(payload)
			_ = gz.Close()
			gz.Reset(io.Discard)
			gzipWriterPool.Put(gz)
			return
		}
	}
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func addVaryValue(header http.Header, token string) {
	for _, existing := range header.Values("Vary") {
		for _, part := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return
			}
		}
	}
	header.Add("Vary", token)
}

func requestAcceptsGzip(r *http.Request) bool {
	if r == nil || r.Method == http.MethodHead {
		return false
	}
	for _, headerValue := range r.Header.Values("Accept-Encoding") {
		for _, part := range strings.Split(headerValue, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			segments := strings.Split(part, ";")
			if !strings.EqualFold(strings.TrimSpace(segments[0]), "gzip") {
				continue
			}
			accepted := true
			for _, param := range segments[1:] {
				param = strings.TrimSpace(param)
				if len(param) >= 2 && (param[0] == 'q' || param[0] == 'Q') && param[1] == '=' {
					q, err := strconv.ParseFloat(strings.TrimSpace(param[2:]), 64)
					if err != nil || q <= 0 {
						accepted = false
						break
					}
				}
			}
			if accepted {
				return true
			}
		}
	}
	return false
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, errNodeNotFound) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	if errors.Is(err, errProbeTargetNotFound) {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal error")
}
