package api

import (
	"strings"
)

type AdminNotificationChannelsResponse struct {
	Channels []AdminNotificationChannel `json:"channels"`
}

type AdminNotificationChannelResponse struct {
	Channel AdminNotificationChannel `json:"channel"`
}

type AdminNotificationTypeResponse struct {
	Type AdminNotificationType `json:"type"`
}

type AdminAlertRulesResponse struct {
	Rules []AdminAlertRule `json:"rules"`
}

type AdminAlertRuleResponse struct {
	Rule AdminAlertRule `json:"rule"`
}

type AdminNotificationTestResponse struct {
	Delivery AdminNotificationDelivery `json:"delivery"`
}

type AdminNotificationChannelCreateRequest struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Destination string `json:"destination"`
	Credential  string `json:"credential"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

func (request *AdminNotificationChannelCreateRequest) normalize() error {
	request.ID = normalizeAdminNodeID(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.Destination = strings.TrimSpace(request.Destination)
	request.Credential = strings.TrimSpace(request.Credential)
	if request.Name == "" || request.Destination == "" || request.Credential == "" {
		return errInvalidAdminNotificationChannelWrite
	}
	return nil
}

type AdminNotificationChannelUpdateRequest struct {
	Name        *string `json:"name,omitempty"`
	Destination *string `json:"destination,omitempty"`
	Credential  *string `json:"credential,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
}

func (request *AdminNotificationChannelUpdateRequest) normalize() error {
	normalizer := newPatchNormalizer(errInvalidAdminNotificationChannelWrite)
	normalizer.text(&request.Name, trimRequired)
	normalizer.text(&request.Destination, trimRequired)
	// Notification credentials are write-only. A blank PATCH credential is
	// treated as omitted so admin forms can leave the field empty without
	// clearing or exposing the stored token.
	normalizer.clearableText(&request.Credential)
	normalizer.present(request.Enabled != nil)
	return normalizer.result()
}

type AdminNotificationTypeUpdateRequest struct {
	Enabled *bool `json:"enabled,omitempty"`
}

func (request AdminNotificationTypeUpdateRequest) normalize() error {
	if request.Enabled == nil {
		return errInvalidAdminNotificationTypeWrite
	}
	return nil
}

type AdminAlertRuleUpdateRequest struct {
	Enabled      *bool     `json:"enabled,omitempty"`
	Threshold    *float64  `json:"threshold,omitempty"`
	RenewalDays  *[]int    `json:"renewal_days,omitempty"`
	DurationSec  *int      `json:"duration_sec,omitempty"`
	ScopeNodeIDs *[]string `json:"scope_node_ids,omitempty"`
}

func (request *AdminAlertRuleUpdateRequest) normalize() error {
	normalizer := newPatchNormalizer(errInvalidAdminAlertRuleUpdate)
	normalizer.present(request.Enabled != nil)
	normalizer.float(&request.Threshold, finiteNonNegativeFloat)
	normalizer.present(request.RenewalDays != nil)
	normalizer.number(&request.DurationSec, nonNegativeInt)
	normalizer.identifierSet(&request.ScopeNodeIDs, normalizeAdminNodeID)
	if err := normalizer.result(); err != nil {
		return err
	}
	if request.Threshold != nil && request.RenewalDays != nil {
		return errInvalidAdminAlertRuleUpdate
	}
	if request.RenewalDays != nil {
		if len(*request.RenewalDays) == 0 {
			return errInvalidAdminAlertRuleUpdate
		}
		seen := make(map[int]bool, len(*request.RenewalDays))
		for _, days := range *request.RenewalDays {
			if seen[days] || !allowedRenewalNoticeDays[days] {
				return errInvalidAdminAlertRuleUpdate
			}
			seen[days] = true
		}
	}
	return nil
}

type AdminAlertRule struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Category              string   `json:"category"`
	Metric                string   `json:"metric"`
	Comparator            string   `json:"comparator"`
	Threshold             float64  `json:"threshold"`
	RenewalDays           []int    `json:"renewal_days,omitempty"`
	ThresholdUnit         string   `json:"threshold_unit"`
	DurationSec           int      `json:"duration_sec"`
	Enabled               bool     `json:"enabled"`
	NotificationEventType string   `json:"notification_event_type"`
	NotificationLabel     string   `json:"notification_label"`
	Description           string   `json:"description"`
	ScopeNodeIDs          []string `json:"scope_node_ids"`
	CreatedAt             string   `json:"created_at"`
	UpdatedAt             string   `json:"updated_at"`
}

type AdminNotificationChannel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Destination   string `json:"destination"`
	Credential    string `json:"-"`
	CredentialSet bool   `json:"credential_set"`
	Enabled       bool   `json:"enabled"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type AdminNotificationType struct {
	EventType string `json:"event_type"`
	Label     string `json:"label"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type AdminNotificationDelivery struct {
	ID             int64  `json:"id"`
	EventType      string `json:"event_type"`
	Label          string `json:"label"`
	NodeID         string `json:"node_id"`
	NodeName       string `json:"node_name"`
	PreviousStatus string `json:"previous_status"`
	Status         string `json:"status"`
	ChannelID      string `json:"channel_id"`
	ChannelName    string `json:"channel_name"`
	Success        bool   `json:"success"`
	Error          string `json:"error,omitempty"`
	CreatedAt      string `json:"created_at"`
}
