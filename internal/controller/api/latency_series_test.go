package api

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestLatencySeriesBuilders(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Second)

	if got := latencySeriesFromPoints(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil node points produced %#v, want non-nil empty series", got)
	}

	nodePoints := []LatencyPoint{
		{TS: t0.Format(time.RFC3339), TargetID: "b", TargetName: "B", MedianMS: latencySeriesFloat(12), AvgMS: latencySeriesFloat(12.345), LossPercent: 1.234},
		{TS: t1.Format(time.RFC3339), TargetID: "a", TargetName: "A", AvgMS: latencySeriesFloat(20.005), LossPercent: 2.005},
		{TS: t1.Format(time.RFC3339), TargetID: "b", TargetName: "renamed", AvgMS: nil, LossPercent: 3.999},
	}
	wantNode := []LatencySeries{
		{TargetID: "b", TargetName: "B", CreatedAt: []int64{t0.UnixMilli(), t1.UnixMilli()}, MedianMS: []*float64{latencySeriesFloat(12), nil}, AvgMS: []*float64{latencySeriesFloat(12.35), nil}, LossPercent: []float64{1.23, 4}},
		{TargetID: "a", TargetName: "A", CreatedAt: []int64{t1.UnixMilli()}, MedianMS: []*float64{nil}, AvgMS: []*float64{latencySeriesFloat(20.01)}, LossPercent: []float64{2.01}},
	}
	if got := latencySeriesFromPoints(nodePoints); !reflect.DeepEqual(got, wantNode) {
		t.Fatalf("node series = %#v, want %#v", got, wantNode)
	}

	odd := latencySeriesFromPoints([]LatencyPoint{{TS: "invalid", TargetID: "odd", AvgMS: latencySeriesFloat(math.NaN()), LossPercent: math.Inf(1)}})
	if len(odd) != 1 || len(odd[0].CreatedAt) != 1 || odd[0].CreatedAt[0] != 0 || odd[0].AvgMS[0] == nil || !math.IsNaN(*odd[0].AvgMS[0]) || !math.IsInf(odd[0].LossPercent[0], 1) {
		t.Fatalf("non-finite/invalid timestamp series = %#v", odd)
	}
}

func TestLatencySeriesJSONGolden(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Second)

	node := LatencyResponse{NodeID: "node", Range: "1h", Points: []LatencyPoint{
		{TS: t0.Format(time.RFC3339), TargetID: "a", TargetName: "A", MedianMS: latencySeriesFloat(1.111), AvgMS: latencySeriesFloat(1.234), LossPercent: 2.345},
		{TS: t0.Format(time.RFC3339), TargetID: "b", TargetName: "B", AvgMS: nil, LossPercent: 3.456},
		{TS: t1.Format(time.RFC3339), TargetID: "a", TargetName: "A", MedianMS: latencySeriesFloat(4.444), AvgMS: latencySeriesFloat(4.567), LossPercent: 5.678},
		{TS: t1.Format(time.RFC3339), TargetID: "b", TargetName: "B", MedianMS: latencySeriesFloat(6.666), AvgMS: latencySeriesFloat(6.789), LossPercent: 7.891},
	}}
	got, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"node_id":"node","range":"1h","created_at":[1785542400000,1785542401000],"series":[{"target_id":"a","target_name":"A","median_ms":[1.11,4.44],"avg_ms":[1.23,4.57],"loss_percent":[2.35,5.68]},{"target_id":"b","target_name":"B","median_ms":[null,6.67],"avg_ms":[null,6.79],"loss_percent":[3.46,7.89]}]}`
	if string(got) != want {
		t.Fatalf("node JSON = %s, want %s", got, want)
	}

	for name, value := range map[string]any{
		"node": LatencyResponse{Points: []LatencyPoint{{TargetID: "odd", AvgMS: latencySeriesFloat(math.Inf(1))}}},
	} {
		t.Run(name+" rejects non-finite values", func(t *testing.T) {
			if _, err := json.Marshal(value); err == nil {
				t.Fatal("marshal unexpectedly accepted non-finite value")
			}
		})
	}
}

func TestLatencySeriesUnmarshalShapesAndFallbacks(t *testing.T) {
	t.Parallel()

	original := LatencyResponse{NodeID: "keep", Range: "keep", Points: []LatencyPoint{{TargetID: "keep"}}}
	gotNode := original
	if err := json.Unmarshal([]byte(`{"node_id":`), &gotNode); err == nil || !reflect.DeepEqual(gotNode, original) {
		t.Fatalf("invalid node JSON changed receiver: got=%#v err=%v", gotNode, err)
	}
	if err := json.Unmarshal([]byte(`{"node_id":"node","range":"1h"}`), &gotNode); err != nil || gotNode.Points != nil {
		t.Fatalf("omitted node series = %#v err=%v, want nil points", gotNode, err)
	}
	if err := json.Unmarshal([]byte(`{"node_id":"node","range":"1h","series":[]}`), &gotNode); err != nil || gotNode.Points == nil || len(gotNode.Points) != 0 {
		t.Fatalf("empty node series = %#v err=%v, want non-nil empty points", gotNode, err)
	}

	legacyNode := `{"node_id":"node","range":"1h","points":[{"ts":"2026-08-01T00:00:00Z","target_id":"legacy","target_name":"Legacy","avg_ms":1.25,"loss_percent":2.5}],"series":[{"target_id":"ignored","created_at":[1]}]}`
	if err := json.Unmarshal([]byte(legacyNode), &gotNode); err != nil || len(gotNode.Points) != 1 || gotNode.Points[0].TargetID != "legacy" {
		t.Fatalf("legacy node points did not win: %#v err=%v", gotNode, err)
	}

	sharedNode := `{"node_id":"node","range":"7d","created_at":[0,-1,1000],"series":[{"target_id":"shared","target_name":"Shared","median_ms":[null,2.5],"avg_ms":[1.5],"loss_percent":[3]},{"target_id":"own","target_name":"Own","created_at":[2000],"median_ms":[4.5],"avg_ms":[5.5],"loss_percent":[6.5]}]}`
	if err := json.Unmarshal([]byte(sharedNode), &gotNode); err != nil {
		t.Fatal(err)
	}
	wantNode := []LatencyPoint{
		{TS: "1970-01-01T00:00:00Z", TargetID: "shared", TargetName: "Shared", AvgMS: latencySeriesFloat(1.5), LossPercent: 3},
		{TS: "1970-01-01T00:00:00Z", TargetID: "shared", TargetName: "Shared", MedianMS: latencySeriesFloat(2.5)},
		{TS: "1970-01-01T00:00:01Z", TargetID: "shared", TargetName: "Shared"},
		{TS: "1970-01-01T00:00:02Z", TargetID: "own", TargetName: "Own", MedianMS: latencySeriesFloat(4.5), AvgMS: latencySeriesFloat(5.5), LossPercent: 6.5},
	}
	if !reflect.DeepEqual(gotNode.Points, wantNode) {
		t.Fatalf("decoded node points = %#v, want %#v", gotNode.Points, wantNode)
	}

}

func latencySeriesFloat(value float64) *float64 { return &value }
