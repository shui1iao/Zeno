package api

import (
	"fmt"
	"testing"
	"time"
)

const (
	decideTestNow        int64 = 1_791_402_208
	decideTestLastSeen   int64 = 1_791_402_147
	decideTestVersion    int64 = 3
	decideTestFingerprnt       = "fp-current"
)

func decideTestInput(kind string) notificationDecisionInput {
	return notificationDecisionInput{
		Kind: kind, ChannelID: "tg", NodeID: "node-a", Now: decideTestNow,
		ChannelVersion: decideTestVersion, ChannelFingerprint: decideTestFingerprnt,
		ActualKnown: true, LastSeenAt: decideTestLastSeen, HoldSeconds: 60,
	}
}

func decideTestRow(kind, notified string) notificationStateRow {
	return notificationStateRow{
		ChannelID: "tg", NodeID: "node-a", Kind: kind, Notified: notified,
		ChannelVersion: decideTestVersion, DestinationFingerprint: decideTestFingerprnt, UpdatedAt: decideTestNow - 1000,
	}
}

func withActual(in notificationDecisionInput, actual string) notificationDecisionInput {
	in.Actual = actual
	in.BaselineActual = actual
	return in
}

func withRow(in notificationDecisionInput, row notificationStateRow) notificationDecisionInput {
	in.HasRow = true
	in.Row = row
	return in
}

func pendingRow(row notificationStateRow, target string, since int64, attempts int, lastError string) notificationStateRow {
	row.PendingTarget = target
	row.PendingSince = since
	row.PendingAttempts = attempts
	row.LastError = lastError
	return row
}

