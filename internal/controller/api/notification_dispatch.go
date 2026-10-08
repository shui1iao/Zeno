package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// notificationSendTimeout bounds one Telegram request. A response that does
// not arrive in time is a failed (retryable) delivery, even if the request was
// already written: the user prefers an occasional duplicate to a lost message.
const notificationSendTimeout = 15 * time.Second

const notificationTestMessageText = "Zeno：通知渠道测试"

type notificationDispatchChannel struct {
	ID                     string
	Name                   string
	Type                   string
	Destination            string
	Credential             string
	DeliveryVersion        int64
	DestinationFingerprint string
}

// notificationEvent describes the admin test delivery returned by the channel
// test endpoint.
type notificationEvent struct {
	EventType      string
	Label          string
	NodeID         string
	NodeName       string
	Status         string
	PreviousStatus string
	TS             string
}

type notificationSender interface {
	Send(ctx context.Context, channel notificationDispatchChannel, text string) error
}

// automaticNotificationsAllowed is the global gate. The sender is nil when
// ZENO_NOTIFICATIONS_DISABLED is set or the external notification authority
// does not match this database.
func (h *handler) automaticNotificationsAllowed() bool {
	return h != nil && h.notificationSender != nil
}

type httpNotificationSender struct {
	client             *http.Client
	telegramAPIBaseURL string
}

func newHTTPNotificationSender(client *http.Client, telegramAPIBaseURL string) notificationSender {
	if client == nil {
		client = &http.Client{Timeout: notificationSendTimeout}
	}
	telegramAPIBaseURL = strings.TrimRight(strings.TrimSpace(telegramAPIBaseURL), "/")
	if telegramAPIBaseURL == "" {
		telegramAPIBaseURL = "https://api.telegram.org"
	}
	return httpNotificationSender{client: client, telegramAPIBaseURL: telegramAPIBaseURL}
}

// telegramRateLimitError carries Telegram's parameters.retry_after so the
// channel waits exactly as long as Telegram asked.
type telegramRateLimitError struct {
	RetryAfter time.Duration
}

func (err telegramRateLimitError) Error() string {
	return fmt.Sprintf("telegram rate limited; retry after %ds", int64(err.RetryAfter/time.Second))
}

func (sender httpNotificationSender) Send(ctx context.Context, channel notificationDispatchChannel, text string) error {
	if channel.Type != "" && strings.ToLower(strings.TrimSpace(channel.Type)) != "telegram" {
		return fmt.Errorf("unsupported notification channel type")
	}
	botCredential := strings.TrimSpace(channel.Credential)
	chatID := strings.TrimSpace(channel.Destination)
	if botCredential == "" || chatID == "" {
		return fmt.Errorf("missing telegram destination")
	}
	endpoint := sender.telegramAPIBaseURL + "/bot" + url.PathEscape(botCredential) + "/sendMessage"
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", text)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", "Zeno-Controller")
	response, err := sender.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if response.StatusCode == http.StatusTooManyRequests {
		return telegramRateLimitError{RetryAfter: telegramRetryAfter(body)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("telegram returned status %d", response.StatusCode)
	}
	return nil
}

func telegramRetryAfter(body []byte) time.Duration {
	var payload struct {
		Parameters struct {
			RetryAfter int64 `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Parameters.RetryAfter <= 0 {
		return 0
	}
	return time.Duration(payload.Parameters.RetryAfter) * time.Second
}

// notificationDestinationFingerprint identifies a channel's routing target.
// Together with delivery_version it forms the binding recorded on every
// notification state row.
func notificationDestinationFingerprint(channelType, destination string) string {
	channelType = strings.ToLower(strings.TrimSpace(channelType))
	if channelType == "" {
		channelType = "telegram"
	}
	sum := sha256.Sum256([]byte(channelType + "\x00" + strings.TrimSpace(destination)))
	return hex.EncodeToString(sum[:])
}

func notificationNodeLabel(nodeName, nodeID, nodeIP string) string {
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		nodeName = nodeID
	}
	if maskedIP := maskIPv4(nodeIP); maskedIP != "" {
		return fmt.Sprintf("%s(%s)", nodeName, maskedIP)
	}
	return nodeName
}

func maskIPv4(value string) string {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 4 {
		return ""
	}
	for _, part := range parts {
		if part == "" {
			return ""
		}
	}
	return parts[0] + "." + parts[1] + ".***.***"
}

func renewalDueMessageText(nodeName, detail string) string {
	parts := strings.Split(strings.TrimSpace(detail), "，")
	statusText := strings.TrimSpace(parts[0])
	dateText := ""
	if len(parts) > 1 {
		dateText = formatRenewalMessageDate(strings.TrimSpace(parts[len(parts)-1]))
	}
	if dateText == "" {
		return fmt.Sprintf("⚠️[到期] %s 即将到期", nodeName)
	}
	if statusText == "今天到期" {
		return fmt.Sprintf("⚠️[到期] %s 今天（%s）到期", nodeName, dateText)
	}
	if strings.HasPrefix(statusText, "还有 ") && strings.HasSuffix(statusText, " 天到期") {
		days := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(statusText, "还有 "), " 天到期"))
		if days != "" {
			return fmt.Sprintf("⚠️[到期] %s 将于 %s 天后（%s）到期", nodeName, days, dateText)
		}
	}
	if strings.HasPrefix(statusText, "已过期 ") && strings.HasSuffix(statusText, " 天") {
		days := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(statusText, "已过期 "), " 天"))
		if days != "" {
			return fmt.Sprintf("⚠️[到期] %s 已于 %s 天前（%s）到期", nodeName, days, dateText)
		}
	}
	return fmt.Sprintf("⚠️[到期] %s 将于 %s 到期", nodeName, dateText)
}

func formatRenewalMessageDate(value string) string {
	date, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(value), time.UTC)
	if err != nil {
		return strings.TrimSpace(value)
	}
	year, month, day := date.Date()
	return fmt.Sprintf("%d-%d-%d", year, int(month), day)
}

func sanitizeNotificationDeliveryError(err error) string {
	if err == nil {
		return ""
	}
	var rateLimited telegramRateLimitError
	if errors.As(err, &rateLimited) {
		return rateLimited.Error()
	}
	if errors.Is(err, errNotificationCredentialUnavailable) {
		return "notification credential unavailable"
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "delivery failed"
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") {
		return "delivery timed out"
	}
	if strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host") || strings.Contains(lower, "network is unreachable") {
		return "delivery connection failed"
	}
	if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") || strings.Contains(lower, "bearer ") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "credential") || strings.Contains(lower, "/bot") {
		return "delivery failed"
	}
	if len(message) > 200 {
		message = message[:200]
	}
	return message
}
