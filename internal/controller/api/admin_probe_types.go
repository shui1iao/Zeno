package api

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

type AdminProbeTargetsResponse struct {
	Targets []AdminProbeTarget `json:"targets"`
}

func (response AdminProbeTargetsResponse) MarshalJSON() ([]byte, error) {
	type alias AdminProbeTargetsResponse
	if response.Targets == nil {
		response.Targets = []AdminProbeTarget{}
	}
	return json.Marshal(alias(response))
}

// AdminProbeTargetReorderRequest carries the complete visible target order so
// the backend can commit it in one transaction.
type AdminProbeTargetReorderRequest struct {
	TargetIDs []string `json:"target_ids"`
}

func (request *AdminProbeTargetReorderRequest) normalize() error {
	return normalizeAdminOrderIDs(request.TargetIDs, errInvalidAdminTargetWrite)
}

type AdminProbeTargetResponse struct {
	Target AdminProbeTarget `json:"target"`
}

type AdminProbeTargetCreateRequest struct {
	ID           string                             `json:"id,omitempty"`
	Name         string                             `json:"name"`
	Type         string                             `json:"type"`
	Address      string                             `json:"address"`
	Port         adminOptionalInt64                 `json:"port,omitempty"`
	Count        int                                `json:"count"`
	TimeoutMS    int                                `json:"timeout_ms"`
	IntervalSec  int                                `json:"interval_sec"`
	DisplayOrder int                                `json:"display_order,omitempty"`
	Assignments  []AdminProbeTargetAssignmentUpdate `json:"assignments,omitempty"`
}

func (request *AdminProbeTargetCreateRequest) normalize() error {
	request.ID = normalizeAdminNodeID(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	normalizedType, ok := normalizeAdminProbeTargetType(request.Type)
	request.Type = normalizedType
	request.Address = strings.TrimSpace(request.Address)
	if request.Name == "" || request.Address == "" || !ok || !validProbeTargetResourceConfig(request.Count, request.TimeoutMS, request.IntervalSec) {
		return errInvalidAdminTargetWrite
	}
	if request.DisplayOrder < 0 {
		return errInvalidAdminTargetWrite
	}
	if request.Assignments != nil {
		seen := map[string]struct{}{}
		for index := range request.Assignments {
			trimmed := strings.TrimSpace(request.Assignments[index].NodeID)
			if trimmed == "" {
				return errInvalidAdminTargetWrite
			}
			if _, exists := seen[trimmed]; exists {
				return errInvalidAdminTargetWrite
			}
			seen[trimmed] = struct{}{}
			request.Assignments[index].NodeID = trimmed
		}
	}
	if request.Type == "tcping" {
		if !request.Port.Set || !request.Port.Valid || !validPort(request.Port.Value) {
			return errInvalidAdminTargetWrite
		}
		return nil
	}
	if request.Type == "http_get" && !validHTTPGetTargetAddress(request.Address) {
		return errInvalidAdminTargetWrite
	}
	if request.Type == "ping" && !validPingTargetAddress(request.Address) {
		return errInvalidAdminTargetWrite
	}
	if request.Port.Set && request.Port.Valid {
		return errInvalidAdminTargetWrite
	}
	request.Port.Set = true
	request.Port.Valid = false
	request.Port.Value = 0
	return nil
}

type AdminProbeTargetUpdateRequest struct {
	Name         *string                            `json:"name,omitempty"`
	Type         *string                            `json:"type,omitempty"`
	Address      *string                            `json:"address,omitempty"`
	Port         adminOptionalInt64                 `json:"port,omitempty"`
	Count        *int                               `json:"count,omitempty"`
	TimeoutMS    *int                               `json:"timeout_ms,omitempty"`
	IntervalSec  *int                               `json:"interval_sec,omitempty"`
	DisplayOrder *int                               `json:"display_order,omitempty"`
	Assignments  []AdminProbeTargetAssignmentUpdate `json:"assignments,omitempty"`
}

type AdminProbeTargetAssignmentUpdate struct {
	NodeID  string `json:"node_id"`
	Enabled bool   `json:"enabled"`
}

func (request *AdminProbeTargetUpdateRequest) normalize() error {
	normalizer := newPatchNormalizer(errInvalidAdminTargetWrite)
	normalizer.text(&request.Name, trimRequired)
	// Changing the type can force the port field: portless types must clear a
	// stored port, and supplying both a portless type and a port is a conflict
	// rather than a silent override.
	normalizer.text(&request.Type, func(value string) (string, bool) {
		normalizedType, ok := normalizeAdminProbeTargetType(value)
		if !ok {
			return "", false
		}
		if normalizedType == "ping" || normalizedType == "http_get" {
			if request.Port.Set && request.Port.Valid {
				return "", false
			}
			request.Port = adminOptionalInt64{Set: true, Valid: false, Value: 0}
		}
		return normalizedType, true
	})
	normalizer.text(&request.Address, trimRequired)
	normalizer.optionalInt64(&request.Port, validPort)
	normalizer.number(&request.Count, boundedInt(1, maxProbeTargetCount))
	normalizer.number(&request.TimeoutMS, boundedInt(minProbeTargetTimeoutMS, maxProbeTargetTimeoutMS))
	normalizer.number(&request.IntervalSec, boundedInt(minProbeTargetIntervalSec, maxProbeTargetIntervalSec))
	normalizer.number(&request.DisplayOrder, nonNegativeInt)
	normalizeProbeTargetAssignments(normalizer, request)
	return normalizer.result()
}

// normalizeProbeTargetAssignments trims node ids and rejects an empty or
// duplicated list, so one request cannot assign the same node twice.
func normalizeProbeTargetAssignments(normalizer *patchNormalizer, request *AdminProbeTargetUpdateRequest) {
	if request.Assignments == nil {
		return
	}
	normalizer.present(true)
	if !normalizer.active() {
		return
	}
	if len(request.Assignments) == 0 {
		normalizer.fail()
		return
	}
	seen := make(map[string]struct{}, len(request.Assignments))
	for index := range request.Assignments {
		trimmed := strings.TrimSpace(request.Assignments[index].NodeID)
		if trimmed == "" {
			normalizer.fail()
			return
		}
		if _, exists := seen[trimmed]; exists {
			normalizer.fail()
			return
		}
		seen[trimmed] = struct{}{}
		request.Assignments[index].NodeID = trimmed
	}
}

func normalizeAdminProbeTargetType(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tcp", "tcping":
		return "tcping", true
	case "icmp", "ping":
		return "ping", true
	case "http", "https", "http_get", "http-get":
		return "http_get", true
	default:
		return "", false
	}
}