// TestDecideNotificationNamedCases pins every action of the decision function
// with the expected persisted row.
func TestDecideNotificationNamedCases(t *testing.T) {
	liveness := decideTestInput(notificationKindLiveness)
	resource := decideTestInput(notificationKindResource)
	resource.HoldSeconds = 300
	renewal := decideTestInput(notificationKindRenewal)
	offlineRow := decideTestRow(notificationKindLiveness, livenessStateOffline)
	offlineRow.IncidentFrom = decideTestLastSeen - 500
	onlineRow := decideTestRow(notificationKindLiveness, livenessStateOnline)
	warningRow := decideTestRow(notificationKindResource, resourceStateWarning)
	warningRow.NotifiedDetail = "CPU"
	warningRow.IncidentFrom = decideTestNow - 900

	type want struct {
		baseline notificationAction
		action   notificationAction
		changed  bool
		next     notificationStateRow
	}
	cases := []struct {
		name string
		in   notificationDecisionInput
		want want
	}{
		{
			name: "paused keeps everything including pending",
			in: func() notificationDecisionInput {
				in := withRow(withActual(liveness, livenessStateOffline), pendingRow(onlineRow, livenessStateOffline, decideTestNow-10, 2, "delivery timed out"))
				in.Paused = true
				return in
			}(),
			want: want{action: notificationActionPause, next: pendingRow(onlineRow, livenessStateOffline, decideTestNow-10, 2, "delivery timed out")},
		},
		{
			name: "unknown actual (no_data) is skipped",
			in: func() notificationDecisionInput {
				in := withRow(liveness, onlineRow)
				in.ActualKnown = false
				return in
			}(),
			want: want{action: notificationActionNone, next: onlineRow},
		},
		{
			name: "first sight online is a silent baseline",
			in:   withActual(liveness, livenessStateOnline),
			want: want{baseline: notificationActionBaseline, action: notificationActionNone, changed: true, next: notificationStateRow{
				ChannelID: "tg", NodeID: "node-a", Kind: notificationKindLiveness, Notified: livenessStateOnline,
				ChannelVersion: decideTestVersion, DestinationFingerprint: decideTestFingerprnt, UpdatedAt: decideTestNow,
			}},
		},
		{
			name: "first sight offline is a silent baseline with incident start",
			in:   withActual(liveness, livenessStateOffline),
			want: want{baseline: notificationActionBaseline, action: notificationActionNone, changed: true, next: notificationStateRow{
				ChannelID: "tg", NodeID: "node-a", Kind: notificationKindLiveness, Notified: livenessStateOffline, IncidentFrom: decideTestLastSeen,
				ChannelVersion: decideTestVersion, DestinationFingerprint: decideTestFingerprnt, UpdatedAt: decideTestNow,
			}},
		},
		{
			name: "changed delivery version re-baselines silently and drops pending",
			in: func() notificationDecisionInput {
				row := pendingRow(onlineRow, livenessStateOffline, decideTestNow-30, 3, "delivery timed out")
				row.ChannelVersion = decideTestVersion - 1
				return withRow(withActual(liveness, livenessStateOffline), row)
			}(),
			want: want{baseline: notificationActionRebaseline, action: notificationActionNone, changed: true, next: notificationStateRow{
				ChannelID: "tg", NodeID: "node-a", Kind: notificationKindLiveness, Notified: livenessStateOffline, IncidentFrom: decideTestLastSeen,
				ChannelVersion: decideTestVersion, DestinationFingerprint: decideTestFingerprnt, UpdatedAt: decideTestNow,
			}},
		},
		{
			name: "changed destination fingerprint re-baselines silently",
			in: func() notificationDecisionInput {
				row := offlineRow
				row.DestinationFingerprint = "fp-old"
				return withRow(withActual(liveness, livenessStateOnline), row)
			}(),
			want: want{baseline: notificationActionRebaseline, action: notificationActionNone, changed: true, next: notificationStateRow{
				ChannelID: "tg", NodeID: "node-a", Kind: notificationKindLiveness, Notified: livenessStateOnline,
				ChannelVersion: decideTestVersion, DestinationFingerprint: decideTestFingerprnt, UpdatedAt: decideTestNow,
			}},
		},
		{
			name: "settled state does nothing",
			in:   withRow(withActual(liveness, livenessStateOnline), onlineRow),
			want: want{action: notificationActionNone, next: onlineRow},
		},
		{
			name: "offline is alerted immediately and the incident starts at last_seen_at",
			in:   withRow(withActual(liveness, livenessStateOffline), onlineRow),
			want: want{action: notificationActionSendAlert, changed: true, next: func() notificationStateRow {
				row := pendingRow(onlineRow, livenessStateOffline, decideTestNow, 0, "")
				row.IncidentFrom = decideTestLastSeen
				return row
			}()},
		},
		{
			name: "failed alert is retried with its original pending fields",
			in: func() notificationDecisionInput {
				row := pendingRow(onlineRow, livenessStateOffline, decideTestNow-20, 2, "delivery timed out")
				row.IncidentFrom = decideTestLastSeen - 20
				return withRow(withActual(liveness, livenessStateOffline), row)
			}(),
			want: want{action: notificationActionSendAlert, next: func() notificationStateRow {
				row := pendingRow(onlineRow, livenessStateOffline, decideTestNow-20, 2, "delivery timed out")
				row.IncidentFrom = decideTestLastSeen - 20
				return row
			}()},
		},
		{
			name: "recovery starts the hold window",
			in:   withRow(withActual(liveness, livenessStateOnline), offlineRow),
			want: want{action: notificationActionWait, changed: true, next: pendingRow(offlineRow, livenessStateOnline, decideTestNow, 0, "")},
		},
		{
			name: "recovery inside the hold window waits",
			in:   withRow(withActual(liveness, livenessStateOnline), pendingRow(offlineRow, livenessStateOnline, decideTestNow-59, 0, "")),
			want: want{action: notificationActionWait, next: pendingRow(offlineRow, livenessStateOnline, decideTestNow-59, 0, "")},
		},
		{
			name: "recovery is sent once the hold window elapsed",
			in:   withRow(withActual(liveness, livenessStateOnline), pendingRow(offlineRow, livenessStateOnline, decideTestNow-60, 0, "")),
			want: want{action: notificationActionSendRecovery, next: pendingRow(offlineRow, livenessStateOnline, decideTestNow-60, 0, "")},
		},
		{
			name: "failed recovery is retried",
			in:   withRow(withActual(liveness, livenessStateOnline), pendingRow(offlineRow, livenessStateOnline, decideTestNow-120, 1, "delivery failed")),
			want: want{action: notificationActionSendRecovery, next: pendingRow(offlineRow, livenessStateOnline, decideTestNow-120, 1, "delivery failed")},
		},
		{
			name: "jitter back to notified inside the hold window clears pending silently",
			in:   withRow(withActual(liveness, livenessStateOffline), pendingRow(offlineRow, livenessStateOnline, decideTestNow-30, 0, "")),
			want: want{action: notificationActionClearPending, changed: true, next: offlineRow},
		},
		{
			name: "undelivered alert is dropped when the state returned",
			in: func() notificationDecisionInput {
				row := pendingRow(onlineRow, livenessStateOffline, decideTestNow-30, 1, "delivery connection failed")
				row.IncidentFrom = decideTestLastSeen - 30
				return withRow(withActual(liveness, livenessStateOnline), row)
			}(),
			want: want{action: notificationActionDrop, changed: true, next: onlineRow},
		},
		{
			name: "alert deferred by a backing-off channel is dropped too",
			in:   withRow(withActual(liveness, livenessStateOnline), pendingRow(onlineRow, livenessStateOffline, decideTestNow-3, 0, notificationDeferredError)),
			want: want{action: notificationActionDrop, changed: true, next: onlineRow},
		},
		{
			name: "failed recovery is dropped when the node went offline again",
			in:   withRow(withActual(liveness, livenessStateOffline), pendingRow(offlineRow, livenessStateOnline, decideTestNow-90, 2, "delivery failed")),
			want: want{action: notificationActionDrop, changed: true, next: offlineRow},
		},
		{
			name: "resource warning is alerted immediately and starts its incident now",
			in: func() notificationDecisionInput {
				in := withRow(withActual(resource, resourceStateWarning), decideTestRow(notificationKindResource, resourceStateOK))
				in.Detail = "CPU、内存"
				return in
			}(),
			want: want{action: notificationActionSendAlert, changed: true, next: func() notificationStateRow {
				row := pendingRow(decideTestRow(notificationKindResource, resourceStateOK), resourceStateWarning, decideTestNow, 0, "")
				row.IncidentFrom = decideTestNow
				return row
			}()},
		},
		{
			name: "resource rule set changing inside warning sends nothing",
			in: func() notificationDecisionInput {
				in := withRow(withActual(resource, resourceStateWarning), warningRow)
				in.Detail = "CPU、内存"
				return in
			}(),
			want: want{action: notificationActionNone, next: warningRow},
		},
		{
			name: "resource recovery waits the full 300 seconds",
			in:   withRow(withActual(resource, resourceStateOK), pendingRow(warningRow, resourceStateOK, decideTestNow-299, 0, "")),
			want: want{action: notificationActionWait, next: pendingRow(warningRow, resourceStateOK, decideTestNow-299, 0, "")},
		},
		{
			name: "resource recovery is sent after 300 seconds",
			in:   withRow(withActual(resource, resourceStateOK), pendingRow(warningRow, resourceStateOK, decideTestNow-300, 0, "")),
			want: want{action: notificationActionSendRecovery, next: pendingRow(warningRow, resourceStateOK, decideTestNow-300, 0, "")},
		},
		{
			name: "zero hold sends recovery immediately",
			in: func() notificationDecisionInput {
				in := withRow(withActual(resource, resourceStateOK), warningRow)
				in.HoldSeconds = 0
				return in
			}(),
			want: want{action: notificationActionSendRecovery, changed: true, next: pendingRow(warningRow, resourceStateOK, decideTestNow, 0, "")},
		},
		{
			name: "renewal baseline counts only reminders before today, so today's is sent",
			in: func() notificationDecisionInput {
				in := withActual(renewal, "2026-10-10#3")
				in.BaselineActual = ""
				return in
			}(),
			want: want{baseline: notificationActionBaseline, action: notificationActionSendAlert, changed: true, next: notificationStateRow{
				ChannelID: "tg", NodeID: "node-a", Kind: notificationKindRenewal, PendingTarget: "2026-10-10#3", PendingSince: decideTestNow,
				ChannelVersion: decideTestVersion, DestinationFingerprint: decideTestFingerprnt, UpdatedAt: decideTestNow,
			}},
		},
		{
			name: "renewal key already notified is not repeated",
			in:   withRow(withActual(renewal, "2026-10-10#3"), decideTestRow(notificationKindRenewal, "2026-10-10#3")),
			want: want{action: notificationActionNone, next: decideTestRow(notificationKindRenewal, "2026-10-10#3")},
		},
		{
			name: "renewal key cleared (permanent or removed date) is recorded silently",
			in:   withRow(withActual(renewal, ""), pendingRow(decideTestRow(notificationKindRenewal, "2026-10-10#3"), "2026-10-10#1", decideTestNow-5, 1, "delivery failed")),
			want: want{action: notificationActionSilentUpdate, changed: true, next: decideTestRow(notificationKindRenewal, "")},
		},
		{
			name: "pending target changing mid-way resets pending_since and attempts",
			in:   withRow(withActual(renewal, "2026-10-10#1"), pendingRow(decideTestRow(notificationKindRenewal, "2026-10-10#7"), "2026-10-10#3", decideTestNow-86400, 4, "delivery failed")),
			want: want{action: notificationActionSendAlert, changed: true, next: pendingRow(decideTestRow(notificationKindRenewal, "2026-10-10#7"), "2026-10-10#1", decideTestNow, 0, "")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideNotification(tc.in)
			if got.Baseline != tc.want.baseline || got.Action != tc.want.action || got.Changed != tc.want.changed {
				t.Fatalf("decision = baseline:%s action:%s changed:%v, want baseline:%s action:%s changed:%v",
					got.Baseline, got.Action, got.Changed, tc.want.baseline, tc.want.action, tc.want.changed)
			}
			if got.Next != tc.want.next {
				t.Fatalf("next row =\n%+v\nwant\n%+v", got.Next, tc.want.next)
			}
		})
	}
}

