package api

import (
	"errors"
)

var (
	errInvalidAdminSettingsUpdate           = errors.New("invalid admin settings update")
	errInvalidAdminNodeUpdate               = errors.New("invalid admin node update")
	errInvalidAdminNodeCreate               = errors.New("invalid admin node create")
	errNodeAlreadyExists                    = errors.New("node already exists")
	errInvalidAdminTargetWrite              = errors.New("invalid admin probe target write")
	errProbeTargetNotFound                  = errors.New("probe target not found")
	errProbeTargetAlreadyExists             = errors.New("probe target already exists")
	errInvalidAdminNotificationChannelWrite = errors.New("invalid admin notification channel write")
	errNotificationChannelNotFound          = errors.New("notification channel not found")
	errNotificationChannelAlreadyExists     = errors.New("notification channel already exists")
	errInvalidAdminNotificationTypeWrite    = errors.New("invalid admin notification type write")
	errNotificationTypeNotFound             = errors.New("notification type not found")
	errNotificationTypeGone                 = errors.New("notification type compatibility endpoint gone")
	errInvalidAdminAlertRuleUpdate          = errors.New("invalid admin alert rule update")
	errAlertRuleNotFound                    = errors.New("alert rule not found")
)
