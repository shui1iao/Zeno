package api

import (
	"context"
	"net/http"
	"time"
)

func (h *handler) handleAdminNotificationChannels(w http.ResponseWriter, r *http.Request) {
	handleAdminReadMutation(h, w, r, http.MethodPost, http.StatusCreated, readAdminNotificationChannels, createAdminNotificationChannel, nil)
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
	parts := adminResourcePathParts(r, "/api/admin/v1/notification-channels/")
	if len(parts) == 2 && parts[0] != "" && parts[1] == "test" {
		h.handleAdminNotificationChannelTest(w, r, parts[0])
		return
	}
	channelID, ok := adminSingleResourceID(w, parts)
	if !ok {
		return
	}
	handleAdminResource(h, w, r, channelID, adminResourceOps[AdminNotificationChannelUpdateRequest, AdminNotificationChannelResponse]{
		update: updateAdminNotificationChannel,
		remove: deleteAdminNotificationChannel,
	})
}

func updateAdminNotificationChannel(ctx context.Context, store adminStore, channelID string, update AdminNotificationChannelUpdateRequest) (AdminNotificationChannelResponse, error) {
	channel, err := store.UpdateAdminNotificationChannel(ctx, channelID, update)
	return AdminNotificationChannelResponse{Channel: channel}, err
}

func deleteAdminNotificationChannel(ctx context.Context, store adminStore, channelID string) error {
	return store.DeleteAdminNotificationChannel(ctx, channelID)
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