// TestDecideNotificationExhaustiveLivenessMatrix enumerates every combination
// of row presence, routing binding, pause, notified, actual, pending target,
// failure history and hold window, and checks the action against an
// independent statement of the rules.
func TestDecideNotificationExhaustiveLivenessMatrix(t *testing.T) {
	states := []string{livenessStateOnline, livenessStateOffline}
	pendings := []string{"", livenessStateOnline, livenessStateOffline}
	count := 0
	for _, paused := range []bool{false, true} {
		for _, hasRow := range []bool{false, true} {
			for _, bindingMatches := range []bool{true, false} {
				for _, notified := range states {
					for _, actual := range states {
						for _, pending := range pendings {
							for _, failed := range []bool{false, true} {
								for _, holdElapsed := range []bool{false, true} {
									count++
									in := decideTestInput(notificationKindLiveness)
									in = withActual(in, actual)
									in.Paused = paused
									since := decideTestNow - 30
									if holdElapsed {
										since = decideTestNow - 60
									}
									attempts, lastError := 0, ""
									if failed {
										attempts, lastError = 1, "delivery failed"
									}
									row := pendingRow(decideTestRow(notificationKindLiveness, notified), pending, since, attempts, lastError)
									if pending == "" {
										row = decideTestRow(notificationKindLiveness, notified)
									}
									if !bindingMatches {
										row.ChannelVersion--
									}
									if hasRow {
										in = withRow(in, row)
									}
									wantBaseline, wantAction := livenessOracle(paused, hasRow, bindingMatches, notified, actual, pending, failed, holdElapsed)
									got := decideNotification(in)
									label := fmt.Sprintf("paused=%v row=%v binding=%v notified=%s actual=%s pending=%q failed=%v hold=%v",
										paused, hasRow, bindingMatches, notified, actual, pending, failed, holdElapsed)
									if got.Baseline != wantBaseline || got.Action != wantAction {
										t.Fatalf("%s: got baseline=%s action=%s, want baseline=%s action=%s", label, got.Baseline, got.Action, wantBaseline, wantAction)
									}
									assertDecisionInvariants(t, label, in, got)
								}
							}
						}
					}
				}
			}
		}
	}
	if count != 2*2*2*2*2*3*2*2 {
		t.Fatalf("matrix size = %d", count)
	}
}

