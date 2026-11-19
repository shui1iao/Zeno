package api

import (
	"bytes"
	"encoding/json"
	"math"
	"net"
	"strings"
	"time"
)

func normalizeAdminNodeDate(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	parsed, err := time.Parse("2006-01-02", trimmed)
	if err != nil || parsed.Format("2006-01-02") != trimmed {
		return "", errInvalidAdminNodeUpdate
	}
	return trimmed, nil
}

func normalizeAdminNodeShortText(value string, maxRunes int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len([]rune(trimmed)) > maxRunes {
		return "", errInvalidAdminNodeUpdate
	}
	return trimmed, nil
}

func normalizeAdminNodeIP(value string, family int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	parsed := net.ParseIP(trimmed)
	if parsed == nil {
		return "", errInvalidAdminNodeUpdate
	}
	if family == 4 {
		ipv4 := parsed.To4()
		if ipv4 == nil {
			return "", errInvalidAdminNodeUpdate
		}
		return ipv4.String(), nil
	}
	if family == 6 {
		if parsed.To4() != nil || parsed.To16() == nil {
			return "", errInvalidAdminNodeUpdate
		}
		return parsed.String(), nil
	}
	return "", errInvalidAdminNodeUpdate
}

func normalizeAdminNodeBillingMode(value string) (string, bool) {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "" {
		return "both", true
	}
	switch mode {
	case "in", "download", "inbound":
		return "in", true
	case "out", "upload", "outbound":
		return "out", true
	case "both", "sum", "total":
		return "both", true
	case "max", "higher":
		return "max", true
	default:
		return "", false
	}
}

func normalizeAdminNodeMonthlyResetDay(value int) (int, bool) {
	if value == 0 {
		return 1, true
	}
	if value < 1 || value > 31 {
		return 0, false
	}
	return value, true
}

// AdminNodesResponse is the authenticated management view for node inventory.
// It intentionally omits token hashes and other credentials.
type AdminNodesResponse struct {
	Nodes []AdminNode `json:"nodes"`
}

func (response AdminNodesResponse) MarshalJSON() ([]byte, error) {
	type alias AdminNodesResponse
	if response.Nodes == nil {
		response.Nodes = []AdminNode{}
	}
	return json.Marshal(alias(response))
}

type AdminNodeResponse struct {
	Node AdminNode `json:"node"`
}

type AdminNodeReorderRequest struct {
	NodeIDs []string `json:"node_ids"`
}

func (request *AdminNodeReorderRequest) normalize() error {
	return normalizeAdminOrderIDs(request.NodeIDs, errInvalidAdminNodeUpdate)
}

// normalizeAdminOrderIDs validates a complete ordered resource list once, so
// all admin reorder endpoints share the same duplicate/missing-id contract.
func normalizeAdminOrderIDs(ids []string, invalid error) error {
	if len(ids) == 0 {
		return invalid
	}
	seen := make(map[string]struct{}, len(ids))
	for index, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return invalid
		}
		if _, exists := seen[id]; exists {
			return invalid
		}
		seen[id] = struct{}{}
		ids[index] = id
	}
	return nil
}

type AdminNodeInstallCommandRequest struct {
	ControllerURL string `json:"controller_url,omitempty"`
}

type AdminNodeInstallCommandResponse struct {
	NodeID                  string            `json:"node_id"`
	Command                 string            `json:"command"`
	Commands                map[string]string `json:"commands,omitempty"`
	EnrollmentExpiresAt     string            `json:"enrollment_expires_at"`
	EnrollmentOneTime       bool              `json:"enrollment_one_time"`
	SupersedesPreviousToken bool              `json:"supersedes_previous_enrollment"`
}

type AdminNodeCreateRequest struct {
	ID                string             `json:"id,omitempty"`
	DisplayName       string             `json:"display_name"`
	CountryCode       string             `json:"country_code,omitempty"`
	Region            string             `json:"region,omitempty"`
	ExpiryDate        string             `json:"expiry_date,omitempty"`
	ExpiryPermanent   bool               `json:"expiry_permanent,omitempty"`
	BillingCycle      string             `json:"billing_cycle,omitempty"`
	RenewalAmount     adminOptionalFloat `json:"renewal_amount,omitempty"`
	RenewalCurrency   string             `json:"renewal_currency,omitempty"`
	BillingMode       string             `json:"billing_mode,omitempty"`
	MonthlyResetDay   *int               `json:"monthly_reset_day,omitempty"`
	DisplayOrder      int                `json:"display_order,omitempty"`
	PublicIPv4        string             `json:"public_ipv4,omitempty"`
	PublicIPv6        string             `json:"public_ipv6,omitempty"`
	MonthlyQuotaBytes adminOptionalInt64 `json:"monthly_quota_bytes,omitempty"`
	Disabled          bool               `json:"disabled,omitempty"`
}

