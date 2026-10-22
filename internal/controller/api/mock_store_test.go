package api

import (
	"context"
	"net/http"
)

type mockStore struct{}

func (mockStore) Summary(context.Context) (SummaryResponse, error) {
	return SummaryResponse{Nodes: mockNodes(), Services: mockServiceTargets(), LatencyPoints: []LatencyPoint{}, ExchangeRates: map[string]float64{"CNY": 1}}, nil
}

func (mockStore) PublicSettings(context.Context) (SiteSettings, error) {
	return defaultSiteSettings(), nil
}

func (mockStore) NodeLatency(_ context.Context, nodeID string, window latencyWindow) (LatencyResponse, error) {
	if !mockNodeExists(nodeID) {
		return LatencyResponse{}, errNodeNotFound
	}
	return LatencyResponse{NodeID: nodeID, Range: window.Name, Points: mockLatencyPoints(nodeID, window.Name)}, nil
}

func (mockStore) NodeState(_ context.Context, nodeID string, window latencyWindow) (StateResponse, error) {
	if !mockNodeExists(nodeID) {
		return StateResponse{}, errNodeNotFound
	}
	return StateResponse{NodeID: nodeID, Range: window.Name, Points: mockStatePoints(window)}, nil
}

func mockNodeExists(nodeID string) bool {
	for _, node := range mockNodes() {
		if node.ID == nodeID {
			return true
		}
	}
	return false
}

func newMockHandler(options ...HandlerOptions) http.Handler {
	opts := HandlerOptions{Store: mockStore{}}
	if len(options) > 0 {
		opts = options[0]
		opts.Store = mockStore{}
	}
	return NewHandler(opts)
}
