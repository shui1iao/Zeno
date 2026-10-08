package api

import (
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRenewalRulesMatchCalendarMonthBoundaries(t *testing.T) {
	removedSameDayRule := []AdminAlertRule{{
		Metric:                "expiry_days",
		NotificationEventType: "renewal_due",
		Enabled:               true,
		Threshold:             0,
	}}
	dueDay := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if renewalRulesMatch(removedSameDayRule, dueDay, dueDay) {
		t.Fatal("removed same-day reminder still matched")
	}

	multiDayRule := []AdminAlertRule{{
		Metric:                "expiry_days",
		NotificationEventType: "renewal_due",
		Enabled:               true,
		Threshold:             7,
		RenewalDays:           []int{1, 3, 7},
	}}
	multiDayDue := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	for _, reminderDay := range []time.Time{
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC),
	} {
		if !renewalRulesMatch(multiDayRule, multiDayDue, reminderDay) {
			t.Fatalf("multi-day renewal reminder did not match %s", reminderDay.Format("2006-01-02"))
		}
	}
	if renewalRulesMatch(multiDayRule, multiDayDue, time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("multi-day renewal reminder matched an unselected day")
	}

	monthRule := []AdminAlertRule{{
		Metric:                "expiry_days",
		NotificationEventType: "renewal_due",
		Enabled:               true,
		Threshold:             renewalNoticeCalendarMonthThreshold,
	}}
	cases := []struct {
		name string
		due  string
		now  string
	}{
		{name: "non leap march end", due: "2026-03-31", now: "2026-02-28"},
		{name: "leap march end", due: "2024-03-31", now: "2024-02-29"},
		{name: "thirty day month", due: "2026-05-31", now: "2026-04-30"},
		{name: "ordinary month", due: "2026-06-18", now: "2026-05-18"},
		{name: "leap day due", due: "2024-02-29", now: "2024-01-29"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			due, err := time.Parse("2006-01-02", tc.due)
			if err != nil {
				t.Fatalf("parse due date: %v", err)
			}
			now, err := time.Parse("2006-01-02", tc.now)
			if err != nil {
				t.Fatalf("parse reminder date: %v", err)
			}
			if !renewalRulesMatch(monthRule, due, now) {
				t.Fatalf("calendar-month reminder did not match due=%s now=%s", tc.due, tc.now)
			}
			if renewalRulesMatch(monthRule, due, now.AddDate(0, 0, -1)) || renewalRulesMatch(monthRule, due, now.AddDate(0, 0, 1)) {
				t.Fatalf("calendar-month reminder matched outside its single trigger day")
			}
		})
	}
}

func renewalTestNode(expiry, cycle string, permanent bool) notificationReconcileNode {
	node := notificationReconcileNode{ID: "n", Name: "N", ExpiryDate: expiry, ExpiryPermanent: permanent}
	if cycle != "" {
		node.BillingCycle = sql.NullString{String: cycle, Valid: true}
	}
	return node
}

func renewalTestRules(days ...int) []AdminAlertRule {
	return []AdminAlertRule{{ID: "renewal_due", Metric: "expiry_days", NotificationEventType: "renewal_due", Enabled: true, Threshold: 3, RenewalDays: days}}
}

