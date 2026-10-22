package api

import (
	"context"
	"errors"
)

var (
	errNodeNotFound       = errors.New("node not found")
	errStoreNotConfigured = errors.New("controller store not configured")
)

type Store interface {
	Summary(ctx context.Context) (SummaryResponse, error)
	PublicSettings(ctx context.Context) (SiteSettings, error)
	NodeLatency(ctx context.Context, nodeID string, window latencyWindow) (LatencyResponse, error)
	NodeState(ctx context.Context, nodeID string, window latencyWindow) (StateResponse, error)
}

// unconfiguredStore keeps direct Handler construction fail-closed. The shipped
// Controller requires -db before it builds a Handler, while package callers
// that omit Store receive explicit readiness and API failures instead of
// plausible preview data.
type unconfiguredStore struct{}

func (unconfiguredStore) Ready(context.Context) error { return errStoreNotConfigured }
func (unconfiguredStore) Summary(context.Context) (SummaryResponse, error) {
	return SummaryResponse{}, errStoreNotConfigured
}
func (unconfiguredStore) PublicSettings(context.Context) (SiteSettings, error) {
	return SiteSettings{}, errStoreNotConfigured
}
func (unconfiguredStore) NodeLatency(context.Context, string, latencyWindow) (LatencyResponse, error) {
	return LatencyResponse{}, errStoreNotConfigured
}
func (unconfiguredStore) NodeState(context.Context, string, latencyWindow) (StateResponse, error) {
	return StateResponse{}, errStoreNotConfigured
}
