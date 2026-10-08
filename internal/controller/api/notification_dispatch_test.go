package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNotificationMessageTexts(t *testing.T) {
	now := shanghaiTime(2026, 10, 8, 3, 44, 47)
	label := notificationNodeLabel("Example Relay", "relay", "203.0.113.9")
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"offline alert is unchanged", livenessAlertText(label), "🔴[离线] Example Relay(203.0.***.***)"},
		{"label falls back to id without address", livenessAlertText(notificationNodeLabel("", "relay", "")), "🔴[离线] relay"},
		{"ipv6 or malformed address is not shown", notificationNodeLabel("Relay", "relay", "2001:db8::1"), "Relay"},
		{"cpu warning is unchanged", resourceAlertText(label, []string{"CPU"}), "⚠️[警告] Example Relay(203.0.***.***)CPU持续占用过高"},
		{"multi-rule warning is unchanged", resourceAlertText(label, []string{"CPU", "内存"}), "⚠️[警告] Example Relay(203.0.***.***)CPU、内存持续占用过高"},
		{
			"offline recovery on the same day",
			livenessRecoveryText(label, shanghaiTime(2026, 10, 8, 3, 42, 27).Unix(), shanghaiTime(2026, 10, 8, 3, 43, 47).Unix(), now, testShanghai),
			"🟢[恢复] Example Relay(203.0.***.***) 离线 03:42:27–03:43:47，共 1 分 20 秒",
		},
		{
			"offline recovery across days",
			livenessRecoveryText(label, shanghaiTime(2026, 10, 6, 20, 33, 27).Unix(), shanghaiTime(2026, 10, 8, 3, 43, 0).Unix(), now, testShanghai),
			"🟢[恢复] Example Relay(203.0.***.***) 离线 10-6 20:33–10-8 03:43，共 1 天 7 小时",
		},
		{
			"offline recovery started yesterday",
			livenessRecoveryText(label, shanghaiTime(2026, 10, 7, 23, 58, 10).Unix(), shanghaiTime(2026, 10, 8, 0, 3, 20).Unix(), now, testShanghai),
			"🟢[恢复] Example Relay(203.0.***.***) 离线 10-7 23:58–10-8 00:03，共 5 分 10 秒",
		},
		{"offline recovery without incident start", livenessRecoveryText(label, 0, now.Unix(), now, testShanghai), "🟢[恢复] Example Relay(203.0.***.***)"},
		{
			"resource recovery names the alerted rules",
			resourceRecoveryText(label, "CPU、内存", shanghaiTime(2026, 10, 8, 1, 10, 0).Unix(), shanghaiTime(2026, 10, 8, 3, 25, 30).Unix(), now, testShanghai),
			"🟢[恢复] Example Relay(203.0.***.***)CPU、内存恢复正常，异常 01:10:00–03:25:30，共 2 小时 15 分",
		},
		{"resource recovery without incident start", resourceRecoveryText(label, "硬盘", 0, now.Unix(), now, testShanghai), "🟢[恢复] Example Relay(203.0.***.***)硬盘恢复正常"},
		{"renewal future", renewalMessageText("Example Harbor", "2026-10-10#3", shanghaiTime(2026, 10, 7, 9, 0, 0)), "⚠️[到期] Example Harbor 将于 3 天后（2026-10-10）到期"},
		{"renewal counts days from today", renewalMessageText("Example Harbor", "2026-10-10#3", shanghaiTime(2026, 10, 8, 9, 0, 0)), "⚠️[到期] Example Harbor 将于 2 天后（2026-10-10）到期"},
		{"renewal today", renewalMessageText("Example Harbor", "2026-07-10#1", time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)), "⚠️[到期] Example Harbor 今天（2026-7-10）到期"},
		{"renewal expired", renewalMessageText("Example Harbor", "2026-07-10#1", time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC)), "⚠️[到期] Example Harbor 已于 2 天前（2026-7-10）到期"},
		{"renewal detail format is unchanged", renewalDueMessageText("Example Harbor", "还有 1 天到期，2026-07-10"), "⚠️[到期] Example Harbor 将于 1 天后（2026-7-10）到期"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("text = %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestNotificationDurationFormatting(t *testing.T) {
	for seconds, want := range map[int64]string{
		0: "0 秒", 59: "59 秒", 60: "1 分 0 秒", 80: "1 分 20 秒", 3599: "59 分 59 秒",
		3600: "1 小时 0 分", 8130: "2 小时 15 分", 86399: "23 小时 59 分", 86400: "1 天 0 小时", 111573: "1 天 6 小时",
	} {
		if got := formatNotificationDuration(seconds); got != want {
			t.Fatalf("formatNotificationDuration(%d) = %q, want %q", seconds, got, want)
		}
	}
}

// A request that was written but never answered is an ordinary failure: the
// reconcile loop retries it. There is no "outcome unknown" state any more.
func TestTelegramTimeoutAfterRequestWriteIsAPlainRetryableFailure(t *testing.T) {
	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		received.Add(1)
		<-r.Context().Done()
	}))
	defer server.Close()
	sender := newHTTPNotificationSender(server.Client(), server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := sender.Send(ctx, notificationDispatchChannel{ID: "ops", Destination: "7579942307", Credential: "telegram-bot-secret-value", Type: "telegram"}, "🔴[离线] Tarek")
	if err == nil || received.Load() != 1 {
		t.Fatalf("send error = %v received=%d, want a timeout after the request reached the server", err, received.Load())
	}
	if got := sanitizeNotificationDeliveryError(err); got != "delivery timed out" {
		t.Fatalf("sanitized error = %q", got)
	}
}

