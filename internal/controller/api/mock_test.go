package api

import (
	"time"
)

const kib = 1024
const mib = 1024 * kib
const gib = 1024 * mib
const tib = 1024 * gib

func ptr(v float64) *float64 { return &v }
func kb(v float64) *float64  { return ptr(v * kib) }
func mb(v float64) *float64  { return ptr(v * mib) }
func gb(v float64) *float64  { return ptr(v * gib) }
func tb(v float64) *float64  { return ptr(v * tib) }

func used(total *float64, percent float64) *float64 {
	if total == nil {
		return nil
	}
	return ptr(*total * percent / 100)
}

func mockOSVersion(osName string) string {
	if osName == "windows" {
		return "11"
	}
	return "13"
}

func mockKernel(osName string) string {
	if osName == "windows" {
		return "10.0.26100"
	}
	return "6.12.0"
}

func mockVirtualization(osName string) string {
	if osName == "windows" {
		return "hyper-v"
	}
	return "kvm"
}

func mockCPUModel(osName string, cores float64) string {
	if osName == "windows" {
		return "AMD Ryzen 9 7945HX"
	}
	if cores >= 4 {
		return "AMD EPYC 7B13"
	}
	return "Intel Xeon Virtual CPU"
}

func mockHourlyLatencyHistory(latency, loss *float64) []HourlyLatencyPoint {
	start := time.Date(2026, 7, 2, 2, 0, 0, 0, time.UTC)
	latencyScale := []float64{0.72, 0.84, 0.93, 1.08, 1.22, 0.96, 1.42, 1.16, 0.88, 1.31, 1.04, 1}
	lossOffset := []float64{0, 0.1, 0, 0.35, 1.2, 0.05, 3.8, 0.2, 0, 1.6, 0.1, 0}
	history := make([]HourlyLatencyPoint, homeLatencyHistoryBucketCount)
	for index := range history {
		history[index].StartedAt = start.Add(time.Duration(index) * time.Hour).Format(time.RFC3339)
		if latency != nil {
			value := *latency * latencyScale[index]
			history[index].LatencyMS = &value
		}
		if loss != nil {
			value := *loss + lossOffset[index]
			history[index].LossPercent = &value
		}
	}
	return history
}

func mockNode(id, name, osName, countryCode string, cores float64, expiry string, cpuPercent float64, memoryTotal *float64, memoryPercent float64, diskTotal *float64, diskPercent float64, monthlyBillable, monthlyQuota, netOutSpeed, netInSpeed *float64, latency, loss *float64) Node {
	var latencySummary *LatencySummary
	if latency != nil || loss != nil {
		latencySummary = &LatencySummary{TargetID: "default", TargetName: "Default", MedianMS: latency, AvgMS: latency, LossPercent: loss, UpdatedAt: "2026-07-02T12:10:00Z", HourlyHistory: mockHourlyLatencyHistory(latency, loss)}
	}
	return Node{
		ID:                   id,
		DisplayName:          name,
		Status:               "online",
		OS:                   osName,
		OSVersion:            mockOSVersion(osName),
		Kernel:               mockKernel(osName),
		Arch:                 "x86_64",
		Virtualization:       mockVirtualization(osName),
		CPUModel:             mockCPUModel(osName, cores),
		CountryCode:          countryCode,
		CPUCores:             ptr(cores),
		ExpiryLabel:          expiry,
		RenewalAmount:        ptr(5 + cores*2.5),
		RenewalCurrency:      "USD",
		BillingCycle:         "月",
		CPUPercent:           ptr(cpuPercent),
		Load1:                ptr(cpuPercent * cores / 100),
		Load5:                ptr(cpuPercent * cores / 125),
		Load15:               ptr(cpuPercent * cores / 150),
		MemoryUsedBytes:      used(memoryTotal, memoryPercent),
		MemoryTotalBytes:     memoryTotal,
		DiskUsedBytes:        used(diskTotal, diskPercent),
		DiskTotalBytes:       diskTotal,
		NetInSpeedBps:        netInSpeed,
		NetOutSpeedBps:       netOutSpeed,
		NetInTotalBytes:      monthlyBillable,
		NetOutTotalBytes:     monthlyBillable,
		BillingMode:          "both",
		MonthlyResetDay:      1,
		MonthlyPeriodStart:   "2026-07-01",
		MonthlyPeriodEnd:     "2026-07-31",
		MonthlyBillableBytes: monthlyBillable,
		MonthlyQuotaBytes:    monthlyQuota,
		LatencySummary:       latencySummary,
	}
}