func livenessOracle(paused, hasRow, bindingMatches bool, notified, actual, pending string, failed, holdElapsed bool) (notificationAction, notificationAction) {
	if paused {
		return notificationActionNone, notificationActionPause
	}
	if !hasRow {
		// Baseline records the actual state; nothing differs afterwards.
		return notificationActionBaseline, notificationActionNone
	}
	if !bindingMatches {
		return notificationActionRebaseline, notificationActionNone
	}
	if actual == notified {
		switch {
		case pending == "":
			return notificationActionNone, notificationActionNone
		case failed:
			return notificationActionNone, notificationActionDrop
		default:
			return notificationActionNone, notificationActionClearPending
		}
	}
	if actual == livenessStateOffline {
		return notificationActionNone, notificationActionSendAlert
	}
	// Recovery: it must have been pending for the full hold window already.
	if pending == livenessStateOnline && holdElapsed {
		return notificationActionNone, notificationActionSendRecovery
	}
	return notificationActionNone, notificationActionWait
}

func assertDecisionInvariants(t *testing.T, label string, in notificationDecisionInput, got notificationDecision) {
	t.Helper()
	if got.Action == notificationActionPause {
		if got.Changed || got.Next != in.Row {
			t.Fatalf("%s: pause modified the row", label)
		}
		return
	}
	// notified only ever changes through a (re)baseline or a confirmed send.
	if got.Baseline == notificationActionNone && got.Action != notificationActionSilentUpdate && got.Next.Notified != in.Row.Notified {
		t.Fatalf("%s: notified advanced without delivery: %q -> %q", label, in.Row.Notified, got.Next.Notified)
	}
	if got.Action.sends() && got.Next.PendingTarget != in.Actual {
		t.Fatalf("%s: send target %q is not the actual state %q", label, got.Next.PendingTarget, in.Actual)
	}
	if !got.Action.sends() && got.Action != notificationActionWait && got.Next.PendingTarget != "" {
		t.Fatalf("%s: settled decision kept pending %q", label, got.Next.PendingTarget)
	}
}

