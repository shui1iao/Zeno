package api

import (
	"context"
	"errors"
	"log"
	"sort"
	"sync"
	"time"
)

// One reconcile goroutine compares, for every enabled channel, node and kind,
// what the user was last told (notification_states.notified) with the actual
// state read from nodes, alert_rule_states and the renewal rules. A
// difference produces one message built from the current actual state;
// notified advances only after Telegram confirms delivery.

const (
	defaultNotificationReconcileInterval = 2 * time.Second
	notificationReconcileWriteKey        = "_notification_reconcile"
)

// notificationRetryDelays is the per-channel backoff after consecutive
// failures; the last value repeats.
var notificationRetryDelays = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}

type notificationReconciler struct {
	wake chan struct{}
	// mu serializes reconcile rounds, so sends to one channel are serial.
	mu      sync.Mutex
	backoff map[string]notificationChannelBackoff
}

type notificationChannelBackoff struct {
	failures  int
	notBefore time.Time
}

func newNotificationReconciler() *notificationReconciler {
	return &notificationReconciler{wake: make(chan struct{}, 1), backoff: map[string]notificationChannelBackoff{}}
}

func (h *handler) notificationNow() time.Time {
	if h.notificationClock != nil {
		return h.notificationClock().UTC()
	}
	return time.Now().UTC()
}

func (h *handler) notificationLocation() *time.Location {
	if h.notificationLoc != nil {
		return h.notificationLoc
	}
	return time.Local
}

func (h *handler) notificationTimeout() time.Duration {
	if h.notificationSendTimeout > 0 {
		return h.notificationSendTimeout
	}
	return notificationSendTimeout
}

func (h *handler) reconciler() *notificationReconciler {
	h.notificationReconcilerOnce.Do(func() {
		if h.notificationReconcilerState == nil {
			h.notificationReconcilerState = newNotificationReconciler()
		}
	})
	return h.notificationReconcilerState
}

// wakeNotificationReconcile asks the reconcile loop for an early round after a
// state change was committed. It never blocks.
func (h *handler) wakeNotificationReconcile() {
	if h == nil {
		return
	}
	select {
	case h.reconciler().wake <- struct{}{}:
	default:
	}
}

func (h *handler) runNotificationReconcileLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = defaultNotificationReconcileInterval
	}
	reconciler := h.reconciler()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	h.reconcileNotifications(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-reconciler.wake:
		}
		h.reconcileNotifications(ctx)
	}
}

// reconcileNotifications runs one round. It is safe to call concurrently;
// rounds are serialized.
func (h *handler) reconcileNotifications(ctx context.Context) {
	if ctx.Err() != nil || !h.automaticNotificationsAllowed() {
		return
	}
	store, ok := h.store.(notificationReconcileStore)
	if !ok {
		return
	}
	reconciler := h.reconciler()
	reconciler.mu.Lock()
	defer reconciler.mu.Unlock()
	now := h.notificationNow()
	snapshot, err := store.NotificationReconcileSnapshot(ctx, now)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("notification reconcile snapshot failed: %v", err)
		}
		return
	}
	plan := h.planNotificationRound(snapshot, now)
	if err := store.ApplyNotificationStates(ctx, plan.rows, plan.logs); err != nil {
		if ctx.Err() == nil {
			log.Printf("notification reconcile state write failed: %v", err)
		}
		return
	}
	logNotificationOutcomes(plan.logs)
	for _, channelID := range plan.channelOrder {
		h.sendNotificationQueue(ctx, store, reconciler, plan.sends[channelID], now)
	}
}

type notificationSendItem struct {
	channel notificationReconcileChannel
	node    notificationReconcileNode
	kind    string
	row     notificationStateRow
	detail  string
	text    string
}

type notificationRoundPlan struct {
	rows         []notificationStateRow
	logs         []notificationLogEntry
	sends        map[string][]notificationSendItem
	channelOrder []string
}

func (h *handler) planNotificationRound(snapshot notificationReconcileSnapshot, now time.Time) notificationRoundPlan {
	plan := notificationRoundPlan{sends: map[string][]notificationSendItem{}}
	for _, channel := range snapshot.Channels {
		if !channel.Enabled {
			// A disabled channel pauses its rows: nothing is sent or advanced.
			continue
		}
		for _, node := range snapshot.Nodes {
			for _, kind := range notificationKinds {
				h.planNotificationItem(&plan, snapshot, channel, node, kind, now)
			}
		}
	}
	for channelID := range plan.sends {
		plan.channelOrder = append(plan.channelOrder, channelID)
	}
	sort.Strings(plan.channelOrder)
	return plan
}