func validHTTPGetTargetAddress(address string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(address))
	if err != nil {
		return false
	}
	return validHTTPProbeURL(parsed)
}

func validHTTPProbeURL(parsed *url.URL) bool {
	if parsed == nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || !validPort(int64(port)) {
			return false
		}
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return true
	case "http":
		return loopbackURLHost(parsed.Hostname()) || directIPURLHost(parsed)
	default:
		return false
	}
}

func validPingTargetAddress(address string) bool {
	address = strings.TrimSpace(address)
	return address != "" && !strings.HasPrefix(address, "-")
}

func validPort(port int64) bool {
	return port > 0 && port <= 65535
}

type AdminProbeTarget struct {
	ID           string                       `json:"id"`
	Name         string                       `json:"name"`
	Type         string                       `json:"type"`
	Address      string                       `json:"address"`
	Port         *int                         `json:"port"`
	Count        int                          `json:"count"`
	TimeoutMS    int                          `json:"timeout_ms"`
	IntervalSec  int                          `json:"interval_sec"`
	DisplayOrder int                          `json:"display_order"`
	Assignments  []AdminProbeTargetAssignment `json:"assignments"`
}

func (target AdminProbeTarget) MarshalJSON() ([]byte, error) {
	type alias AdminProbeTarget
	if target.Assignments == nil {
		target.Assignments = []AdminProbeTargetAssignment{}
	}
	return json.Marshal(alias(target))
}

type AdminProbeTargetAssignment struct {
	NodeID          string `json:"node_id"`
	NodeDisplayName string `json:"node_display_name"`
	Enabled         bool   `json:"enabled"`
}
