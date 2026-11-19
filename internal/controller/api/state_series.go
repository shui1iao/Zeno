package api

import (
	"encoding/json"
	"time"
)

type StateSeries struct {
	CPUPercent         []*float64 `json:"cpu_percent,omitempty"`
	Load1              []*float64 `json:"load1,omitempty"`
	Load5              []*float64 `json:"load5,omitempty"`
	Load15             []*float64 `json:"load15,omitempty"`
	MemoryUsedBytes    []*float64 `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes   []*float64 `json:"memory_total_bytes,omitempty"`
	SwapUsedBytes      []*float64 `json:"swap_used_bytes,omitempty"`
	SwapTotalBytes     []*float64 `json:"swap_total_bytes,omitempty"`
	DiskUsedBytes      []*float64 `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes     []*float64 `json:"disk_total_bytes,omitempty"`
	NetInTotalBytes    []*float64 `json:"net_in_total_bytes,omitempty"`
	NetOutTotalBytes   []*float64 `json:"net_out_total_bytes,omitempty"`
	NetInSpeedBps      []*float64 `json:"net_in_speed_bps,omitempty"`
	NetOutSpeedBps     []*float64 `json:"net_out_speed_bps,omitempty"`
	ProcessCount       []*float64 `json:"process_count,omitempty"`
	TCPConnectionCount []*float64 `json:"tcp_connection_count,omitempty"`
	UDPConnectionCount []*float64 `json:"udp_connection_count,omitempty"`
	UptimeSeconds      []*float64 `json:"uptime_seconds,omitempty"`
}

func (response StateResponse) MarshalJSON() ([]byte, error) {
	type stateResponseJSON struct {
		NodeID    string       `json:"node_id"`
		Range     string       `json:"range"`
		CreatedAt []int64      `json:"created_at,omitempty"`
		Series    *StateSeries `json:"series,omitempty"`
	}
	createdAt, series := stateSeriesPayloadFromPoints(response.Points)
	return json.Marshal(stateResponseJSON{
		NodeID:    response.NodeID,
		Range:     response.Range,
		CreatedAt: createdAt,
		Series:    series,
	})
}

func (response *StateResponse) UnmarshalJSON(data []byte) error {
	type stateResponseJSON struct {
		NodeID    string       `json:"node_id"`
		Range     string       `json:"range"`
		Points    []StatePoint `json:"points"`
		CreatedAt []int64      `json:"created_at"`
		Series    *StateSeries `json:"series"`
	}
	var raw stateResponseJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	response.NodeID = raw.NodeID
	response.Range = raw.Range
	if raw.Points != nil {
		response.Points = raw.Points
		return nil
	}
	response.Points = statePointsFromSeries(raw.CreatedAt, raw.Series)
	return nil
}

