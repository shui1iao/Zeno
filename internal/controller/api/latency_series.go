package api

import (
	"encoding/json"
	"math"
	"time"
)

type LatencySeries struct {
	TargetID    string     `json:"target_id"`
	TargetName  string     `json:"target_name"`
	CreatedAt   []int64    `json:"created_at,omitempty"`
	MedianMS    []*float64 `json:"median_ms,omitempty"`
	AvgMS       []*float64 `json:"avg_ms,omitempty"`
	LossPercent []float64  `json:"loss_percent,omitempty"`
}

type latencyResponseDecode struct {
	NodeID          string          `json:"node_id"`
	Range           string          `json:"range"`
	SharedCreatedAt []int64         `json:"created_at"`
	Points          []LatencyPoint  `json:"points"`
	Series          []LatencySeries `json:"series"`
}

func (response LatencyResponse) MarshalJSON() ([]byte, error) {
	type latencyResponseJSON struct {
		NodeID          string          `json:"node_id"`
		Range           string          `json:"range"`
		SharedCreatedAt []int64         `json:"created_at,omitempty"`
		Series          []LatencySeries `json:"series"`
	}
	createdAt, series := latencySeriesPayloadFromPoints(response.Points)
	return json.Marshal(latencyResponseJSON{
		NodeID:          response.NodeID,
		Range:           response.Range,
		SharedCreatedAt: createdAt,
		Series:          series,
	})
}

func (response *LatencyResponse) UnmarshalJSON(data []byte) error {
	return decodeLatencyResponse(data, func(raw latencyResponseDecode) {
		response.NodeID = raw.NodeID
		response.Range = raw.Range
		response.Points = latencyPointsFromPayload(
			raw.Points, raw.Series, raw.SharedCreatedAt,
			func(series LatencySeries) []int64 { return series.CreatedAt }, latencyPointFromSeries,
		)
	})
}

func decodeLatencyResponse[R any](data []byte, assign func(R)) error {
	var raw R
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	assign(raw)
	return nil
}

func latencyPointsFromPayload[P, S any](
	points []P, seriesList []S, sharedCreatedAt []int64,
	createdAtFor func(S) []int64, pointAt func(S, int64, int) P,
) []P {
	if len(points) > 0 || seriesList == nil {
		return points
	}
	points = make([]P, 0)
	for _, series := range seriesList {
		createdAtValues := createdAtFor(series)
		if len(createdAtValues) == 0 {
			createdAtValues = sharedCreatedAt
		}
		for index, createdAt := range createdAtValues {
			points = append(points, pointAt(series, createdAt, index))
		}
	}
	return points
}

func latencyPointFromSeries(series LatencySeries, createdAt int64, index int) LatencyPoint {
	return LatencyPoint{TS: latencyTimestampString(createdAt), TargetID: series.TargetID, TargetName: series.TargetName,
		MedianMS: floatSliceValue(series.MedianMS, index), AvgMS: floatSliceValue(series.AvgMS, index), LossPercent: floatValue(series.LossPercent, index)}
}

func latencySeriesPayloadFromPoints(points []LatencyPoint) ([]int64, []LatencySeries) {
	series := latencySeriesFromPoints(points)
	if len(series) == 0 || len(series[0].CreatedAt) == 0 {
		return nil, series
	}
	shared := series[0].CreatedAt
	for index := 1; index < len(series); index++ {
		if !sameInt64Slice(shared, series[index].CreatedAt) {
			return nil, series
		}
	}
	for index := range series {
		series[index].CreatedAt = nil
	}
	return shared, series
}

type floatSlab struct {
	buf []float64
}

func (s *floatSlab) alloc(v float64) *float64 {
	if len(s.buf) == cap(s.buf) {
		size := 512
		if cap(s.buf) > 0 && cap(s.buf) < 8192 {
			size = cap(s.buf) * 2
		} else if cap(s.buf) >= 8192 {
			size = 8192
		}
		s.buf = make([]float64, 0, size)
	}
	idx := len(s.buf)
	s.buf = append(s.buf, v)
	return &s.buf[idx]
}

