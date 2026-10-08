package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestNotificationHistoryReplay feeds the production notification history to
// the real reconcile round and compares what the new system would have sent
// with what the old outbox actually delivered. It runs only when
// ZENO_REPLAY_ROWS points at a JSON export of notification_deliveries.
//
// Simulation assumptions:
//   - Every row is a stored-status change observed at its created_at
//     (Controller time). A row canceled as "superseded by newer status" implies
//     an opposite change that never got a row, observed at the row's
//     updated_at (when the old outbox canceled it).
//   - An offline incident starts nodeHeartbeatOfflineAfter before it was
//     observed (the deliveries table does not record last_seen_at).
//   - Telegram accepts every send except: the second 2026-10-03 03:41:46Z
//     (the outbox fetch failure) and the first attempt of each row recorded as
//     "outcome unknown" (row 339), which is treated as a retryable failure.
//   - Renewal rows fix each node's due date (the date in the delivered detail,
//     reminder 3 days before); other nodes have no due date.
//   - Rounds run on every status change (wake), every 2 seconds while anything
//     is pending, and at every UTC midnight; idle stretches are skipped because
//     nothing can change in between.
func TestNotificationHistoryReplay(t *testing.T) {
	path := os.Getenv("ZENO_REPLAY_ROWS")
	if path == "" {
		t.Skip("ZENO_REPLAY_ROWS not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	var rows []replayDeliveryRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode rows: %v", err)
	}
	if len(rows) != 83 {
		t.Fatalf("rows = %d, want the 83 production deliveries", len(rows))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	sim := newReplaySimulation(rows)
	sim.run()

	result := compareReplay(rows, sim.store.logs)
	report := result.markdown(sim)
	if out := os.Getenv("ZENO_REPLAY_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(report), 0o644); err != nil {
			t.Fatalf("write report: %v", err)
		}
	}
	t.Log("\n" + report)

	if got := fmt.Sprint(result.unmatchedOldIDs()); got != "[309 310 321 322]" {
		t.Errorf("old-only deliveries = %s, want the 10-03 Sharon/Alibaba alerts and recoveries", got)
	}
	tarekAlert, tarekRecovery := false, false
	for _, entry := range result.newOnly {
		if entry.NodeID != "tarek-e97b62a6" || entry.TS < 1791402208 {
			continue
		}
		tarekAlert = tarekAlert || (entry.To == livenessStateOffline && entry.Attempt == 2)
		tarekRecovery = tarekRecovery || (entry.To == livenessStateOnline && strings.Contains(entry.Message, "–03:43:47，共 "))
	}
	if !tarekAlert || !tarekRecovery {
		t.Errorf("new-only messages = %+v, want Tarek's retried alert and its recovery with interval", result.newOnly)
	}
	for _, pair := range result.pairs {
		if pair.verdict == replayVerdictDifferent {
			t.Errorf("row %d: unexplained text difference old=%q new=%q", pair.old.ID, pair.oldText, pair.new.Message)
		}
	}
	if len(result.dropped) != 12 {
		t.Errorf("dropped = %d, want 12 (every node of the 2026-10-03 outage)", len(result.dropped))
	}
	for _, entry := range result.dropped {
		if entry.TS < 1790998906 || entry.TS > 1790998910 {
			t.Errorf("dropped outside the 10-03 outage: %+v", entry)
		}
	}
}