func TestNotificationSendOutcomesAdvanceOnlyOnSuccess(t *testing.T) {
	alert := pendingRow(decideTestRow(notificationKindResource, resourceStateOK), resourceStateWarning, decideTestNow-4, 2, "delivery timed out")
	alert.IncidentFrom = decideTestNow - 4
	sent := applyNotificationSendSuccess(notificationKindResource, alert, "CPU、内存", decideTestNow)
	if sent.Notified != resourceStateWarning || sent.NotifiedDetail != "CPU、内存" || sent.IncidentFrom != decideTestNow-4 ||
		sent.PendingTarget != "" || sent.PendingSince != 0 || sent.PendingAttempts != 0 || sent.LastError != "" {
		t.Fatalf("alert success row = %+v", sent)
	}
	recovery := pendingRow(sent, resourceStateOK, decideTestNow+300, 0, "")
	recovered := applyNotificationSendSuccess(notificationKindResource, recovery, "", decideTestNow+301)
	if recovered.Notified != resourceStateOK || recovered.NotifiedDetail != "" || recovered.IncidentFrom != 0 || recovered.PendingTarget != "" {
		t.Fatalf("recovery success row = %+v", recovered)
	}
	failed := applyNotificationSendFailure(alert, "delivery failed", decideTestNow)
	if failed.Notified != resourceStateOK || failed.PendingAttempts != 3 || failed.LastError != "delivery failed" || failed.PendingSince != alert.PendingSince {
		t.Fatalf("failure row = %+v", failed)
	}
	deferred, changed := applyNotificationDeferral(pendingRow(decideTestRow(notificationKindLiveness, livenessStateOnline), livenessStateOffline, decideTestNow, 0, ""), decideTestNow)
	if !changed || deferred.LastError != notificationDeferredError || deferred.PendingAttempts != 0 {
		t.Fatalf("deferral row = %+v changed=%v", deferred, changed)
	}
	if again, changed := applyNotificationDeferral(failed, decideTestNow); changed || again != failed {
		t.Fatalf("deferral overwrote a real failure: %+v", again)
	}
}

func TestNotificationChannelBackoffSequence(t *testing.T) {
	reconciler := newNotificationReconciler()
	at := time.Unix(decideTestNow, 0).UTC()
	for index, want := range []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, time.Minute, time.Minute} {
		reconciler.recordFailure("tg", at, fmt.Errorf("telegram returned status 502"))
		if !reconciler.inBackoff("tg", at.Add(want-time.Second)) {
			t.Fatalf("failure %d: channel not backing off at %s", index+1, want-time.Second)
		}
		if reconciler.inBackoff("tg", at.Add(want)) {
			t.Fatalf("failure %d: channel still backing off at %s", index+1, want)
		}
		at = at.Add(want)
	}
	reconciler.recordSuccess("tg")
	reconciler.recordFailure("tg", at, fmt.Errorf("telegram returned status 502"))
	if reconciler.inBackoff("tg", at.Add(2*time.Second)) {
		t.Fatal("success did not reset the backoff sequence")
	}
	reconciler.recordFailure("tg", at, telegramRateLimitError{RetryAfter: 37 * time.Second})
	if !reconciler.inBackoff("tg", at.Add(36*time.Second)) || reconciler.inBackoff("tg", at.Add(37*time.Second)) {
		t.Fatal("429 retry_after was not honoured exactly")
	}
	if reconciler.inBackoff("other", at) {
		t.Fatal("backoff leaked to another channel")
	}
}