func latencySeriesFromPoints(points []LatencyPoint) []LatencySeries {
	n := len(points)
	if n == 0 {
		return []LatencySeries{}
	}
	indexByID := make(map[string]int, 16)
	seriesList := make([]LatencySeries, 0, 16)
	counts := make([]int, 0, 16)
	nonNilFloats := 0
	nextGuess := 0
	for i := range points {
		p := &points[i]
		var idx int
		if nextGuess < len(seriesList) && seriesList[nextGuess].TargetID == p.TargetID {
			idx = nextGuess
		} else {
			var exists bool
			idx, exists = indexByID[p.TargetID]
			if !exists {
				idx = len(seriesList)
				indexByID[p.TargetID] = idx
				seriesList = append(seriesList, LatencySeries{
					TargetID:   p.TargetID,
					TargetName: p.TargetName,
				})
				counts = append(counts, 0)
			}
		}
		counts[idx]++
		nextGuess = idx + 1
		if nextGuess == len(seriesList) {
			nextGuess = 0
		}
		if p.MedianMS != nil {
			nonNilFloats++
		}
		if p.AvgMS != nil {
			nonNilFloats++
		}
	}

	createdAtSlab := make([]int64, 0, n)
	medianSlab := make([]*float64, 0, n)
	avgSlab := make([]*float64, 0, n)
	lossSlab := make([]float64, 0, n)
	floatValues := floatSlab{buf: make([]float64, 0, nonNilFloats)}
	offset := 0
	for i, count := range counts {
		end := offset + count
		seriesList[i].CreatedAt = createdAtSlab[offset:offset:end]
		seriesList[i].MedianMS = medianSlab[offset:offset:end]
		seriesList[i].AvgMS = avgSlab[offset:offset:end]
		seriesList[i].LossPercent = lossSlab[offset:offset:end]
		offset = end
	}

	var lastTS string
	var lastMillis int64
	var hasLastTS bool
	nextGuess = 0
	for i := range points {
		p := &points[i]
		var idx int
		if nextGuess < len(seriesList) && seriesList[nextGuess].TargetID == p.TargetID {
			idx = nextGuess
		} else {
			idx = indexByID[p.TargetID]
		}
		nextGuess = idx + 1
		if nextGuess == len(seriesList) {
			nextGuess = 0
		}

		var tsMillis int64
		if p.TSUnix > 0 {
			tsMillis = p.TSUnix * 1000
		} else if hasLastTS && p.TS == lastTS {
			tsMillis = lastMillis
		} else {
			tsMillis = latencyTimestampMillis(p.TS)
			lastTS = p.TS
			lastMillis = tsMillis
			hasLastTS = true
		}

		s := &seriesList[idx]
		s.CreatedAt = append(s.CreatedAt, tsMillis)
		if p.MedianMS != nil {
			s.MedianMS = append(s.MedianMS, floatValues.alloc(compactLatencyNumber(*p.MedianMS)))
		} else {
			s.MedianMS = append(s.MedianMS, nil)
		}
		if p.AvgMS != nil {
			s.AvgMS = append(s.AvgMS, floatValues.alloc(compactLatencyNumber(*p.AvgMS)))
		} else {
			s.AvgMS = append(s.AvgMS, nil)
		}
		s.LossPercent = append(s.LossPercent, compactLatencyNumber(p.LossPercent))
	}
	return seriesList
}

func sameInt64Slice(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func compactLatencyNumber(value float64) float64 {
	return math.Round(value*100) / 100
}

func latencyTimestampMillis(value string) int64 {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0
	}
	return parsed.UTC().UnixMilli()
}

func latencyTimestampString(value int64) string {
	if value <= 0 {
		return time.Unix(0, 0).UTC().Format(time.RFC3339)
	}
	return time.UnixMilli(value).UTC().Format(time.RFC3339)
}

func floatSliceValue(values []*float64, index int) *float64 {
	if index < 0 || index >= len(values) {
		return nil
	}
	return values[index]
}

func floatValue(values []float64, index int) float64 {
	if index < 0 || index >= len(values) {
		return 0
	}
	return values[index]
}
