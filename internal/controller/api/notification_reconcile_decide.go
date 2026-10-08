package api

import "strings"

// The reconcile model keeps exactly one fact per (channel, node, kind): the
// state the user was last told about ("notified"). The actual state is always
// read from its authoritative representation. decideNotification compares the
// two and is deliberately free of I/O so every combination can be tested
// exhaustively.

// Notification kinds reuse the historical event types so alert rules, the
// notification-types compatibility API and the log stay comparable.
const (
	notificationKindLiveness = "node_offline"
	notificationKindResource = "probe_unhealthy"
	notificationKindRenewal  = "renewal_due"
)

var notificationKinds = []string{notificationKindLiveness, notificationKindResource, notificationKindRenewal}

const (
	livenessStateOnline  = "online"
	livenessStateOffline = "offline"
	resourceStateOK      = "ok"
	resourceStateWarning = "warning"
)

type notificationStateRow struct {
	ChannelID              string
	NodeID                 string
	Kind                   string
	Notified               string
	NotifiedDetail         string
	IncidentFrom           int64
	PendingTarget          string
	PendingSince           int64
	PendingAttempts        int
	LastError              string
	ChannelVersion         int64
	DestinationFingerprint string
	UpdatedAt              int64
}

type notificationAction int

const (
	notificationActionNone notificationAction = iota
	notificationActionPause
	notificationActionBaseline
	notificationActionRebaseline
	notificationActionWait
	notificationActionSendAlert
	notificationActionSendRecovery
	notificationActionClearPending
	notificationActionDrop
	notificationActionSilentUpdate
)

func (action notificationAction) String() string {
	switch action {
	case notificationActionPause:
		return "pause"
	case notificationActionBaseline:
		return "baseline"
	case notificationActionRebaseline:
		return "rebaseline"
	case notificationActionWait:
		return "wait"
	case notificationActionSendAlert:
		return "send_alert"
	case notificationActionSendRecovery:
		return "send_recovery"
	case notificationActionClearPending:
		return "clear_pending"
	case notificationActionDrop:
		return "drop"
	case notificationActionSilentUpdate:
		return "silent_update"
	default:
		return "none"
	}
}

func (action notificationAction) sends() bool {
	return action == notificationActionSendAlert || action == notificationActionSendRecovery
}

type notificationDecisionInput struct {
	Kind               string
	ChannelID          string
	NodeID             string
	Now                int64
	HasRow             bool
	Row                notificationStateRow
	ChannelVersion     int64
	ChannelFingerprint string
	// Paused covers every condition under which nothing may be sent or
	// advanced: channel disabled, no enabled rule for this node and kind, or
	// node disabled. Global unavailability stops the loop before any decision.
	Paused      bool
	ActualKnown bool
	Actual      string
	// BaselineActual is the value recorded when a row is first created. It
	// equals Actual except for renewal, where today's reminder is not treated
	// as already notified.
	BaselineActual string
	// Detail is the active resource rule names for a resource warning.
	Detail string
	// LastSeenAt is the liveness incident start when the node is offline.
	LastSeenAt  int64
	HoldSeconds int64
}

type notificationDecision struct {
	// Baseline is notificationActionBaseline or notificationActionRebaseline
	// when the row was (re)created silently by this decision.
	Baseline notificationAction
	Action   notificationAction
	// Next is the row to persist. For send actions it carries the pending
	// fields; the send outcome is applied on top of it.
	Next    notificationStateRow
	Changed bool
}

func decideNotification(in notificationDecisionInput) notificationDecision {
	if in.Paused {
		return notificationDecision{Action: notificationActionPause, Next: in.Row}
	}
	if !in.ActualKnown {
		return notificationDecision{Action: notificationActionNone, Next: in.Row}
	}
	baseline := notificationActionNone
	row := in.Row
	if !in.HasRow {
		baseline = notificationActionBaseline
	} else if row.ChannelVersion != in.ChannelVersion || row.DestinationFingerprint != in.ChannelFingerprint {
		baseline = notificationActionRebaseline
	}
	if baseline != notificationActionNone {
		row = baselineNotificationRow(in)
	}
	decision := compareNotificationState(in, row)
	decision.Baseline = baseline
	if baseline != notificationActionNone {
		decision.Changed = true
	}
	return decision
}

