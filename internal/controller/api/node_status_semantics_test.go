package api

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// The notification refactor stopped reading notification_event_marks when the
// agent writers decide whether a heartbeat ends an offline incident; they now
// read alert_rule_states only. While both stores agreed (the legacy outbox
// kept the status-active:offline mark in step with the active node_offline
// alert state) the stored nodes.status is identical. These tests pin that by
// comparing every combination against a reference transcription of the
// v1.0.23 rules.

// legacyHeartbeatNextStatus transcribes recordAgentHeartbeatTransitionOnce at
// v1.0.23: an active offline incident turns the previous status into
// "offline", which disables the warning-preservation rule.
func legacyHeartbeatNextStatus(stored string, incident bool, heartbeat string) string {
	previous := storedNodeStatusForNotification(stored)
	if incident && (heartbeat == "online" || heartbeat == "warning") {
		previous = "offline"
	}
	if heartbeat == "online" && previous == "warning" {
		return "warning"
	}
	return heartbeat
}

// legacyStateNextStatus transcribes updateNodeStatusForAlertRules at v1.0.23,
// where the offline incident never influenced the stored status.
func legacyStateNextStatus(stored, aggregate string, hadRelevantActive bool) string {
	if !hadRelevantActive && aggregate == "online" && stored == "warning" {
		return "warning"
	}
	return aggregate
}

func resetNodeStatusCase(t *testing.T, store *SQLiteStore, stored string, incident, staleMarkOnly bool) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Unix()
	for _, statement := range []string{
		`DELETE FROM alert_rule_states WHERE node_id = 'example-node-a'`,
		`DELETE FROM notification_event_marks WHERE node_id = 'example-node-a'`,
		`DELETE FROM state_samples WHERE node_id = 'example-node-a'`,
	} {
		if _, err := store.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = ?, last_seen_at = ? WHERE id = 'example-node-a'`, stored, now-120); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if incident {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO alert_rule_states (node_id, rule_id, active, first_seen_at, last_seen_at, updated_at)
			VALUES ('example-node-a', 'node_offline', 1, ?, ?, ?)
		`, now-60, now-60, now-60); err != nil {
			t.Fatalf("set incident: %v", err)
		}
	}
	if incident || staleMarkOnly {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO notification_event_marks (event_type, node_id, mark, created_at)
			VALUES ('node_offline', 'example-node-a', 'status-active:offline', ?)
		`, now-60); err != nil {
			t.Fatalf("set mark: %v", err)
		}
	}
}

func storedNodeStatus(t *testing.T, store *SQLiteStore) string {
	t.Helper()
	var status string
	if err := store.db.QueryRowContext(context.Background(), `SELECT status FROM nodes WHERE id = 'example-node-a'`).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return status
}

func TestHeartbeatStoredStatusMatchesLegacyRules(t *testing.T) {
	store := openOfflineRecoveryTestStore(t)
	for _, stored := range []string{"online", "warning", "offline", "no_data"} {
		for _, incident := range []bool{false, true} {
			for _, heartbeat := range []string{"online", "warning", "offline", "no_data"} {
				name := fmt.Sprintf("stored=%s incident=%v heartbeat=%s", stored, incident, heartbeat)
				resetNodeStatusCase(t, store, stored, incident, false)
				if _, err := store.RecordAgentHeartbeatTransition(context.Background(), "example-node-a", time.Now().UTC(), heartbeat, "agent-test"); err != nil {
					t.Fatalf("%s: heartbeat: %v", name, err)
				}
				want := legacyHeartbeatNextStatus(stored, incident, normalizeHeartbeatStatus(heartbeat))
				if got := storedNodeStatus(t, store); got != want {
					t.Fatalf("%s: nodes.status = %q, want legacy %q", name, got, want)
				}
				var active int
				_ = store.db.QueryRowContext(context.Background(), `SELECT COALESCE(MAX(active), 0) FROM alert_rule_states WHERE node_id = 'example-node-a' AND rule_id = 'node_offline'`).Scan(&active)
				if active != 0 {
					t.Fatalf("%s: offline incident still active after a heartbeat", name)
				}
			}
		}
	}
}

func TestStateReportStoredStatusMatchesLegacyRules(t *testing.T) {
	store := openOfflineRecoveryTestStore(t)
	zero := 0
	if _, err := store.UpdateAdminAlertRule(context.Background(), "cpu_high", AdminAlertRuleUpdateRequest{DurationSec: &zero}); err != nil {
		t.Fatalf("cpu rule: %v", err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	step := int64(0)
	for _, stored := range []string{"online", "warning", "offline"} {
		for _, incident := range []bool{false, true} {
			for _, staleMark := range []bool{false, true} {
				if incident && staleMark {
					continue
				}
				for _, cpu := range []float64{20, 97} {
					name := fmt.Sprintf("stored=%s incident=%v staleMark=%v cpu=%v", stored, incident, staleMark, cpu)
					resetNodeStatusCase(t, store, stored, incident, staleMark)
					step++
					state := AgentStateRequest{TS: base.Unix() + step, CPUPercent: cpu, MemoryUsedBytes: 1, MemoryTotalBytes: 100, DiskUsedBytes: 1, DiskTotalBytes: 100}
					accepted, _, err := store.RecordAgentStateReport(context.Background(), "example-node-a", state)
					if err != nil || !accepted {
						t.Fatalf("%s: state report accepted=%v err=%v", name, accepted, err)
					}
					aggregate := "online"
					if cpu >= 90 {
						aggregate = "warning"
					}
					want := legacyStateNextStatus(stored, aggregate, false)
					if got := storedNodeStatus(t, store); got != want {
						t.Fatalf("%s: nodes.status = %q, want legacy %q", name, got, want)
					}
				}
			}
		}
	}
}

// A status-active:offline mark without an active alert state was only
// possible in v1.0.23 when a recovery happened while no channel was enabled.
// This release never writes or clears marks, so such a mark is ignored: a
// heartbeat keeps a resource warning instead of treating the stale mark as a
// new offline incident.
func TestHeartbeatIgnoresStaleLegacyOfflineMark(t *testing.T) {
	store := openOfflineRecoveryTestStore(t)
	resetNodeStatusCase(t, store, "warning", false, true)
	transition, err := store.RecordAgentHeartbeatTransition(context.Background(), "example-node-a", time.Now().UTC(), "online", "agent-test")
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if got := storedNodeStatus(t, store); got != "warning" {
		t.Fatalf("nodes.status = %q, want the resource warning kept", got)
	}
	if transition.Previous.Status != "warning" || transition.Current.Status != "warning" {
		t.Fatalf("transition = %+v, want warning -> warning", transition)
	}
}