func mockNodes() []Node {
	return []Node{
		mockNode("mechrevo", "Mechrevo", "windows", "HK", 16, "永 久", 19.73, gb(23.29), 58.73, tb(1.37), 30.62, kb(104.32), tb(10), kb(27.22), kb(967.98), nil, nil),
		mockNode("example-harbor", "Example Harbor", "debian", "HK", 2, "余 7 天", 6.72, gb(1.89), 28.08, gb(29.36), 5.92, gb(212.90), gb(512), kb(7.00), kb(10.75), ptr(55.9), ptr(0.18)),
		mockNode("example-transit", "Example Transit", "debian", "HK", 2, "余 1755 天", 5.04, mb(431.25), 47.63, gb(4.84), 34.00, gb(2.75), gb(200), kb(4.63), kb(7.98), ptr(35.1), ptr(0)),
		mockNode("example-node-a", "Example Node A", "debian", "HK", 2, "永 久", 61.43, gb(3.83), 68.52, gb(97.87), 18.21, mb(1.50), tb(10), kb(419.02), kb(20.00), ptr(34.6), ptr(0.40)),
		mockNode("example-edge-hk", "Example Edge HK", "debian", "HK", 1, "余 30 天", 3.02, mb(464.69), 44.13, gb(9.74), 15.31, kb(19.80), gb(1000), kb(3.87), kb(4.01), ptr(31.6), ptr(0)),
		mockNode("example-relay", "Example Relay", "debian", "JP", 1, "余 15 天", 7.88, mb(967.94), 42.87, gb(19.52), 8.03, kb(14.18), tb(2), kb(4.50), kb(8.41), ptr(108), ptr(1.22)),
		mockNode("example-edge-jp", "Example Edge JP", "debian", "JP", 1, "余 30 天", 0.49, mb(464.69), 49.53, gb(9.74), 16.12, kb(4.92), gb(1000), kb(0.40), kb(11.96), ptr(86.1), ptr(0.66)),
		mockNode("example-edge-tw", "Example Edge TW", "debian", "CN", 1, "余 30 天", 6.97, mb(464.69), 45.27, gb(9.74), 16.07, kb(4.48), gb(1000), kb(1.13), kb(2.11), ptr(62.7), ptr(0.16)),
		mockNode("example-compute", "Example Compute", "debian", "US", 4, "余 1017 天", 50.31, gb(5.79), 76.18, gb(97.87), 53.88, gb(28.34), tb(25), kb(281.90), kb(512.42), ptr(172), ptr(0.04)),
		mockNode("example-node-b", "Example Node B", "debian", "US", 1, "余 91 天", 4.85, mb(967.94), 39.16, gb(19.52), 5.31, kb(68.65), tb(4), kb(22.88), kb(25.78), ptr(202), ptr(1.48)),
		mockNode("example-host", "Example Host", "debian", "DE", 1, "余 331 天", 1.38, gb(1.84), 23.82, gb(19.52), 7.52, kb(2.63), tb(1), kb(1.31), kb(1.69), ptr(156), ptr(0.09)),
	}
}