func stateSeriesPayloadFromPoints(points []StatePoint) ([]int64, *StateSeries) {
	n := len(points)
	if n == 0 {
		return nil, nil
	}
	createdAt := make([]int64, n)
	ptrSlab := make([]*float64, 18*n)
	series := &StateSeries{
		CPUPercent:         ptrSlab[0*n : 1*n : 1*n],
		Load1:              ptrSlab[1*n : 2*n : 2*n],
		Load5:              ptrSlab[2*n : 3*n : 3*n],
		Load15:             ptrSlab[3*n : 4*n : 4*n],
		MemoryUsedBytes:    ptrSlab[4*n : 5*n : 5*n],
		MemoryTotalBytes:   ptrSlab[5*n : 6*n : 6*n],
		SwapUsedBytes:      ptrSlab[6*n : 7*n : 7*n],
		SwapTotalBytes:     ptrSlab[7*n : 8*n : 8*n],
		DiskUsedBytes:      ptrSlab[8*n : 9*n : 9*n],
		DiskTotalBytes:     ptrSlab[9*n : 10*n : 10*n],
		NetInTotalBytes:    ptrSlab[10*n : 11*n : 11*n],
		NetOutTotalBytes:   ptrSlab[11*n : 12*n : 12*n],
		NetInSpeedBps:      ptrSlab[12*n : 13*n : 13*n],
		NetOutSpeedBps:     ptrSlab[13*n : 14*n : 14*n],
		ProcessCount:       ptrSlab[14*n : 15*n : 15*n],
		TCPConnectionCount: ptrSlab[15*n : 16*n : 16*n],
		UDPConnectionCount: ptrSlab[16*n : 17*n : 17*n],
		UptimeSeconds:      ptrSlab[17*n : 18*n : 18*n],
	}
	floatValues := floatSlab{buf: make([]float64, 0, 18*n)}
	var lastTS string
	var lastMillis int64
	var hasLastTS bool
	for i := range points {
		point := &points[i]
		if point.TSUnix > 0 {
			createdAt[i] = point.TSUnix * 1000
		} else if hasLastTS && point.TS == lastTS {
			createdAt[i] = lastMillis
		} else {
			lastMillis = latencyTimestampMillis(point.TS)
			lastTS = point.TS
			hasLastTS = true
			createdAt[i] = lastMillis
		}
		if point.CPUPercent != nil {
			series.CPUPercent[i] = floatValues.alloc(compactLatencyNumber(*point.CPUPercent))
		}
		if point.Load1 != nil {
			series.Load1[i] = floatValues.alloc(compactLatencyNumber(*point.Load1))
		}
		if point.Load5 != nil {
			series.Load5[i] = floatValues.alloc(compactLatencyNumber(*point.Load5))
		}
		if point.Load15 != nil {
			series.Load15[i] = floatValues.alloc(compactLatencyNumber(*point.Load15))
		}
		if point.MemoryUsedBytes != nil {
			series.MemoryUsedBytes[i] = floatValues.alloc(compactLatencyNumber(*point.MemoryUsedBytes))
		}
		if point.MemoryTotalBytes != nil {
			series.MemoryTotalBytes[i] = floatValues.alloc(compactLatencyNumber(*point.MemoryTotalBytes))
		}
		if point.SwapUsedBytes != nil {
			series.SwapUsedBytes[i] = floatValues.alloc(compactLatencyNumber(*point.SwapUsedBytes))
		}
		if point.SwapTotalBytes != nil {
			series.SwapTotalBytes[i] = floatValues.alloc(compactLatencyNumber(*point.SwapTotalBytes))
		}
		if point.DiskUsedBytes != nil {
			series.DiskUsedBytes[i] = floatValues.alloc(compactLatencyNumber(*point.DiskUsedBytes))
		}
		if point.DiskTotalBytes != nil {
			series.DiskTotalBytes[i] = floatValues.alloc(compactLatencyNumber(*point.DiskTotalBytes))
		}
		if point.NetInTotalBytes != nil {
			series.NetInTotalBytes[i] = floatValues.alloc(compactLatencyNumber(*point.NetInTotalBytes))
		}
		if point.NetOutTotalBytes != nil {
			series.NetOutTotalBytes[i] = floatValues.alloc(compactLatencyNumber(*point.NetOutTotalBytes))
		}
		if point.NetInSpeedBps != nil {
			series.NetInSpeedBps[i] = floatValues.alloc(compactLatencyNumber(*point.NetInSpeedBps))
		}
		if point.NetOutSpeedBps != nil {
			series.NetOutSpeedBps[i] = floatValues.alloc(compactLatencyNumber(*point.NetOutSpeedBps))
		}
		if point.ProcessCount != nil {
			series.ProcessCount[i] = floatValues.alloc(compactLatencyNumber(*point.ProcessCount))
		}
		if point.TCPConnectionCount != nil {
			series.TCPConnectionCount[i] = floatValues.alloc(compactLatencyNumber(*point.TCPConnectionCount))
		}
		if point.UDPConnectionCount != nil {
			series.UDPConnectionCount[i] = floatValues.alloc(compactLatencyNumber(*point.UDPConnectionCount))
		}
		if point.UptimeSeconds != nil {
			series.UptimeSeconds[i] = floatValues.alloc(compactLatencyNumber(*point.UptimeSeconds))
		}
	}
	return createdAt, series
}

func statePointsFromSeries(createdAt []int64, series *StateSeries) []StatePoint {
	if series == nil || len(createdAt) == 0 {
		return nil
	}
	points := make([]StatePoint, 0, len(createdAt))
	for index, timestamp := range createdAt {
		points = append(points, StatePoint{
			TS:                 time.UnixMilli(timestamp).UTC().Format(time.RFC3339),
			CPUPercent:         stateSeriesValue(series.CPUPercent, index),
			Load1:              stateSeriesValue(series.Load1, index),
			Load5:              stateSeriesValue(series.Load5, index),
			Load15:             stateSeriesValue(series.Load15, index),
			MemoryUsedBytes:    stateSeriesValue(series.MemoryUsedBytes, index),
			MemoryTotalBytes:   stateSeriesValue(series.MemoryTotalBytes, index),
			SwapUsedBytes:      stateSeriesValue(series.SwapUsedBytes, index),
			SwapTotalBytes:     stateSeriesValue(series.SwapTotalBytes, index),
			DiskUsedBytes:      stateSeriesValue(series.DiskUsedBytes, index),
			DiskTotalBytes:     stateSeriesValue(series.DiskTotalBytes, index),
			NetInTotalBytes:    stateSeriesValue(series.NetInTotalBytes, index),
			NetOutTotalBytes:   stateSeriesValue(series.NetOutTotalBytes, index),
			NetInSpeedBps:      stateSeriesValue(series.NetInSpeedBps, index),
			NetOutSpeedBps:     stateSeriesValue(series.NetOutSpeedBps, index),
			ProcessCount:       stateSeriesValue(series.ProcessCount, index),
			TCPConnectionCount: stateSeriesValue(series.TCPConnectionCount, index),
			UDPConnectionCount: stateSeriesValue(series.UDPConnectionCount, index),
			UptimeSeconds:      stateSeriesValue(series.UptimeSeconds, index),
		})
	}
	return points
}

func stateSeriesValue(values []*float64, index int) *float64 {
	if index < 0 || index >= len(values) {
		return nil
	}
	return values[index]
}