func (h *handler) planNotificationItem(plan *notificationRoundPlan, snapshot notificationReconcileSnapshot, channel notificationReconcileChannel, node notificationReconcileNode, kind string, now time.Time) {
	key := notificationStateKey{ChannelID: channel.ID, NodeID: node.ID, Kind: kind}
	row, hasRow := snapshot.States[key]
	rules := snapshot.rulesFor(kind, node.ID)
	input := notificationDecisionInput{
		Kind: kind, ChannelID: channel.ID, NodeID: node.ID, Now: now.Unix(),
		HasRow: hasRow, Row: row,
		ChannelVersion: channel.DeliveryVersion, ChannelFingerprint: channel.DestinationFingerprint,
		Paused:      node.Disabled || len(rules) == 0,
		LastSeenAt:  node.LastSeenAt,
		HoldSeconds: notificationHoldSeconds(rules),
	}
	switch kind {
	case notificationKindLiveness:
		input.Actual, input.ActualKnown = livenessActual(node.Status)
		input.BaselineActual = input.Actual
	case notificationKindResource:
		names := snapshot.ResourceActive[node.ID]
		input.ActualKnown = true
		input.Actual = resourceStateOK
		if len(names) > 0 {
			input.Actual = resourceStateWarning
			input.Detail = joinResourceAlertNames(names)
		}
		input.BaselineActual = input.Actual
	case notificationKindRenewal:
		input.ActualKnown = true
		input.Actual = renewalNotificationKeyAt(node, rules, now, true)
		input.BaselineActual = renewalNotificationKeyAt(node, rules, now, false)
	}
	decision := decideNotification(input)
	next := decision.Next
	next.ChannelID, next.NodeID, next.Kind = channel.ID, node.ID, kind
	if decision.Changed {
		next.UpdatedAt = now.Unix()
	}
	entry := notificationLogEntry{TS: now.Unix(), ChannelID: channel.ID, NodeID: node.ID, NodeName: node.Name, Kind: kind}
	switch decision.Baseline {
	case notificationActionBaseline:
		baselineEntry := entry
		baselineEntry.Outcome = notificationOutcomeBaseline
		baselineEntry.To = input.BaselineActual
		plan.logs = append(plan.logs, baselineEntry)
	case notificationActionRebaseline:
		baselineEntry := entry
		baselineEntry.Outcome = notificationOutcomeRebaseline
		baselineEntry.From = row.Notified
		baselineEntry.To = input.BaselineActual
		plan.logs = append(plan.logs, baselineEntry)
	}
	if decision.Action == notificationActionDrop {
		// Only a stored row can carry a failed pending target; a (re)baseline
		// row never does.
		dropEntry := entry
		dropEntry.Outcome = notificationOutcomeDropped
		dropEntry.From = row.Notified
		dropEntry.To = row.PendingTarget
		dropEntry.Attempt = row.PendingAttempts
		dropEntry.Error = row.LastError
		dropEntry.Message = notificationDroppedText(kind, h.notificationLabel(kind, node), row, now, h.notificationLocation())
		plan.logs = append(plan.logs, dropEntry)
	}
	if decision.Changed {
		plan.rows = append(plan.rows, next)
	}
	if decision.Action.sends() {
		item := notificationSendItem{channel: channel, node: node, kind: kind, row: next, detail: input.Detail}
		item.text = h.notificationMessageText(item, now)
		plan.sends[channel.ID] = append(plan.sends[channel.ID], item)
	}
}

func livenessActual(status string) (string, bool) {
	switch status {
	case "offline":
		return livenessStateOffline, true
	case "online", "warning":
		return livenessStateOnline, true
	default:
		return "", false
	}
}

func (h *handler) notificationLabel(kind string, node notificationReconcileNode) string {
	if kind == notificationKindRenewal {
		// Renewal reminders never carried the node address.
		return notificationNodeLabel(node.Name, node.ID, "")
	}
	return notificationNodeLabel(node.Name, node.ID, node.IPv4)
}

// notificationMessageText renders the message from the actual state at send
// time.
func (h *handler) notificationMessageText(item notificationSendItem, now time.Time) string {
	label := h.notificationLabel(item.kind, item.node)
	loc := h.notificationLocation()
	switch item.kind {
	case notificationKindLiveness:
		if item.row.PendingTarget == livenessStateOffline {
			return livenessAlertText(label)
		}
		return livenessRecoveryText(label, item.row.IncidentFrom, item.row.PendingSince, now, loc)
	case notificationKindResource:
		if item.row.PendingTarget == resourceStateWarning {
			return resourceAlertText(label, resourceAlertNames(item.detail))
		}
		return resourceRecoveryText(label, item.row.NotifiedDetail, item.row.IncidentFrom, item.row.PendingSince, now, loc)
	default:
		return renewalMessageText(label, item.row.PendingTarget, now)
	}
}

