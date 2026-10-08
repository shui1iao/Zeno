package api

import (
	"fmt"
	"strings"
	"time"
)

// Message texts. Alert texts are byte-for-byte the historical ones; recovery
// texts add the incident interval and duration.

func livenessAlertText(label string) string {
	return fmt.Sprintf("🔴[离线] %s", label)
}

func resourceAlertText(label string, names []string) string {
	return fmt.Sprintf("⚠️[警告] %s%s", label, resourceAlertDetail(names, false))
}

func livenessRecoveryText(label string, from, to int64, now time.Time, loc *time.Location) string {
	if from <= 0 || to < from {
		return fmt.Sprintf("🟢[恢复] %s", label)
	}
	return fmt.Sprintf("🟢[恢复] %s 离线 %s，共 %s", label, formatNotificationInterval(from, to, now, loc), formatNotificationDuration(to-from))
}

func resourceRecoveryText(label, notifiedDetail string, from, to int64, now time.Time, loc *time.Location) string {
	names := resourceAlertNames(notifiedDetail)
	if from <= 0 || to < from {
		return fmt.Sprintf("🟢[恢复] %s%s", label, resourceAlertDetail(names, true))
	}
	return fmt.Sprintf("🟢[恢复] %s%s，异常 %s，共 %s", label, resourceAlertDetail(names, true), formatNotificationInterval(from, to, now, loc), formatNotificationDuration(to-from))
}

// resourceAlertNames splits the stored notified_detail ("CPU、内存") back into
// rule names.
func resourceAlertNames(detail string) []string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return nil
	}
	return compactNonEmptyStrings(strings.Split(detail, "、"))
}

func joinResourceAlertNames(names []string) string {
	return strings.Join(compactNonEmptyStrings(names), "、")
}

// formatNotificationInterval renders "from–to" in the Controller's local time
// zone. Both ends use HH:MM:SS when they fall on the sending day, otherwise
// M-D HH:MM.
func formatNotificationInterval(from, to int64, now time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	start := time.Unix(from, 0).In(loc)
	end := time.Unix(to, 0).In(loc)
	today := now.In(loc)
	if sameLocalDay(start, today) && sameLocalDay(end, today) {
		return start.Format("15:04:05") + "–" + end.Format("15:04:05")
	}
	return formatNotificationMonthDay(start) + "–" + formatNotificationMonthDay(end)
}

func sameLocalDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func formatNotificationMonthDay(value time.Time) string {
	return fmt.Sprintf("%d-%d %s", int(value.Month()), value.Day(), value.Format("15:04"))
}

func formatNotificationDuration(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	switch {
	case seconds < 60:
		return fmt.Sprintf("%d 秒", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%d 分 %d 秒", seconds/60, seconds%60)
	case seconds < 86400:
		return fmt.Sprintf("%d 小时 %d 分", seconds/3600, (seconds%3600)/60)
	default:
		return fmt.Sprintf("%d 天 %d 小时", seconds/86400, (seconds%86400)/3600)
	}
}

// renewalMessageText renders a renewal key "<dueDate>#<threshold>" with the
// historical text, counting days from today.
func renewalMessageText(nodeName, key string, now time.Time) string {
	dueText, _, ok := parseRenewalNotificationKey(key)
	if !ok {
		return fmt.Sprintf("⚠️[到期] %s 即将到期", nodeName)
	}
	dueDate, err := time.ParseInLocation("2006-01-02", dueText, time.UTC)
	if err != nil {
		return fmt.Sprintf("⚠️[到期] %s 即将到期", nodeName)
	}
	return renewalDueMessageText(nodeName, formatRenewalNotificationDetail(renewalDaysRemaining(dueDate, now), dueText))
}

// notificationDroppedText explains which state was never delivered and how
// long it lasted before the actual state returned to what the user had seen.
func notificationDroppedText(kind, label string, row notificationStateRow, now time.Time, loc *time.Location) string {
	nowUnix := now.Unix()
	switch kind {
	case notificationKindLiveness, notificationKindResource:
		from := row.PendingSince
		if notificationStateIsAlert(kind, row.PendingTarget) && row.IncidentFrom > 0 {
			from = row.IncidentFrom
		}
		if from <= 0 || from > nowUnix {
			from = nowUnix
		}
		return fmt.Sprintf("未送达：%s %s %s，共 %s；已回到 %s", label, notificationStateLabel(kind, row.PendingTarget),
			formatNotificationInterval(from, nowUnix, now, loc), formatNotificationDuration(nowUnix-from), notificationStateLabel(kind, row.Notified))
	default:
		return fmt.Sprintf("未送达：%s 续费提醒 %s", label, row.PendingTarget)
	}
}

func notificationStateLabel(kind, value string) string {
	switch kind {
	case notificationKindLiveness:
		if value == livenessStateOffline {
			return "离线"
		}
		return "在线"
	case notificationKindResource:
		if value == resourceStateWarning {
			return "异常"
		}
		return "正常"
	default:
		return value
	}
}