func TestTelegramDefaultClientTimeoutIsFifteenSeconds(t *testing.T) {
	sender, ok := newHTTPNotificationSender(nil, "").(httpNotificationSender)
	if !ok || sender.client.Timeout != 15*time.Second || notificationSendTimeout != 15*time.Second {
		t.Fatalf("default sender = %+v, want 15s timeout", sender)
	}
	if sender.telegramAPIBaseURL != "https://api.telegram.org" {
		t.Fatalf("default base URL = %q", sender.telegramAPIBaseURL)
	}
}

func TestTelegramRateLimitParsesRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"parameters":{"retry_after":23}}`))
	}))
	defer server.Close()
	err := newHTTPNotificationSender(server.Client(), server.URL).Send(context.Background(),
		notificationDispatchChannel{ID: "ops", Destination: "1", Credential: "secret", Type: "telegram"}, "text")
	var rateLimited telegramRateLimitError
	if !errors.As(err, &rateLimited) || rateLimited.RetryAfter != 23*time.Second {
		t.Fatalf("error = %#v, want retry_after 23s", err)
	}
	if telegramRetryAfter([]byte(`not json`)) != 0 || telegramRetryAfter([]byte(`{"parameters":{}}`)) != 0 {
		t.Fatal("missing retry_after must fall back to the regular backoff")
	}
}

func TestSanitizeNotificationDeliveryErrorNeverLeaksCredentials(t *testing.T) {
	for _, err := range []error{
		errors.New(`Post "https://api.telegram.org/bottelegram-bot-secret-value/sendMessage": EOF`),
		errors.New("bearer token secret"),
		errors.New("credential rejected"),
	} {
		got := sanitizeNotificationDeliveryError(err)
		if strings.Contains(got, "secret") || strings.Contains(got, "/bot") || strings.Contains(got, "telegram-bot") {
			t.Fatalf("sanitized %q leaked: %q", err, got)
		}
	}
	if got := sanitizeNotificationDeliveryError(errNotificationCredentialUnavailable); got != "notification credential unavailable" {
		t.Fatalf("credential error = %q", got)
	}
}
