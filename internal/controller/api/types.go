package api

type SummaryResponse struct {
	Nodes         []Node             `json:"nodes"`
	Services      []ServiceTarget    `json:"services"`
	LatencyPoints []LatencyPoint     `json:"latency_points"`
	ExchangeRates map[string]float64 `json:"exchange_rates"`
}

type LatencyResponse struct {
	NodeID string         `json:"node_id"`
	Range  string         `json:"range"`
	Points []LatencyPoint `json:"points"`
}

type StateResponse struct {
	NodeID string       `json:"node_id"`
	Range  string       `json:"range"`
	Points []StatePoint `json:"points"`
}

type Node struct {
	ID                   string           `json:"id"`
	DisplayName          string           `json:"display_name"`
	Status               string           `json:"status"`
	OS                   string           `json:"os"`
	OSVersion            string           `json:"os_version,omitempty"`
	Kernel               string           `json:"kernel,omitempty"`
	Arch                 string           `json:"arch,omitempty"`
	Virtualization       string           `json:"virtualization,omitempty"`
	CPUModel             string           `json:"cpu_model,omitempty"`
	CountryCode          string           `json:"country_code,omitempty"`
	Subtitle             string           `json:"subtitle,omitempty"`
	CPUCores             *float64         `json:"cpu_cores,omitempty"`
	ExpiryLabel          string           `json:"expiry_label,omitempty"`
	RenewalAmount        *float64         `json:"renewal_amount,omitempty"`
	RenewalCurrency      string           `json:"renewal_currency,omitempty"`
	BillingCycle         string           `json:"billing_cycle,omitempty"`
	MonthlyCostCNY       *float64         `json:"monthly_cost_cny,omitempty"`
	CPUPercent           *float64         `json:"cpu_percent"`
	MemoryUsedBytes      *float64         `json:"memory_used_bytes"`
	MemoryTotalBytes     *float64         `json:"memory_total_bytes"`
	DiskUsedBytes        *float64         `json:"disk_used_bytes"`
	DiskTotalBytes       *float64         `json:"disk_total_bytes"`
	BootTime             *string          `json:"boot_time,omitempty"`
	Load1                *float64         `json:"load1,omitempty"`
	Load5                *float64         `json:"load5,omitempty"`
	Load15               *float64         `json:"load15,omitempty"`
	UptimeSeconds        *float64         `json:"uptime_seconds,omitempty"`
	NetInSpeedBps        *float64         `json:"net_in_speed_bps"`
	NetOutSpeedBps       *float64         `json:"net_out_speed_bps"`
	NetInTotalBytes      *float64         `json:"net_in_total_bytes"`
	NetOutTotalBytes     *float64         `json:"net_out_total_bytes"`
	NetInLifetimeBytes   *float64         `json:"net_in_lifetime_bytes"`
	NetOutLifetimeBytes  *float64         `json:"net_out_lifetime_bytes"`
	BillingMode          string           `json:"billing_mode,omitempty"`
	MonthlyResetDay      int              `json:"monthly_reset_day,omitempty"`
	MonthlyPeriodStart   string           `json:"monthly_period_start,omitempty"`
	MonthlyPeriodEnd     string           `json:"monthly_period_end,omitempty"`
	MonthlyBillableBytes *float64         `json:"monthly_billable_bytes"`
	MonthlyQuotaBytes    *float64         `json:"monthly_quota_bytes"`
	LatencySummary       *LatencySummary  `json:"latency_summary,omitempty"`
	LatencySummaries     []LatencySummary `json:"latency_summaries,omitempty"`
}

type LatencySummary struct {
	TargetID      string               `json:"target_id"`
	TargetName    string               `json:"target_name"`
	MedianMS      *float64             `json:"median_ms"`
	AvgMS         *float64             `json:"avg_ms"`
	LossPercent   *float64             `json:"loss_percent"`
	UpdatedAt     string               `json:"updated_at"`
	HourlyHistory []HourlyLatencyPoint `json:"hourly_history,omitempty"`
}

type HourlyLatencyPoint struct {
	StartedAt   string   `json:"started_at"`
	LatencyMS   *float64 `json:"latency_ms"`
	LossPercent *float64 `json:"loss_percent"`
}

type LatencyPoint struct {
	TS          string   `json:"ts"`
	TargetID    string   `json:"target_id"`
	TargetName  string   `json:"target_name"`
	MedianMS    *float64 `json:"median_ms"`
	AvgMS       *float64 `json:"avg_ms"`
	LossPercent float64  `json:"loss_percent"`
}

type ServiceTarget struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Type               string   `json:"type"`
	AssignedNodeCount  int      `json:"assigned_node_count"`
	ReportingNodeCount int      `json:"reporting_node_count"`
	MedianMS           *float64 `json:"median_ms"`
	AvgMS              *float64 `json:"avg_ms"`
	LossPercent        *float64 `json:"loss_percent"`
	UpdatedAt          string   `json:"updated_at,omitempty"`
}

type StatePoint struct {
	TS                 string   `json:"ts"`
	CPUPercent         *float64 `json:"cpu_percent"`
	Load1              *float64 `json:"load1"`
	Load5              *float64 `json:"load5"`
	Load15             *float64 `json:"load15"`
	MemoryUsedBytes    *float64 `json:"memory_used_bytes"`
	MemoryTotalBytes   *float64 `json:"memory_total_bytes"`
	SwapUsedBytes      *float64 `json:"swap_used_bytes"`
	SwapTotalBytes     *float64 `json:"swap_total_bytes"`
	DiskUsedBytes      *float64 `json:"disk_used_bytes"`
	DiskTotalBytes     *float64 `json:"disk_total_bytes"`
	NetInTotalBytes    *float64 `json:"net_in_total_bytes"`
	NetOutTotalBytes   *float64 `json:"net_out_total_bytes"`
	NetInSpeedBps      *float64 `json:"net_in_speed_bps"`
	NetOutSpeedBps     *float64 `json:"net_out_speed_bps"`
	ProcessCount       *float64 `json:"process_count"`
	TCPConnectionCount *float64 `json:"tcp_connection_count"`
	UDPConnectionCount *float64 `json:"udp_connection_count"`
	UptimeSeconds      *float64 `json:"uptime_seconds"`
}