// notificationPlanFreshness bounds how old a planned message may be when it is
// sent. A slow delivery ends the queue early and the next round re-reads the
// actual state, so every message reflects the state at send time.
const notificationPlanFreshness = 2 * time.Second

func (h *handler) sendNotificationQueue(ctx context.Context, store notificationReconcileStore, reconciler *notificationReconciler, items []notificationSendItem, planned time.Time) {
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		now := h.notificationNow()
		if now.Sub(planned) > notificationPlanFreshness {
			h.wakeNotificationReconcile()
			return
		}
		if reconciler.inBackoff(item.channel.ID, now) {
			// The channel failed recently. Skip every send to it this round but
			// remember that this message was due and could not be delivered.
			if deferred, changed := applyNotificationDeferral(item.row, now.Unix()); changed {
				if err := store.ApplyNotificationStates(ctx, []notificationStateRow{deferred}, nil); err != nil && ctx.Err() == nil {
					log.Printf("notification reconcile state write failed: %v", err)
				}
			}
			continue
		}
		sendErr := item.channel.CredentialErr
		if sendErr == nil {
			sendCtx, cancel := context.WithTimeout(ctx, h.notificationTimeout())
			sendErr = h.notificationSender.Send(sendCtx, item.channel.notificationDispatchChannel, item.text)
			cancel()
		}
		if ctx.Err() != nil && sendErr != nil {
			// Shutdown interrupted the request; the next start retries it.
			return
		}
		h.recordNotificationSendOutcome(ctx, store, reconciler, item, sendErr)
	}
}

func (h *handler) recordNotificationSendOutcome(ctx context.Context, store notificationReconcileStore, reconciler *notificationReconciler, item notificationSendItem, sendErr error) {
	now := h.notificationNow()
	entry := notificationLogEntry{
		TS: now.Unix(), ChannelID: item.channel.ID, NodeID: item.node.ID, NodeName: item.node.Name, Kind: item.kind,
		From: item.row.Notified, To: item.row.PendingTarget, Attempt: item.row.PendingAttempts + 1, Message: item.text,
	}
	var next notificationStateRow
	if sendErr == nil {
		reconciler.recordSuccess(item.channel.ID)
		next = applyNotificationSendSuccess(item.kind, item.row, item.detail, now.Unix())
		entry.Outcome = notificationOutcomeSent
	} else {
		reconciler.recordFailure(item.channel.ID, now, sendErr)
		entry.Error = sanitizeNotificationDeliveryError(sendErr)
		next = applyNotificationSendFailure(item.row, entry.Error, now.Unix())
		entry.Outcome = notificationOutcomeFailed
	}
	logs := []notificationLogEntry{entry}
	// A crash between a confirmed send and this write repeats the message on
	// restart, which is the accepted trade-off.
	if err := store.ApplyNotificationStates(ctx, []notificationStateRow{next}, logs); err != nil && ctx.Err() == nil {
		log.Printf("notification reconcile state write failed: %v", err)
	}
	logNotificationOutcomes(logs)
}

func (r *notificationReconciler) inBackoff(channelID string, now time.Time) bool {
	state, ok := r.backoff[channelID]
	return ok && now.Before(state.notBefore)
}

func (r *notificationReconciler) recordSuccess(channelID string) {
	delete(r.backoff, channelID)
}

func (r *notificationReconciler) recordFailure(channelID string, now time.Time, sendErr error) {
	state := r.backoff[channelID]
	state.failures++
	delay := notificationRetryDelays[len(notificationRetryDelays)-1]
	if state.failures <= len(notificationRetryDelays) {
		delay = notificationRetryDelays[state.failures-1]
	}
	var rateLimited telegramRateLimitError
	if errors.As(sendErr, &rateLimited) && rateLimited.RetryAfter > 0 {
		delay = rateLimited.RetryAfter
	}
	state.notBefore = now.Add(delay)
	r.backoff[channelID] = state
}

// logNotificationOutcomes mirrors sent, failed and dropped log rows to the
// container log. Messages contain node labels only; channel credentials are
// never part of an entry.
func logNotificationOutcomes(entries []notificationLogEntry) {
	for _, entry := range entries {
		switch entry.Outcome {
		case notificationOutcomeSent, notificationOutcomeFailed, notificationOutcomeDropped:
			log.Printf("notification %s channel_id=%s node_id=%s kind=%s from=%s to=%s attempt=%d error=%q message=%q",
				entry.Outcome, entry.ChannelID, entry.NodeID, entry.Kind, entry.From, entry.To, entry.Attempt, entry.Error, entry.Message)
		}
	}
}