func mockLatencyPoints(nodeID string, rangeNames ...string) []LatencyPoint {
	window, ok := resolveLatencyWindow("")
	if len(rangeNames) > 0 {
		if gridWindow, gridOK := resolveLatencyGridWindow(rangeNames[0]); gridOK {
			window, ok = gridWindow, true
		} else {
			window, ok = resolveLatencyWindow(rangeNames[0])
		}
	}
	if !ok {
		window, _ = resolveLatencyWindow("")
	}
	end := time.Date(2026, 7, 2, 13, 30, 0, 0, time.UTC)
	targets := []struct {
		id       string
		name     string
		baseMS   float64
		jitterMS float64
		loss     float64
	}{
		{id: "cq-unicom", name: "重庆联通", baseMS: 34.8, jitterMS: 2.4, loss: 0.18},
		{id: "cq-mobile", name: "重庆移动", baseMS: 34.3, jitterMS: 2.1, loss: 0.19},
		{id: "cq-telecom", name: "重庆电信", baseMS: 43.5, jitterMS: 3.2, loss: 0.18},
		{id: "telegram-dc5", name: "DC5", baseMS: 31.8, jitterMS: 2.6, loss: 0.38},
		{id: "google", name: "Google", baseMS: 1.4, jitterMS: 0.4, loss: 0},
		{id: "telegram-dc2", name: "DC2", baseMS: 193.4, jitterMS: 9.2, loss: 0.12},
		{id: "telegram-dc1", name: "DC1", baseMS: 198.2, jitterMS: 10.5, loss: 1.71},
		{id: "akari-tw", name: "Akari TW", baseMS: 14.4, jitterMS: 1.4, loss: 0},
		{id: "akari-jp", name: "Akari JP", baseMS: 50.3, jitterMS: 4.1, loss: 1.02},
		{id: "akari-hk", name: "Akari HK", baseMS: 1.7, jitterMS: 0.5, loss: 1.22},
		{id: "example-node-a", name: "Example Node A", baseMS: 1.8, jitterMS: 0.5, loss: 1.29},
		{id: "example-compute", name: "Example Compute", baseMS: 152.2, jitterMS: 8.4, loss: 0.19},
		{id: "example-node-b", name: "Example Node B", baseMS: 144.8, jitterMS: 7.5, loss: 0.16},
	}

	points := make([]LatencyPoint, 0, window.Samples*len(targets))
	spikeIndex := window.Samples / 2
	partialLossIndex := window.Samples / 3
	fullLossIndex := window.Samples * 2 / 3
	for index := 0; index < window.Samples; index++ {
		ts := end.Add(-time.Duration(window.Samples-1-index) * window.Step).Format(time.RFC3339)
		for targetIndex, target := range targets {
			wave := float64((index+targetIndex)%6) / 5
			median := target.baseMS + wave*target.jitterMS
			if index == spikeIndex && (target.id == "cq-telecom" || target.id == "telegram-dc5") {
				median += 110
			}

			loss := target.loss
			var medianPtr *float64
			if target.id == "telegram-dc1" && index == fullLossIndex {
				loss = 100
			} else {
				if target.id == "telegram-dc1" && index == partialLossIndex {
					loss = 30
				}
				medianPtr = ptr(median)
			}

			points = append(points, LatencyPoint{TS: ts, TargetID: target.id, TargetName: target.name, MedianMS: medianPtr, AvgMS: medianPtr, LossPercent: loss})
		}
	}
	return points
}

func mockServiceTargets() []ServiceTarget {
	return []ServiceTarget{
		{ID: "cq-unicom", Name: "重庆联通", Type: "tcping", AssignedNodeCount: 11, ReportingNodeCount: 11, MedianMS: ptr(34.8), LossPercent: ptr(0.18), UpdatedAt: "2026-07-02T13:30:00Z"},
		{ID: "telegram-dc5", Name: "DC5", Type: "tcping", AssignedNodeCount: 11, ReportingNodeCount: 10, MedianMS: ptr(31.8), LossPercent: ptr(0.38), UpdatedAt: "2026-07-02T13:30:00Z"},
		{ID: "google", Name: "Google", Type: "http_get", AssignedNodeCount: 11, ReportingNodeCount: 11, MedianMS: ptr(1.4), LossPercent: ptr(0), UpdatedAt: "2026-07-02T13:30:00Z"},
	}
}

func mockStatePoints(window latencyWindow) []StatePoint {
	end := time.Date(2026, 7, 2, 13, 30, 0, 0, time.UTC)
	points := make([]StatePoint, 0, window.Samples)
	for index := 0; index < window.Samples; index++ {
		wave := float64(index%8) / 7
		points = append(points, StatePoint{
			TS:               end.Add(-time.Duration(window.Samples-1-index) * window.Step).Format(time.RFC3339),
			CPUPercent:       ptr(8 + wave*18),
			MemoryUsedBytes:  gb(2.1 + wave*0.4),
			MemoryTotalBytes: gb(3.83),
			DiskUsedBytes:    gb(18 + wave*2),
			DiskTotalBytes:   gb(97.87),
			NetInTotalBytes:  mb(1500 + float64(index)*5),
			NetOutTotalBytes: mb(900 + float64(index)*3),
			NetInSpeedBps:    kb(20 + wave*30),
			NetOutSpeedBps:   kb(10 + wave*20),
			UptimeSeconds:    ptr(86400 + float64(index)*float64(window.Step/time.Second)),
		})
	}
	return points
}