func (request *AdminNodeCreateRequest) normalize() error {
	trimmedName := strings.TrimSpace(request.DisplayName)
	if trimmedName == "" {
		return errInvalidAdminNodeCreate
	}
	request.DisplayName = trimmedName
	request.ID = strings.TrimSpace(request.ID)
	request.CountryCode = strings.ToUpper(strings.TrimSpace(request.CountryCode))
	if len(request.CountryCode) > 8 {
		return errInvalidAdminNodeCreate
	}
	request.Region = strings.TrimSpace(request.Region)
	expiryDate, err := normalizeAdminNodeDate(request.ExpiryDate)
	if err != nil {
		return errInvalidAdminNodeCreate
	}
	request.ExpiryDate = expiryDate
	billingCycle, err := normalizeAdminNodeShortText(request.BillingCycle, 64)
	if err != nil {
		return errInvalidAdminNodeCreate
	}
	request.BillingCycle = billingCycle
	if request.RenewalAmount.Set && request.RenewalAmount.Valid {
		amount, ok := normalizeAdminNodeRenewalAmount(request.RenewalAmount.Value)
		if !ok {
			return errInvalidAdminNodeCreate
		}
		request.RenewalAmount.Value = amount
	}
	currency, ok := normalizeAdminNodeRenewalCurrency(request.RenewalCurrency)
	if !ok {
		return errInvalidAdminNodeCreate
	}
	request.RenewalCurrency = currency
	billingMode, ok := normalizeAdminNodeBillingMode(request.BillingMode)
	if !ok {
		return errInvalidAdminNodeCreate
	}
	request.BillingMode = billingMode
	if request.MonthlyResetDay == nil {
		defaultResetDay := 1
		request.MonthlyResetDay = &defaultResetDay
	} else {
		if *request.MonthlyResetDay == 0 {
			return errInvalidAdminNodeCreate
		}
		resetDay, ok := normalizeAdminNodeMonthlyResetDay(*request.MonthlyResetDay)
		if !ok {
			return errInvalidAdminNodeCreate
		}
		request.MonthlyResetDay = &resetDay
	}
	if request.DisplayOrder < 0 {
		return errInvalidAdminNodeCreate
	}
	publicIPv4, err := normalizeAdminNodeIP(request.PublicIPv4, 4)
	if err != nil {
		return errInvalidAdminNodeCreate
	}
	request.PublicIPv4 = publicIPv4
	publicIPv6, err := normalizeAdminNodeIP(request.PublicIPv6, 6)
	if err != nil {
		return errInvalidAdminNodeCreate
	}
	request.PublicIPv6 = publicIPv6
	if request.MonthlyQuotaBytes.Set && request.MonthlyQuotaBytes.Valid && request.MonthlyQuotaBytes.Value < 0 {
		return errInvalidAdminNodeCreate
	}
	return nil
}

type AdminNodeUpdateRequest struct {
	DisplayName       *string            `json:"display_name,omitempty"`
	CountryCode       *string            `json:"country_code,omitempty"`
	Region            *string            `json:"region,omitempty"`
	HomeProbeTargetID *string            `json:"home_probe_target_id,omitempty"`
	ExpiryDate        *string            `json:"expiry_date,omitempty"`
	ExpiryPermanent   *bool              `json:"expiry_permanent,omitempty"`
	BillingCycle      *string            `json:"billing_cycle,omitempty"`
	RenewalAmount     adminOptionalFloat `json:"renewal_amount,omitempty"`
	RenewalCurrency   *string            `json:"renewal_currency,omitempty"`
	BillingMode       *string            `json:"billing_mode,omitempty"`
	MonthlyResetDay   *int               `json:"monthly_reset_day,omitempty"`
	DisplayOrder      *int               `json:"display_order,omitempty"`
	PublicIPv4        *string            `json:"public_ipv4,omitempty"`
	PublicIPv6        *string            `json:"public_ipv6,omitempty"`
	MonthlyQuotaBytes adminOptionalInt64 `json:"monthly_quota_bytes,omitempty"`
	Disabled          *bool              `json:"disabled,omitempty"`
	ProbeTargetIDs    []string           `json:"probe_target_ids,omitempty"`
}

