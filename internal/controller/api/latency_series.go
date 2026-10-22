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
	shared := sharedSeriesCreatedAt(series, func(series LatencySeries) []int64 { return series.CreatedAt })
	if len(shared) > 0 {
		for index := range series {
			series[index].CreatedAt = nil
		}
	}
	return shared, series
}

func latencySeriesFromPoints(points []LatencyPoint) []LatencySeries {
	return groupLatencySeries(points, latencySeriesIdentity, appendLatencySeriesPoint)
}

func latencySeriesIdentity(point LatencyPoint) (string, LatencySeries) {
	return point.TargetID, LatencySeries{TargetID: point.TargetID, TargetName: point.TargetName}
}

func appendLatencySeriesPoint(series *LatencySeries, point LatencyPoint) {
	appendLatencySeriesValues(&series.CreatedAt, &series.MedianMS, &series.AvgMS, &series.LossPercent, point.TS, point.MedianMS, point.AvgMS, point.LossPercent)
}

func appendLatencySeriesValues(createdAt *[]int64, median, average *[]*float64, lossPercent *[]float64, timestamp string, medianMS, averageMS *float64, loss float64) {
	*createdAt = append(*createdAt, latencyTimestampMillis(timestamp))
	*median = append(*median, compactLatencyValue(medianMS))
	*average = append(*average, compactLatencyValue(averageMS))
	*lossPercent = append(*lossPercent, compactLatencyNumber(loss))
}

func groupLatencySeries[P, S any](points []P, newSeries func(P) (string, S), appendPoint func(*S, P)) []S {
	order := make([]string, 0)
	byID := make(map[string]*S)
	for _, point := range points {
		id, initial := newSeries(point)
		series := byID[id]
		if series == nil {
			series = &initial
			byID[id] = series
			order = append(order, id)
		}
		appendPoint(series, point)
	}
	seriesList := make([]S, 0, len(order))
	for _, id := range order {
		seriesList = append(seriesList, *byID[id])
	}
	return seriesList
}

func sharedSeriesCreatedAt[S any](seriesList []S, createdAt func(S) []int64) []int64 {
	if len(seriesList) == 0 || len(createdAt(seriesList[0])) == 0 {
		return nil
	}
	shared := createdAt(seriesList[0])
	for _, series := range seriesList[1:] {
		if !sameInt64Slice(shared, createdAt(series)) {
			return nil
		}
	}
	return append([]int64(nil), shared...)
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

func compactLatencyValue(value *float64) *float64 {
	if value == nil {
		return nil
	}
	compact := compactLatencyNumber(*value)
	return &compact
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
