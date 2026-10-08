package api

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestExpiryLabelValueUsesBillingCycleForNextRenewal(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	label := expiryLabelValue(
		sql.NullString{String: "2026-08-10", Valid: true},
		sql.NullString{String: "月", Valid: true},
		false,
		now,
	)
	if label != "余 2 天" {
		t.Fatalf("expiry label = %q, want monthly cycle to use the next renewal as 余 2 天", label)
	}
}

func TestExpiredRecurringDateRollsForwardForSummaryAndNotifications(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	expiryDate := sql.NullString{String: "2026-07-18", Valid: true}
	billingCycle := sql.NullString{String: "月付", Valid: true}

	label := expiryLabelValue(expiryDate, billingCycle, false, now)
	if label != "余 30 天" {
		t.Fatalf("expiry label = %q, want expired recurring date to roll forward as 余 30 天", label)
	}

	dueDate, ok := renewalNotificationDueDate(expiryDate.String, billingCycle, now)
	if !ok || dueDate.Format("2006-01-02") != "2026-08-18" {
		t.Fatalf("renewal due date = %s, ok = %v, want 2026-08-18", dueDate.Format("2006-01-02"), ok)
	}
}

func TestRecurringBillingDateRollsForwardAtStartOfDueDay(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, shanghai)
	expiryDate := sql.NullString{String: "2026-08-01", Valid: true}
	billingCycle := sql.NullString{String: "月", Valid: true}

	label := expiryLabelValue(expiryDate, billingCycle, false, now)
	if label != "余 31 天" {
		t.Fatalf("expiry label = %q, want due-day recurring billing to roll forward as 余 31 天", label)
	}

	dueDate, ok := renewalNotificationDueDate(expiryDate.String, billingCycle, now)
	if !ok || dueDate.Format("2006-01-02") != "2026-09-01" {
		t.Fatalf("renewal due date = %s, ok = %v, want 2026-09-01", dueDate.Format("2006-01-02"), ok)
	}
}

func TestFormatExpiryDaysLabelUsesZeroDaysInsteadOfToday(t *testing.T) {
	now := time.Date(2026, 8, 1, 13, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	if label := formatExpiryDaysLabel(now, now); label != "余 0 天" {
		t.Fatalf("expiry label = %q, want 余 0 天", label)
	}
}

func TestRenewalNotificationKeySkipsPermanentNode(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	expiryDate := time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")
	permanent := true
	if _, err := store.UpdateAdminNode(ctx, "example-node-a", AdminNodeUpdateRequest{ExpiryDate: &expiryDate, ExpiryPermanent: &permanent}); err != nil {
		t.Fatalf("set permanent expiry: %v", err)
	}
	enabled := true
	if _, err := store.UpdateAdminAlertRule(ctx, "renewal_due", AdminAlertRuleUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable renewal rule: %v", err)
	}
	snapshot, err := store.NotificationReconcileSnapshot(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("notification snapshot: %v", err)
	}
	for _, node := range snapshot.Nodes {
		for _, now := range []time.Time{time.Now().UTC(), time.Now().UTC().Add(24 * time.Hour)} {
			if key := renewalNotificationKeyAt(node, snapshot.rulesFor("renewal_due", node.ID), now, true); key != "" {
				t.Fatalf("permanent node %s produced renewal key %q", node.ID, key)
			}
		}
	}
}

func TestExpiryLabelValueRollsOverAtLocalMidnight(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 7, 11, 0, 30, 0, 0, shanghai)
	label := expiryLabelValue(
		sql.NullString{String: "2026-08-10", Valid: true},
		sql.NullString{String: "月", Valid: true},
		false,
		now,
	)
	if label != "余 30 天" {
		t.Fatalf("expiry label = %q, want recurring billing date to roll over at local midnight", label)
	}

	dueDate, ok := renewalNotificationDueDate(
		"2026-08-10",
		sql.NullString{String: "月", Valid: true},
		now,
	)
	if !ok || dueDate.Format("2006-01-02") != "2026-08-10" {
		t.Fatalf("renewal due date = %s, ok = %v, want 2026-08-10", dueDate.Format("2006-01-02"), ok)
	}
	if got := dateOnlyUTC(now).Format("2006-01-02"); got != "2026-07-11" {
		t.Fatalf("calendar date = %s, want local date 2026-07-11", got)
	}
}

func TestExpiryLabelValueKeepsRawDateWhenBillingCycleUnknown(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	label := expiryLabelValue(
		sql.NullString{String: "2026-08-10", Valid: true},
		sql.NullString{String: "", Valid: true},
		false,
		now,
	)
	if label != "2026-08-10" {
		t.Fatalf("expiry label = %q, want raw date without a recurring billing cycle", label)
	}
}

func TestExpiryLabelValueKeepsExpiredRawDateWhenBillingCycleUnknown(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	label := expiryLabelValue(
		sql.NullString{String: "2026-07-18", Valid: true},
		sql.NullString{String: "一次性", Valid: true},
		false,
		now,
	)
	if label != "2026-07-18" {
		t.Fatalf("expiry label = %q, want unknown billing cycle to keep the one-time raw date", label)
	}
}

func TestExpiryLabelValueClampsMonthEndCycle(t *testing.T) {
	now := time.Date(2026, 2, 27, 12, 0, 0, 0, time.UTC)
	label := expiryLabelValue(
		sql.NullString{String: "2026-03-31", Valid: true},
		sql.NullString{String: "月付", Valid: true},
		false,
		now,
	)
	if label != "余 1 天" {
		t.Fatalf("expiry label = %q, want February renewal clamped to month end", label)
	}
}