func compareNotificationState(in notificationDecisionInput, row notificationStateRow) notificationDecision {
	if in.Actual == row.Notified {
		return settleNotificationPending(in.Kind, row)
	}
	recovery := notificationIsRecovery(in.Kind, row.Notified, in.Actual)
	if !recovery && !notificationStateIsAlert(in.Kind, in.Actual) {
		// A non-alert value that is not a recovery (for example a renewal key
		// that disappeared because the node became permanent) is recorded
		// silently without a message.
		next := clearNotificationPending(row)
		next.Notified = in.Actual
		next.NotifiedDetail = ""
		next.IncidentFrom = 0
		return notificationDecision{Action: notificationActionSilentUpdate, Next: next, Changed: true}
	}
	next := row
	changed := false
	if next.PendingTarget != in.Actual {
		next.PendingTarget = in.Actual
		next.PendingSince = in.Now
		next.PendingAttempts = 0
		next.LastError = ""
		if !recovery && !notificationStateIsAlert(in.Kind, row.Notified) {
			// The incident starts when the alert state is first observed,
			// whether or not the alert can be delivered.
			next.IncidentFrom = notificationIncidentStart(in)
		}
		changed = true
	}
	if recovery {
		if in.Now-next.PendingSince < in.HoldSeconds {
			return notificationDecision{Action: notificationActionWait, Next: next, Changed: changed}
		}
		return notificationDecision{Action: notificationActionSendRecovery, Next: next, Changed: changed}
	}
	return notificationDecision{Action: notificationActionSendAlert, Next: next, Changed: changed}
}

// settleNotificationPending handles actual == notified. A pending target that
// was never due is ordinary hold-window jitter and is cleared silently; one
// that failed (or was deferred by a failing channel) is reported as dropped.
func settleNotificationPending(kind string, row notificationStateRow) notificationDecision {
	if row.PendingTarget == "" {
		return notificationDecision{Action: notificationActionNone, Next: row}
	}
	action := notificationActionClearPending
	if row.PendingAttempts > 0 || row.LastError != "" {
		action = notificationActionDrop
	}
	next := clearNotificationPending(row)
	if !notificationStateIsAlert(kind, next.Notified) {
		next.IncidentFrom = 0
	}
	return notificationDecision{Action: action, Next: next, Changed: true}
}

func baselineNotificationRow(in notificationDecisionInput) notificationStateRow {
	row := notificationStateRow{
		ChannelID:              in.ChannelID,
		NodeID:                 in.NodeID,
		Kind:                   in.Kind,
		Notified:               in.BaselineActual,
		ChannelVersion:         in.ChannelVersion,
		DestinationFingerprint: in.ChannelFingerprint,
		UpdatedAt:              in.Now,
	}
	if notificationStateIsAlert(in.Kind, in.BaselineActual) {
		// The incident starts when it is first observed: last_seen_at for an
		// offline node, the observation time for a resource warning.
		row.IncidentFrom = notificationIncidentStart(in)
		if in.Kind == notificationKindResource {
			row.NotifiedDetail = in.Detail
		}
	}
	return row
}

func notificationIncidentStart(in notificationDecisionInput) int64 {
	switch in.Kind {
	case notificationKindLiveness:
		return in.LastSeenAt
	case notificationKindResource:
		return in.Now
	default:
		return 0
	}
}

func clearNotificationPending(row notificationStateRow) notificationStateRow {
	row.PendingTarget = ""
	row.PendingSince = 0
	row.PendingAttempts = 0
	row.LastError = ""
	return row
}

// notificationStateIsAlert reports the alert direction, which is sent
// immediately. Any non-empty renewal key is an alert.
func notificationStateIsAlert(kind, value string) bool {
	switch kind {
	case notificationKindLiveness:
		return value == livenessStateOffline
	case notificationKindResource:
		return value == resourceStateWarning
	case notificationKindRenewal:
		return strings.TrimSpace(value) != ""
	default:
		return false
	}
}

// notificationIsRecovery reports the recovery direction, which must hold for
// the rule duration before it is sent.
func notificationIsRecovery(kind, notified, actual string) bool {
	switch kind {
	case notificationKindLiveness:
		return notified == livenessStateOffline && actual == livenessStateOnline
	case notificationKindResource:
		return notified == resourceStateWarning && actual == resourceStateOK
	default:
		return false
	}
}

// applyNotificationSendSuccess advances notified only after a confirmed send.
func applyNotificationSendSuccess(kind string, next notificationStateRow, detail string, now int64) notificationStateRow {
	actual := next.PendingTarget
	row := clearNotificationPending(next)
	row.Notified = actual
	row.NotifiedDetail = ""
	if notificationStateIsAlert(kind, actual) {
		if kind == notificationKindResource {
			row.NotifiedDetail = detail
		}
	} else {
		row.IncidentFrom = 0
	}
	row.UpdatedAt = now
	return row
}

// applyNotificationSendFailure keeps the pending target for the next round.
// Every failure, including a request whose response never arrived, is a
// retryable non-delivery.
func applyNotificationSendFailure(next notificationStateRow, sanitizedError string, now int64) notificationStateRow {
	next.PendingAttempts++
	next.LastError = sanitizedError
	next.UpdatedAt = now
	return next
}

const notificationDeferredError = "channel backing off after a failed delivery"

// applyNotificationDeferral records that a due message could not be attempted
// because its channel is backing off after a failure. It is not an attempt,
// but a later return to the notified state is reported as dropped.
func applyNotificationDeferral(next notificationStateRow, now int64) (notificationStateRow, bool) {
	if next.LastError != "" {
		return next, false
	}
	next.LastError = notificationDeferredError
	next.UpdatedAt = now
	return next, true
}
