package api

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Renewal reminders are reconciled like every other kind. The actual value is
// the newest reminder point that is already due under the current due date,
// written "<dueDate>#<threshold>". A new non-empty key sends one reminder; a
// key that disappears (permanent node, removed date) is recorded silently.
// After the due date passes without a new billing cycle the key stays the
// same, so the reminder is never repeated.
//
// All calendar-day comparisons follow now.Location() (time.Local in production,
// or the caller-supplied location in unit tests).

func renewalNotificationDueDate(rawDate string, billingCycle sql.NullString, now time.Time) (time.Time, bool) {
	cycleMonths := billingCycleMonths(billingCycle)
	if cycleMonths > 0 {
		return nextBillingCycleDate(rawDate, cycleMonths, now)
	}
	loc := effectiveTimeLocation(now)
	expiresAt, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(rawDate), loc)
	if err != nil {
		return time.Time{}, false
	}
	return dateOnlyInLocation(expiresAt, loc), true
}

// renewalReminderDate is the day a threshold fires. "提前 1 个月" is a calendar
// operation, not a 30-day duration: clamping preserves end-of-month intent
// (Mar 31 -> Feb 28/29, May 31 -> Apr 30).
func renewalReminderDate(dueDate time.Time, threshold int) time.Time {
	dueDate = dateOnlyUTC(dueDate)
	if threshold == renewalNoticeCalendarMonthThreshold {
		return addMonthsFromAnchorClampedUTC(dueDate, -1)
	}
	return dueDate.AddDate(0, 0, -threshold)
}

func renewalRulesMatch(rules []AdminAlertRule, dueDate, today time.Time) bool {
	loc := effectiveTimeLocation(today)
	today = dateOnlyInLocation(today, loc)
	dueDate = dateOnlyInLocation(dueDate, loc)
	for _, rule := range rules {
		if !renewalRuleApplies(rule) {
			continue
		}
		for _, threshold := range effectiveRenewalNoticeDays(rule) {
			if calendarDaysBetween(renewalReminderDate(dueDate, threshold), today) == 0 {
				return true
			}
		}
	}
	return false
}

func renewalRuleApplies(rule AdminAlertRule) bool {
	return rule.NotificationEventType == notificationKindRenewal && rule.Metric == "expiry_days" && rule.Enabled
}

func renewalNotificationKey(dueDate time.Time, threshold int) string {
	return dateOnlyUTC(dueDate).Format("2006-01-02") + "#" + strconv.Itoa(threshold)
}

func parseRenewalNotificationKey(key string) (string, int, bool) {
	dueText, thresholdText, found := strings.Cut(strings.TrimSpace(key), "#")
	if !found {
		return "", 0, false
	}
	threshold, err := strconv.Atoi(thresholdText)
	if err != nil {
		return "", 0, false
	}
	if _, err := time.ParseInLocation("2006-01-02", dueText, time.UTC); err != nil {
		return "", 0, false
	}
	return dueText, threshold, true
}

// renewalNotificationKeyAt returns the newest reminder point under the
// node's current due date that is on or before today (includeToday) or
// strictly before today (the baseline view).
func renewalNotificationKeyAt(node notificationReconcileNode, rules []AdminAlertRule, now time.Time, includeToday bool) string {
	if node.ExpiryPermanent || strings.TrimSpace(node.ExpiryDate) == "" {
		return ""
	}
	loc := effectiveTimeLocation(now)
	now = now.In(loc)
	dueDate, ok := renewalNotificationDueDate(node.ExpiryDate, node.BillingCycle, now)
	if !ok {
		return ""
	}
	today := dateOnlyInLocation(now, loc)
	found := false
	var bestDate time.Time
	bestThreshold := 0
	for _, rule := range rules {
		if !renewalRuleApplies(rule) {
			continue
		}
		for _, threshold := range effectiveRenewalNoticeDays(rule) {
			reminder := dateOnlyInLocation(renewalReminderDate(dueDate, threshold), loc)
			if reminder.After(today) || (!includeToday && reminder.Equal(today)) {
				continue
			}
			if !found || reminder.After(bestDate) || (reminder.Equal(bestDate) && threshold < bestThreshold) {
				found = true
				bestDate = reminder
				bestThreshold = threshold
			}
		}
	}
	if !found {
		return ""
	}
	return renewalNotificationKey(dueDate, bestThreshold)
}

func renewalDaysRemaining(dueDate, now time.Time) int {
	return calendarDaysBetween(now, dueDate)
}

// renewalNotificationMark is the legacy notification_event_marks key for a
// reminder sent on day for expiryDate. It is only read by the one-time seed.
func renewalNotificationMark(day, expiryDate string) string {
	return strings.TrimSpace(day) + ":" + strings.TrimSpace(expiryDate)
}

func formatRenewalNotificationDetail(daysRemaining int, expiryDate string) string {
	switch {
	case daysRemaining > 0:
		return fmt.Sprintf("还有 %d 天到期，%s", daysRemaining, expiryDate)
	case daysRemaining == 0:
		return fmt.Sprintf("今天到期，%s", expiryDate)
	default:
		return fmt.Sprintf("已过期 %d 天，%s", -daysRemaining, expiryDate)
	}
}
