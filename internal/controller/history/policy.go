// Package history owns the tiered retention policy for probe and state
// history: how long raw samples live, the bucket width of each rollup tier,
// how much work one maintenance slice may do, and the SQL that moves rows
// between tiers.
//
// Keeping the policy and its generated SQL in one package makes the storage
// contract reviewable on its own. The controller supplies the database
// transaction; this package never opens one.
package history

import "time"

const (
	// RawRetention is how long raw high-frequency samples are kept once the
	// rollup tiers are active. Latency keeps exact one-minute weighted
	// aggregates and state keeps exact thirty-second weighted aggregates,
	// both finer than the public 7d/30d chart grids.
	RawRetention = 26 * time.Hour

	// RollupRetention bounds the aggregated tiers.
	RollupRetention = 30 * 24 * time.Hour

	// BatchSize is the row budget of a single delete or compaction batch.
	BatchSize = 1000

	// BatchPause yields the SQLite writer between batches.
	BatchPause = 10 * time.Millisecond

	// MaxBatchCycles bounds each hourly maintenance slice so a first
	// deployment can progressively compact a large database without
	// monopolizing SQLite's single writer. Leftover work resumes next hour.
	//
	// The budget must exceed one hour of ingest or the backlog grows forever:
	// a deployment writing ~13k state rows and ~9k probe rounds per hour needs
	// more than 22 cycles just to break even. 300 cycles drain roughly 300k
	// rows per table per hour, clearing a multi-million row backlog in days
	// instead of weeks, while BatchPause keeps the writer responsive.
	MaxBatchCycles = 300

	// ScheduleOffset staggers retention away from other hourly jobs that
	// would otherwise contend for the writer at the same instant.
	ScheduleOffset = 5 * time.Minute

	// VacuumPagesPerPass bounds how many freed pages one maintenance pass
	// returns to the filesystem. At the 4KiB default page size this releases
	// up to 80MiB per hour, enough to keep pace with retention without
	// holding SQLite's single writer for long.
	VacuumPagesPerPass = 20000

	// LatencyRollupStep is the latency rollup bucket width.
	LatencyRollupStep = time.Minute

	// StateRollupStep is the state rollup bucket width.
	StateRollupStep = 30 * time.Second
)

// FirstDelay staggers the first run of an hourly-or-slower retention loop.
// Sub-hourly intervals (tests, tuned deployments) keep their exact cadence.
func FirstDelay(interval time.Duration) time.Duration {
	if interval >= time.Hour {
		return interval + ScheduleOffset
	}
	return interval
}

// BucketFloor snaps a unix timestamp down to the start of its bucket, so
// rollup deletion never removes a bucket that is still accumulating.
func BucketFloor(unixSeconds int64, step time.Duration) int64 {
	stepSeconds := int64(step / time.Second)
	if stepSeconds <= 0 {
		return unixSeconds
	}
	return (unixSeconds / stepSeconds) * stepSeconds
}

// StepSeconds expresses a bucket width in whole seconds for SQL parameters.
func StepSeconds(step time.Duration) int64 {
	return int64(step / time.Second)
}

// Cutoffs are the timestamps one maintenance pass prunes against.
type Cutoffs struct {
	// Raw is the boundary for raw sample compaction.
	Raw int64
	// LegacyRaw is the boundary used during the rollback grace period, when
	// the previous release's complete 30-day raw view must stay intact.
	LegacyRaw int64
	// LatencyRollup and StateRollup bound the aggregated tiers.
	LatencyRollup int64
	StateRollup   int64
	// NotificationHistory bounds notification log rows.
	NotificationHistory int64
	// Now is the pass timestamp, reused for update stamps.
	Now int64
}

// CutoffsAt derives every retention boundary for a maintenance pass.
func CutoffsAt(now time.Time) Cutoffs {
	now = now.UTC()
	retention := now.Add(-RollupRetention).Unix()
	return Cutoffs{
		Raw:                 now.Add(-RawRetention).Unix(),
		LegacyRaw:           retention,
		LatencyRollup:       BucketFloor(retention, LatencyRollupStep),
		StateRollup:         BucketFloor(retention, StateRollupStep),
		NotificationHistory: retention,
		Now:                 now.Unix(),
	}
}
