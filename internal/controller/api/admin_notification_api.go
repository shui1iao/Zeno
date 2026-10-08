package api

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func (h *handler) handleAdminNotificationChannels(w http.ResponseWriter, r *http.Request) {
	handleAdminReadMutation(h, w, r, http.MethodPost, http.StatusCreated, readAdminNotificationChannels, createAdminNotificationChannel)
}

func readAdminNotificationChannels(ctx context.Context, store adminStore) (AdminNotificationChannelsResponse, error) {
	channels, err := store.AdminNotificationChannels(ctx)
	return AdminNotificationChannelsResponse{Channels: channels}, err
}

func createAdminNotificationChannel(ctx context.Context, store adminStore, create AdminNotificationChannelCreateRequest) (AdminNotificationChannelResponse, error) {
	channel, err := store.CreateAdminNotificationChannel(ctx, create)
	return AdminNotificationChannelResponse{Channel: channel}, err
}

func (h *handler) handleAdminNotificationChannelResource(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/v1/notification-channels/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] == "test" {
		h.handleAdminNotificationChannelTest(w, r, parts[0])
		return
	}
	if len(parts) != 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		if err := store.DeleteAdminNotificationChannel(r.Context(), parts[0]); err != nil {
			writeAdminError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var update AdminNotificationChannelUpdateRequest
	if !decodeJSONBody(w, r, &update, adminJSONBodyLimit, true) {
		return
	}
	channel, err := store.UpdateAdminNotificationChannel(r.Context(), parts[0], update)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, AdminNotificationChannelResponse{Channel: channel})
}

func (h *handler) handleAdminNotificationChannelTest(w http.ResponseWriter, r *http.Request, channelID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	if h.notificationSender == nil {
		writeError(w, http.StatusConflict, "notification delivery disabled")
		return
	}
	channel, err := store.AdminNotificationDispatchChannel(r.Context(), channelID)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	now := time.Now().UTC()
	event := adminTestNotificationEvent(now)
	sendCtx, cancel := context.WithTimeout(r.Context(), h.notificationTimeout())
	defer cancel()
	sendErr := h.notificationSender.Send(sendCtx, channel, notificationTestMessageText)
	delivery := AdminNotificationDelivery{
		EventType:      event.EventType,
		Label:          event.Label,
		NodeID:         event.NodeID,
		NodeName:       event.NodeName,
		PreviousStatus: event.PreviousStatus,
		Status:         event.Status,
		ChannelID:      channel.ID,
		ChannelName:    channel.Name,
		Success:        sendErr == nil,
		Error:          sanitizeNotificationDeliveryError(sendErr),
		CreatedAt:      now.Format(time.RFC3339),
	}
	writeJSON(w, http.StatusOK, AdminNotificationTestResponse{Delivery: delivery})
}

func adminTestNotificationEvent(ts time.Time) notificationEvent {
	return notificationEvent{
		EventType:      "test_notification",
		Label:          "测试发送",
		NodeID:         "admin-test",
		NodeName:       "Zeno",
		Status:         "test",
		PreviousStatus: "test",
		TS:             ts.Format(time.RFC3339),
	}
}

func (h *handler) handleAdminNotificationTypeResource(w http.ResponseWriter, r *http.Request) {
	handleAdminPatchResource(h, w, r, "/api/admin/v1/notification-types/", updateAdminNotificationType)
}

func updateAdminNotificationType(ctx context.Context, store adminStore, eventType string, update AdminNotificationTypeUpdateRequest) (AdminNotificationTypeResponse, error) {
	notificationType, err := store.UpdateAdminNotificationType(ctx, eventType, update)
	return AdminNotificationTypeResponse{Type: notificationType}, err
}