func (request *AdminNodeUpdateRequest) normalize() error {
	normalizer := newPatchNormalizer(errInvalidAdminNodeUpdate)
	normalizer.text(&request.DisplayName, trimRequired)
	normalizer.text(&request.CountryCode, trimUpperMax(8))
	normalizer.text(&request.Region, trimOptional)
	normalizer.text(&request.HomeProbeTargetID, trimOptional)
	normalizer.text(&request.ExpiryDate, fromError(normalizeAdminNodeDate))
	normalizer.present(request.ExpiryPermanent != nil)
	normalizer.text(&request.BillingCycle, fromError(func(value string) (string, error) {
		return normalizeAdminNodeShortText(value, 64)
	}))
	normalizer.optionalFloat(&request.RenewalAmount, normalizeAdminNodeRenewalAmount)
	normalizer.text(&request.RenewalCurrency, normalizeAdminNodeRenewalCurrency)
	normalizer.text(&request.BillingMode, normalizeAdminNodeBillingMode)
	// A reset day of zero is rejected outright: on update it means the client
	// sent an explicit zero rather than omitting the field.
	normalizer.number(&request.MonthlyResetDay, func(value int) (int, bool) {
		if value == 0 {
			return value, false
		}
		return normalizeAdminNodeMonthlyResetDay(value)
	})
	normalizer.number(&request.DisplayOrder, nonNegativeInt)
	normalizer.text(&request.PublicIPv4, fromError(func(value string) (string, error) {
		return normalizeAdminNodeIP(value, 4)
	}))
	normalizer.text(&request.PublicIPv6, fromError(func(value string) (string, error) {
		return normalizeAdminNodeIP(value, 6)
	}))
	normalizer.optionalInt64(&request.MonthlyQuotaBytes, nonNegativeInt64)
	normalizer.present(request.Disabled != nil)
	normalizer.identifiers(&request.ProbeTargetIDs)
	return normalizer.result()
}

type adminOptionalInt64 struct {
	Set   bool
	Valid bool
	Value int64
}

type adminOptionalFloat struct {
	Set   bool
	Valid bool
	Value float64
}

func (value *adminOptionalFloat) UnmarshalJSON(data []byte) error {
	return unmarshalAdminOptionalNumber(data, &value.Set, &value.Valid, &value.Value)
}

func (value *adminOptionalInt64) UnmarshalJSON(data []byte) error {
	return unmarshalAdminOptionalNumber(data, &value.Set, &value.Valid, &value.Value)
}

func unmarshalAdminOptionalNumber[T int64 | float64](data []byte, set, valid *bool, value *T) error {
	*set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*valid = false
		*value = 0
		return nil
	}
	var parsed T
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*valid = true
	*value = parsed
	return nil
}

type AdminNode struct {
	ID                string   `json:"id"`
	DisplayName       string   `json:"display_name"`
	Status            string   `json:"status"`
	CountryCode       string   `json:"country_code,omitempty"`
	Region            string   `json:"region,omitempty"`
	HomeProbeTargetID string   `json:"home_probe_target_id,omitempty"`
	Disabled          bool     `json:"disabled"`
	BillingMode       string   `json:"billing_mode"`
	MonthlyResetDay   int      `json:"monthly_reset_day"`
	ExpiryDate        string   `json:"expiry_date,omitempty"`
	ExpiryPermanent   bool     `json:"expiry_permanent"`
	BillingCycle      string   `json:"billing_cycle,omitempty"`
	RenewalAmount     *float64 `json:"renewal_amount,omitempty"`
	RenewalCurrency   string   `json:"renewal_currency"`
	DisplayOrder      int      `json:"display_order"`
	PublicIPv4        string   `json:"public_ipv4,omitempty"`
	PublicIPv6        string   `json:"public_ipv6,omitempty"`
	MonthlyQuotaBytes *int64   `json:"monthly_quota_bytes,omitempty"`
	LastSeenAt        *string  `json:"last_seen_at,omitempty"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
	Hostname          string   `json:"hostname,omitempty"`
	OSName            string   `json:"os_name,omitempty"`
	OSVersion         string   `json:"os_version,omitempty"`
	Kernel            string   `json:"kernel,omitempty"`
	Arch              string   `json:"arch,omitempty"`
	Virtualization    string   `json:"virtualization,omitempty"`
	CPUModel          string   `json:"cpu_model,omitempty"`
	CPUCores          *int     `json:"cpu_cores,omitempty"`
	MemoryTotalBytes  *int64   `json:"memory_total_bytes,omitempty"`
	DiskTotalBytes    *int64   `json:"disk_total_bytes,omitempty"`
	BootTime          *string  `json:"boot_time,omitempty"`
	AgentVersion      string   `json:"agent_version,omitempty"`
}

var adminNodeRenewalCurrencies = map[string]struct{}{
	"CNY": {},
	"USD": {},
	"HKD": {},
	"EUR": {},
	"GBP": {},
	"JPY": {},
	"SGD": {},
	"AUD": {},
	"CAD": {},
	"KRW": {},
}

func normalizeAdminNodeRenewalCurrency(value string) (string, bool) {
	currency := strings.ToUpper(strings.TrimSpace(value))
	if currency == "" {
		currency = "CNY"
	}
	_, ok := adminNodeRenewalCurrencies[currency]
	return currency, ok
}

func normalizeAdminNodeRenewalAmount(value float64) (float64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > 1_000_000_000 {
		return 0, false
	}
	return math.Round(value*100) / 100, true
}
