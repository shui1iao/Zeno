package api

import (
	"context"
	"sync"
	"time"
)

type summaryAggregateFlight struct {
	generation  uint64
	done        chan struct{}
	home        map[string]*LatencySummary
	services    []ServiceTarget
	nodeLatency map[string][]LatencySummary
	err         error
}

type sqliteSummaryCache struct {
	mu          sync.Mutex
	updated     time.Time
	home        map[string]*LatencySummary
	services    []ServiceTarget
	nodeLatency map[string][]LatencySummary
	generation  uint64
	flight      *summaryAggregateFlight
}

func (s *sqliteMonitoringDomain) Summary(ctx context.Context) (SummaryResponse, error) {
	nodes, err := s.nodes(ctx)
	if err != nil {
		return SummaryResponse{}, err
	}
	if nodes == nil {
		nodes = []Node{}
	}
	homeSummaries, services, latencySummaries, err := s.summaryAggregates(ctx)
	if err != nil {
		return SummaryResponse{}, err
	}
	if services == nil {
		services = []ServiceTarget{}
	}
	exchangeRates, err := s.exchangeRateSnapshot(ctx)
	if err != nil {
		return SummaryResponse{}, err
	}
	for index := range nodes {
		nodes[index].LatencySummary = homeSummaries[nodes[index].ID]
		if summaries, ok := latencySummaries[nodes[index].ID]; ok {
			nodes[index].LatencySummaries = summaries
		} else {
			nodes[index].LatencySummaries = []LatencySummary{}
		}
	}
	return SummaryResponse{Nodes: nodes, Services: services, LatencyPoints: []LatencyPoint{}, ExchangeRates: exchangeRates}, nil
}

func (s *sqliteMonitoringDomain) summaryAggregates(ctx context.Context) (map[string]*LatencySummary, []ServiceTarget, map[string][]LatencySummary, error) {
	retries := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		now := time.Now()
		s.summaryCache.mu.Lock()
		if !s.summaryCache.updated.IsZero() && now.Sub(s.summaryCache.updated) < summaryAggregateFreshFor {
			home := s.summaryCache.home
			services := s.summaryCache.services
			nodeLatency := s.summaryCache.nodeLatency
			s.summaryCache.mu.Unlock()
			return home, services, nodeLatency, nil
		}
		generation := s.summaryCache.generation
		if flight := s.summaryCache.flight; flight != nil {
			s.summaryCache.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, nil, nil, ctx.Err()
			case <-flight.done:
			}
			s.summaryCache.mu.Lock()
			currentGeneration := s.summaryCache.generation
			s.summaryCache.mu.Unlock()
			if currentGeneration != flight.generation {
				if retries < summaryGenerationMaxRetries {
					retries++
					continue
				}
				return flight.home, flight.services, flight.nodeLatency, flight.err
			}
			return flight.home, flight.services, flight.nodeLatency, flight.err
		}
		flight := &summaryAggregateFlight{generation: generation, done: make(chan struct{})}
		s.summaryCache.flight = flight
		s.summaryCache.mu.Unlock()

		// These are the expensive rolling queries. They intentionally run outside
		// the summary cache mutex so Agent probe writes can return 202 without waiting
		// for a 24-hour scan.
		homeSummaries, err := s.latestHomeLatencySummaries(ctx)
		var services []ServiceTarget
		if err == nil {
			services, err = s.serviceTargets(ctx)
		}
		var latencySummaries map[string][]LatencySummary
		if err == nil {
			latencySummaries, err = s.latestLatencySummariesByNode(ctx)
		}

		s.summaryCache.mu.Lock()
		currentGeneration := s.summaryCache.generation
		if err == nil && currentGeneration == generation {
			s.summaryCache.home = homeSummaries
			s.summaryCache.services = services
			s.summaryCache.nodeLatency = latencySummaries
			s.summaryCache.updated = time.Now()
		}
		flight.home = homeSummaries
		flight.services = services
		flight.nodeLatency = latencySummaries
		flight.err = err
		if s.summaryCache.flight == flight {
			s.summaryCache.flight = nil
		}
		close(flight.done)
		s.summaryCache.mu.Unlock()

		if currentGeneration != generation {
			// Invalidation raced this snapshot. Do not let it repopulate the cache;
			// retry once in the current generation. If writes continue, the second
			// completed query is still a usable point-in-time snapshot and must be
			// returned rather than allowing unbounded aggregate rebuilds.
			if retries < summaryGenerationMaxRetries {
				retries++
				continue
			}
			return homeSummaries, services, latencySummaries, err
		}
		return homeSummaries, services, latencySummaries, err
	}
}

func (s *sqliteMonitoringDomain) invalidateSummaryAggregates() {
	s.summaryCache.mu.Lock()
	s.summaryCache.generation++
	s.summaryCache.updated = time.Time{}
	s.summaryCache.home = nil
	s.summaryCache.services = nil
	s.summaryCache.nodeLatency = nil
	s.summaryCache.mu.Unlock()
}

func (s *sqliteMonitoringDomain) NodeLatency(ctx context.Context, nodeID string, window latencyWindow) (LatencyResponse, error) {
	points, err := nodeWindowPoints(ctx, s, nodeID, window, s.latencyPoints)
	if err != nil {
		return LatencyResponse{}, err
	}
	return LatencyResponse{NodeID: nodeID, Range: window.Name, Points: points}, nil
}

func (s *sqliteMonitoringDomain) NodeState(ctx context.Context, nodeID string, window latencyWindow) (StateResponse, error) {
	points, err := nodeWindowPoints(ctx, s, nodeID, window, s.statePoints)
	if err != nil {
		return StateResponse{}, err
	}
	return StateResponse{NodeID: nodeID, Range: window.Name, Points: points}, nil
}

func nodeWindowPoints[T any](ctx context.Context, store *sqliteMonitoringDomain, nodeID string, window latencyWindow, query func(context.Context, string, latencyWindow) ([]T, error)) ([]T, error) {
	exists, err := store.nodeExists(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errNodeNotFound
	}
	return query(ctx, nodeID, window)
}

const activeAdminNodeExistsSQL = `
	SELECT 1 FROM nodes n
	WHERE n.id = ?
	  AND NOT EXISTS (
		SELECT 1 FROM admin_deletion_jobs deletion
		WHERE deletion.entity_kind = 'node'
		  AND deletion.entity_id = n.id
		  AND deletion.state IN ('pending', 'running')
	  )
`

const activeAdminProbeTargetExistsSQL = `
	SELECT 1 FROM probe_targets pt
	WHERE pt.id = ?
	  AND NOT EXISTS (
		SELECT 1 FROM admin_deletion_jobs deletion
		WHERE deletion.entity_kind = 'probe_target'
		  AND deletion.entity_id = pt.id
		  AND deletion.state IN ('pending', 'running')
	  )
`