// The actual renewal value is the newest reminder point that is due under the
// current due date. It reuses the billing-cycle, calendar-month and UTC date
// rules unchanged.
func TestRenewalNotificationKeyAt(t *testing.T) {
	day := func(value string) time.Time {
		parsed, err := time.Parse("2006-01-02 15:04", value)
		if err != nil {
			t.Fatalf("parse %s: %v", value, err)
		}
		return parsed
	}
	cases := []struct {
		name         string
		node         notificationReconcileNode
		rules        []AdminAlertRule
		now          time.Time
		includeToday string
		beforeToday  string
	}{
		{"before the first reminder", renewalTestNode("2026-10-10", "", false), renewalTestRules(3), day("2026-10-06 23:59"), "", ""},
		{"on the reminder day", renewalTestNode("2026-10-10", "", false), renewalTestRules(3), day("2026-10-07 00:00"), "2026-10-10#3", ""},
		{"after the reminder day", renewalTestNode("2026-10-10", "", false), renewalTestRules(3), day("2026-10-09 12:00"), "2026-10-10#3", "2026-10-10#3"},
		{"expired without a new cycle keeps the key", renewalTestNode("2026-10-10", "", false), renewalTestRules(3), day("2026-11-20 12:00"), "2026-10-10#3", "2026-10-10#3"},
		{"newest of several reminders", renewalTestNode("2026-10-15", "", false), renewalTestRules(1, 3, 7), day("2026-10-12 08:00"), "2026-10-15#3", "2026-10-15#7"},
		{"newest of several reminders on a later day", renewalTestNode("2026-10-15", "", false), renewalTestRules(1, 3, 7), day("2026-10-13 08:00"), "2026-10-15#3", "2026-10-15#3"},
		// Only reminder points under the current due date count; the previous
		// cycle's reminder is not carried over.
		{"recurring cycle uses the next billing date", renewalTestNode("2026-08-10", "月", false), renewalTestRules(3), day("2026-10-07 01:00"), "2026-10-10#3", ""},
		{"recurring cycle after the reminder", renewalTestNode("2026-08-10", "月", false), renewalTestRules(3), day("2026-10-09 01:00"), "2026-10-10#3", "2026-10-10#3"},
		{"recurring cycle rolls over on the billing day", renewalTestNode("2026-08-10", "月", false), renewalTestRules(3), day("2026-10-10 01:00"), "", ""},
		{"calendar month February cycle", renewalTestNode("2026-04-01", "月", false), renewalTestRules(renewalNoticeCalendarMonthThreshold), day("2026-02-01 12:00"), "2026-03-01#30", ""},
		{"permanent node", renewalTestNode("2026-10-10", "", true), renewalTestRules(3), day("2026-10-08 12:00"), "", ""},
		{"no expiry date", renewalTestNode("", "", false), renewalTestRules(3), day("2026-10-08 12:00"), "", ""},
		{"disabled rule", renewalTestNode("2026-10-10", "", false), []AdminAlertRule{{Metric: "expiry_days", NotificationEventType: "renewal_due", RenewalDays: []int{3}}}, day("2026-10-08 12:00"), "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renewalNotificationKeyAt(tc.node, tc.rules, tc.now, true); got != tc.includeToday {
				t.Fatalf("actual key = %q, want %q", got, tc.includeToday)
			}
			if got := renewalNotificationKeyAt(tc.node, tc.rules, tc.now, false); got != tc.beforeToday {
				t.Fatalf("baseline key = %q, want %q", got, tc.beforeToday)
			}
		})
	}
	// The reminder becomes due at the UTC day boundary, i.e. 08:00 in
	// Shanghai, exactly like the legacy scanner's dateOnlyUTC semantics.
	node := renewalTestNode("2026-10-10", "", false)
	if got := renewalNotificationKeyAt(node, renewalTestRules(3), shanghaiTime(2026, 10, 7, 7, 59, 59), true); got != "" {
		t.Fatalf("key before the UTC day boundary = %q", got)
	}
	if got := renewalNotificationKeyAt(node, renewalTestRules(3), shanghaiTime(2026, 10, 7, 8, 0, 0), true); got != "2026-10-10#3" {
		t.Fatalf("key at the UTC day boundary = %q", got)
	}
}

// A recurring billing date is reminded with the cycle date, not the final
// expiry date, and concurrent rounds send it exactly once.
func TestReconcileRenewalRecurringCycleIsSentOnceUnderConcurrentRounds(t *testing.T) {
	hs := newReconcileHarness(t, shanghaiTime(2026, 10, 6, 10, 0, 0))
	hs.addNode("harbor", "Example Harbor", "203.0.113.60")
	setNodeExpiry(t, hs, "harbor", "2026-12-08", "月")
	enableRenewalForHarness(t, hs, 1)
	hs.round()
	hs.clock.Set(shanghaiTime(2026, 10, 7, 9, 0, 0))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hs.round()
		}()
	}
	wg.Wait()
	texts := hs.tg.ackedTexts()
	if len(texts) != 1 || texts[0] != "⚠️[到期] Example Harbor 将于 1 天后（2026-10-8）到期" {
		t.Fatalf("acked = %q, want one reminder for the 2026-10-08 billing date", texts)
	}
	if strings.Contains(texts[0], "2026-12-8") {
		t.Fatalf("reminder used the final expiry date: %q", texts[0])
	}
	hs.at(shanghaiTime(2026, 10, 8, 20, 0, 0))
	if len(hs.tg.ackedTexts()) != 1 {
		t.Fatalf("billing day repeated the reminder: %q", hs.tg.ackedTexts())
	}
}