type replayDeliveryRow struct {
	ID             int64  `json:"id"`
	EventType      string `json:"event_type"`
	NodeID         string `json:"node_id"`
	NodeName       string `json:"node_name"`
	NodeIP         string `json:"node_ip"`
	PreviousStatus string `json:"previous_status"`
	Status         string `json:"status"`
	EventTS        string `json:"event_ts"`
	State          string `json:"state"`
	LastError      string `json:"last_error"`
	Detail         string `json:"detail"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
	DeliveredAt    *int64 `json:"delivered_at"`
}

// replayEvent carries the node name and address recorded on the row: the
// legacy text used them as they were at the time (FXTRANSIT JP was named
// FXTRANSIT in early October).
type replayEvent struct {
	at     int64
	nodeID string
	status string
	name   string
	ipv4   string
}

type replayStore struct {
	mockStore
	channel notificationReconcileChannel
	nodes   []notificationReconcileNode
	index   map[string]int
	rules   []AdminAlertRule
	states  map[notificationStateKey]notificationStateRow
	logs    []notificationLogEntry
}

func (s *replayStore) NotificationReconcileSnapshot(context.Context, time.Time) (notificationReconcileSnapshot, error) {
	states := make(map[notificationStateKey]notificationStateRow, len(s.states))
	for key, row := range s.states {
		states[key] = row
	}
	return notificationReconcileSnapshot{
		Channels: []notificationReconcileChannel{s.channel}, Nodes: append([]notificationReconcileNode(nil), s.nodes...),
		Rules: s.rules, ResourceActive: map[string][]string{}, States: states,
	}, nil
}

func (s *replayStore) ApplyNotificationStates(_ context.Context, rows []notificationStateRow, logs []notificationLogEntry) error {
	for _, row := range rows {
		s.states[notificationStateKey{ChannelID: row.ChannelID, NodeID: row.NodeID, Kind: row.Kind}] = row
	}
	s.logs = append(s.logs, logs...)
	return nil
}

func (s *replayStore) hasPending() bool {
	for _, row := range s.states {
		if row.PendingTarget != "" {
			return true
		}
	}
	return false
}

type replaySender struct {
	clock       *fakeNotificationClock
	unavailable map[int64]string
}

func (sender *replaySender) Send(context.Context, notificationDispatchChannel, string) error {
	if reason, ok := sender.unavailable[sender.clock.Now().Unix()]; ok {
		return errors.New(reason)
	}
	return nil
}

type replaySimulation struct {
	store  *replayStore
	events []replayEvent
	clock  *fakeNotificationClock
	h      *handler
	start  int64
	end    int64
	rounds int
}

func newReplaySimulation(rows []replayDeliveryRow) *replaySimulation {
	store := &replayStore{
		channel: notificationReconcileChannel{Enabled: true, notificationDispatchChannel: notificationDispatchChannel{
			ID: "telegram-d1c2ee3a", Name: "Telegram", Type: "telegram", Destination: "replay", DeliveryVersion: 1, DestinationFingerprint: "replay",
		}},
		index:  map[string]int{},
		states: map[notificationStateKey]notificationStateRow{},
		rules: []AdminAlertRule{
			{ID: "node_offline", NotificationEventType: notificationKindLiveness, Enabled: true, DurationSec: 60},
			{ID: "cpu_high", NotificationEventType: notificationKindResource, Enabled: true, DurationSec: 300},
			{ID: "renewal_due", Metric: "expiry_days", NotificationEventType: notificationKindRenewal, Enabled: true, Threshold: 3, RenewalDays: []int{3}},
		},
	}
	unavailable := map[int64]string{1790998906: "outbox fetch failed (2026-10-03 Controller outage)"}
	sim := &replaySimulation{store: store}
	offlineLead := int64(nodeHeartbeatOfflineAfter / time.Second)
	for _, row := range rows {
		index, ok := store.index[row.NodeID]
		if !ok {
			index = len(store.nodes)
			store.index[row.NodeID] = index
			store.nodes = append(store.nodes, notificationReconcileNode{ID: row.NodeID, Name: row.NodeName, Status: "online"})
			if row.EventType == notificationKindLiveness && row.PreviousStatus != "" {
				store.nodes[index].Status = row.PreviousStatus
			}
		}
		if strings.TrimSpace(row.NodeIP) != "" {
			store.nodes[index].IPv4 = row.NodeIP
		}
		switch row.EventType {
		case notificationKindLiveness:
			sim.events = append(sim.events, replayEvent{at: row.CreatedAt, nodeID: row.NodeID, status: row.Status, name: row.NodeName, ipv4: row.NodeIP})
			if row.State == "canceled" && strings.Contains(row.LastError, "superseded by newer status") {
				sim.events = append(sim.events, replayEvent{at: row.UpdatedAt, nodeID: row.NodeID, status: oppositeLiveness(row.Status), name: row.NodeName, ipv4: row.NodeIP})
			}
			if strings.Contains(row.LastError, "outcome unknown") {
				unavailable[row.CreatedAt] = fmt.Sprintf("row %d: request written, no response (outcome unknown)", row.ID)
			}
		case notificationKindRenewal:
			store.nodes[index].ExpiryDate = legacyRenewalDueDate(row.Detail)
		}
		for _, value := range []int64{row.CreatedAt, row.UpdatedAt} {
			if sim.end < value {
				sim.end = value
			}
		}
	}
	sort.SliceStable(sim.events, func(i, j int) bool { return sim.events[i].at < sim.events[j].at })
	sim.start = sim.events[0].at - 10
	sim.end += 600
	for index := range store.nodes {
		store.nodes[index].LastSeenAt = sim.start
		if store.nodes[index].Status == "offline" {
			store.nodes[index].LastSeenAt = sim.start - offlineLead
		}
	}
	sim.clock = newFakeNotificationClock(time.Unix(sim.start, 0))
	sim.h = &handler{
		store: store, notificationSender: &replaySender{clock: sim.clock, unavailable: unavailable},
		notificationClock: sim.clock.Now, notificationLoc: testShanghai, notificationSendTimeout: time.Second,
	}
	return sim
}

func (sim *replaySimulation) run() {
	offlineLead := int64(nodeHeartbeatOfflineAfter / time.Second)
	next := 0
	for at := sim.start; at <= sim.end; {
		for next < len(sim.events) && sim.events[next].at <= at {
			event := sim.events[next]
			node := &sim.store.nodes[sim.store.index[event.nodeID]]
			node.Status = event.status
			node.Name = event.name
			if strings.TrimSpace(event.ipv4) != "" {
				node.IPv4 = event.ipv4
			}
			node.LastSeenAt = event.at
			if event.status == "offline" {
				node.LastSeenAt = event.at - offlineLead
			}
			next++
		}
		sim.clock.Set(time.Unix(at, 0))
		sim.h.reconcileNotifications(context.Background())
		sim.rounds++
		following := (at/86400 + 1) * 86400
		if next < len(sim.events) && sim.events[next].at < following {
			following = sim.events[next].at
		}
		if sim.store.hasPending() && at+2 < following {
			following = at + 2
		}
		at = following
	}
}

func oppositeLiveness(status string) string {
	if status == "offline" {
		return "online"
	}
	return "offline"
}

func legacyRenewalDueDate(detail string) string {
	if index := strings.LastIndex(detail, "，"); index >= 0 {
		return strings.TrimSpace(detail[index+len("，"):])
	}
	return ""
}

const (
	replayVerdictSame      = "一致"
	replayVerdictRecovery  = "恢复消息增加区间和时长"
	replayVerdictDifferent = "文本不同"
)

type replayPair struct {
	old     replayDeliveryRow
	oldText string
	new     notificationLogEntry
	verdict string
}

type replayComparison struct {
	pairs   []replayPair
	oldOnly []replayPair
	newOnly []notificationLogEntry
	dropped []notificationLogEntry
	failed  []notificationLogEntry
}

func legacyDeliveredText(row replayDeliveryRow) (string, string) {
	if row.EventType == notificationKindRenewal {
		return renewalDueMessageText(row.NodeName, row.Detail), legacyRenewalDueDate(row.Detail) + "#3"
	}
	label := notificationNodeLabel(row.NodeName, row.NodeID, row.NodeIP)
	if row.Status == "offline" {
		return livenessAlertText(label), livenessStateOffline
	}
	return "🟢[恢复] " + label, livenessStateOnline
}

func compareReplay(rows []replayDeliveryRow, logs []notificationLogEntry) replayComparison {
	var result replayComparison
	var sent []notificationLogEntry
	for _, entry := range logs {
		switch entry.Outcome {
		case notificationOutcomeSent:
			sent = append(sent, entry)
		case notificationOutcomeDropped:
			result.dropped = append(result.dropped, entry)
		case notificationOutcomeFailed:
			result.failed = append(result.failed, entry)
		}
	}
	delivered := make([]replayDeliveryRow, 0, len(rows))
	for _, row := range rows {
		if row.State == "delivered" && row.DeliveredAt != nil {
			delivered = append(delivered, row)
		}
	}
	sort.SliceStable(delivered, func(i, j int) bool { return *delivered[i].DeliveredAt < *delivered[j].DeliveredAt })
	used := make([]bool, len(sent))
	for _, row := range delivered {
		text, to := legacyDeliveredText(row)
		pair := replayPair{old: row, oldText: text}
		// Pair with the closest unused message of the same node, kind and
		// direction (same UTC day for renewals, within 10 minutes otherwise).
		match, best := -1, int64(0)
		for index, entry := range sent {
			if used[index] || entry.NodeID != row.NodeID || entry.Kind != row.EventType || entry.To != to {
				continue
			}
			distance := entry.TS - *row.DeliveredAt
			if distance < 0 {
				distance = -distance
			}
			sameWindow := distance <= 600
			if row.EventType == notificationKindRenewal {
				sameWindow = entry.TS/86400 == *row.DeliveredAt/86400
			}
			if sameWindow && (match < 0 || distance < best) {
				match, best = index, distance
			}
		}
		if match < 0 {
			result.oldOnly = append(result.oldOnly, pair)
			continue
		}
		used[match] = true
		pair.new = sent[match]
		switch {
		case pair.new.Message == text:
			pair.verdict = replayVerdictSame
		case row.EventType == notificationKindLiveness && to == livenessStateOnline && strings.HasPrefix(pair.new.Message, text+" 离线 "):
			pair.verdict = replayVerdictRecovery
		default:
			pair.verdict = replayVerdictDifferent
		}
		result.pairs = append(result.pairs, pair)
	}
	for index, entry := range sent {
		if !used[index] {
			result.newOnly = append(result.newOnly, entry)
		}
	}
	return result
}

func (result replayComparison) unmatchedOldIDs() []int64 {
	ids := make([]int64, 0, len(result.oldOnly))
	for _, pair := range result.oldOnly {
		ids = append(ids, pair.old.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func replayClock(ts int64) string {
	return time.Unix(ts, 0).In(testShanghai).Format("01-02 15:04:05")
}

func (result replayComparison) markdown(sim *replaySimulation) string {
	var out strings.Builder
	counts := map[string]int{}
	for _, pair := range result.pairs {
		counts[pair.verdict]++
	}
	fmt.Fprintf(&out, "## 历史回放（%d 条 notification_deliveries，%s – %s，北京时间）\n\n", 83, replayClock(sim.start), replayClock(sim.end))
	fmt.Fprintf(&out, "- 对账轮次：%d；旧系统送达 %d 条；新系统发送 %d 条（失败 %d 次后重试）；dropped %d 条\n",
		sim.rounds, len(result.pairs)+len(result.oldOnly), len(result.pairs)+len(result.newOnly), len(result.failed), len(result.dropped))
	fmt.Fprintf(&out, "- 逐条配对 %d 条：%s %d、%s %d、%s %d；仅旧系统 %d 条；仅新系统 %d 条\n\n",
		len(result.pairs), replayVerdictSame, counts[replayVerdictSame], replayVerdictRecovery, counts[replayVerdictRecovery],
		replayVerdictDifferent, counts[replayVerdictDifferent], len(result.oldOnly), len(result.newOnly))
	out.WriteString("| 旧行 | 节点 | 类别 | 方向 | 旧送达 | 新发送 | 差(秒) | 结论 | 新消息正文 |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, pair := range result.pairs {
		fmt.Fprintf(&out, "| %d | %s | %s | %s | %s | %s | %+d | %s | %s |\n", pair.old.ID, pair.old.NodeName, pair.old.EventType,
			pair.new.To, replayClock(*pair.old.DeliveredAt), replayClock(pair.new.TS), pair.new.TS-*pair.old.DeliveredAt, pair.verdict, pair.new.Message)
	}
	out.WriteString("\n### 仅旧系统送达（新系统不发）\n\n")
	for _, pair := range result.oldOnly {
		fmt.Fprintf(&out, "- 行 %d %s %s→%s 送达 %s：%s\n", pair.old.ID, pair.old.NodeName, pair.old.PreviousStatus, pair.old.Status, replayClock(*pair.old.DeliveredAt), pair.oldText)
	}
	out.WriteString("\n### 仅新系统发送\n\n")
	for _, entry := range result.newOnly {
		fmt.Fprintf(&out, "- %s %s %s→%s 第 %d 次尝试：%s\n", replayClock(entry.TS), entry.NodeName, entry.From, entry.To, entry.Attempt, entry.Message)
	}
	out.WriteString("\n### 失败尝试\n\n")
	for _, entry := range result.failed {
		fmt.Fprintf(&out, "- %s %s %s→%s 第 %d 次：%s\n", replayClock(entry.TS), entry.NodeName, entry.From, entry.To, entry.Attempt, entry.Error)
	}
	out.WriteString("\n### dropped 日志\n\n")
	for _, entry := range result.dropped {
		fmt.Fprintf(&out, "- %s %s：%s（attempt=%d, error=%s）\n", replayClock(entry.TS), entry.NodeName, entry.Message, entry.Attempt, entry.Error)
	}
	return out.String()
}
