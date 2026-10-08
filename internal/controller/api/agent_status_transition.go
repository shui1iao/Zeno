package api

import (
	"context"
	"time"
)

// Agent status writers still return the stored-status transition they
// committed. Notifications no longer consume it: the reconcile loop reads the
// stored state itself after being woken.

type heartbeatTransitionStore interface {
	RecordAgentHeartbeatTransition(ctx context.Context, nodeID string, ts time.Time, status, agentVersion string) (notificationStatusTransition, error)
}

type notificationNodeSnapshot struct {
	ID          string
	DisplayName string
	Status      string
	PublicIPv4  string
}

type notificationStatusTransition struct {
	Previous notificationNodeSnapshot
	Current  notificationNodeSnapshot
	Detail   string
}
