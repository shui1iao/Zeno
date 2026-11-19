package api

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"time"
)

func upsertLifetimeTraffic(ctx context.Context, tx *sql.Tx, nodeID string, inTotal, outTotal int64, counterSource string, sampleTS, now int64) error {
	var lifetimeIn, lifetimeOut int64
	var previousIn, previousOut, lastSampleTS sql.NullInt64
	var previousSource string
	err := tx.QueryRowContext(ctx, `
		SELECT in_bytes, out_bytes, last_in_total_bytes, last_out_total_bytes, counter_source, last_sample_ts
		FROM traffic_lifetime
		WHERE node_id = ?
	`, nodeID).Scan(&lifetimeIn, &lifetimeOut, &previousIn, &previousOut, &previousSource, &lastSampleTS)
	if err == sql.ErrNoRows {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO traffic_lifetime (
				node_id, in_bytes, out_bytes, last_in_total_bytes,
				last_out_total_bytes, counter_source, last_sample_ts, updated_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, nodeID, inTotal, outTotal, inTotal, outTotal, counterSource, sampleTS, now)
		return err
	}
	if err != nil {
		return err
	}
	if lastSampleTS.Valid && sampleTS <= lastSampleTS.Int64 {
		return nil
	}
	if counterSource != "" && counterSource != previousSource {
		_, err = tx.ExecContext(ctx, `
			UPDATE traffic_lifetime
			SET last_in_total_bytes = ?, last_out_total_bytes = ?, counter_source = ?, last_sample_ts = ?, updated_at = ?
			WHERE node_id = ?
		`, inTotal, outTotal, counterSource, sampleTS, now, nodeID)
		return err
	}
	effectiveSource := counterSource
	if effectiveSource == "" && previousSource != "" {
		// An older Agent may temporarily report no source identity after a newer
		// Agent established one. Preserve the last known source while accounting
		// the monotonic interval; clearing it would make the next identified sample
		// look like a source transition and silently drop that interval.
		effectiveSource = previousSource
	}

	deltaIn := lifetimeTrafficDelta(previousIn, inTotal)
	deltaOut := lifetimeTrafficDelta(previousOut, outTotal)
	lifetimeIn = saturatingAddNonNegativeInt64(lifetimeIn, deltaIn)
	lifetimeOut = saturatingAddNonNegativeInt64(lifetimeOut, deltaOut)
	_, err = tx.ExecContext(ctx, `
		UPDATE traffic_lifetime
		SET in_bytes = ?,
		    out_bytes = ?,
		    last_in_total_bytes = ?,
		    last_out_total_bytes = ?,
		    counter_source = ?,
		    last_sample_ts = ?,
		    updated_at = ?
		WHERE node_id = ?
	`, lifetimeIn, lifetimeOut, inTotal, outTotal, effectiveSource, sampleTS, now, nodeID)
	return err
}

func lifetimeTrafficDelta(previous sql.NullInt64, current int64) int64 {
	if !previous.Valid {
		return 0
	}
	if current < previous.Int64 {
		return current
	}
	return current - previous.Int64
}

func saturatingAddNonNegativeInt64(value, delta int64) int64 {
	if value < 0 || delta < 0 || value >= math.MaxInt64-delta {
		return math.MaxInt64
	}
	return value + delta
}

func upsertMonthlyTraffic(ctx context.Context, tx *sql.Tx, nodeID, month string, billingEpoch int64, resetDay int, billingMode string, inTotal, outTotal int64, counterSource string, sampleTS, now int64) error {
	var aggregateIn, aggregateOut, aggregateBillable int64
	var previousIn, previousOut, lastSampleTS sql.NullInt64
	var previousSource string
	err := tx.QueryRowContext(ctx, `
		SELECT in_bytes, out_bytes, billable_bytes,
		       last_in_total_bytes, last_out_total_bytes, counter_source, last_sample_ts
		FROM traffic_monthly
		WHERE node_id = ? AND month = ? AND billing_epoch = ?
	`, nodeID, month, billingEpoch).Scan(&aggregateIn, &aggregateOut, &aggregateBillable, &previousIn, &previousOut, &previousSource, &lastSampleTS)
	if err == sql.ErrNoRows {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO traffic_monthly (node_id, month, billing_epoch, reset_day, billing_mode, in_bytes, out_bytes, billable_bytes, last_in_total_bytes, last_out_total_bytes, counter_source, last_sample_ts, updated_at)
			VALUES (?, ?, ?, ?, ?, 0, 0, 0, ?, ?, ?, ?, ?)
		`, nodeID, month, billingEpoch, normalizeBillingResetDay(resetDay), normalizeTrafficBillingMode(billingMode), inTotal, outTotal, counterSource, sampleTS, now)
		return err
	}
	if err != nil {
		return err
	}
	if lastSampleTS.Valid && sampleTS <= lastSampleTS.Int64 {
		return nil
	}
	if counterSource != "" && counterSource != previousSource {
		_, err = tx.ExecContext(ctx, `
			UPDATE traffic_monthly
			SET last_in_total_bytes = ?, last_out_total_bytes = ?, counter_source = ?, last_sample_ts = ?, updated_at = ?
			WHERE node_id = ? AND month = ? AND billing_epoch = ?
		`, inTotal, outTotal, counterSource, sampleTS, now, nodeID, month, billingEpoch)
		return err
	}
	effectiveSource := counterSource
	if effectiveSource == "" && previousSource != "" {
		effectiveSource = previousSource
	}

	deltaIn := nonNegativeDelta(previousIn, inTotal)
	deltaOut := nonNegativeDelta(previousOut, outTotal)
	aggregateIn = saturatingAddNonNegativeInt64(aggregateIn, deltaIn)
	aggregateOut = saturatingAddNonNegativeInt64(aggregateOut, deltaOut)
	aggregateBillable = billableTrafficAggregate(billingMode, aggregateIn, aggregateOut)
	_, err = tx.ExecContext(ctx, `
		UPDATE traffic_monthly
		SET in_bytes = ?,
		    out_bytes = ?,
		    billable_bytes = ?,
		    last_in_total_bytes = ?,
		    last_out_total_bytes = ?,
		    counter_source = ?,
		    last_sample_ts = ?,
		    updated_at = ?
		WHERE node_id = ? AND month = ? AND billing_epoch = ?
	`, aggregateIn, aggregateOut, aggregateBillable, inTotal, outTotal, effectiveSource, sampleTS, now, nodeID, month, billingEpoch)
	return err
}

func normalizeBillingResetDay(resetDay int) int {
	if resetDay < 1 || resetDay > 31 {
		return 1
	}
	return resetDay
}

func normalizeTrafficBillingMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "in", "download", "inbound":
		return "in"
	case "out", "upload", "outbound":
		return "out"
	case "max", "higher":
		return "max"
	default:
		return "both"
	}
}

func nonNegativeDelta(previous sql.NullInt64, current int64) int64 {
	if !previous.Valid || current < previous.Int64 {
		return 0
	}
	return current - previous.Int64
}

func billableTrafficDelta(mode string, deltaIn, deltaOut int64) int64 {
	return billableTrafficAggregate(mode, deltaIn, deltaOut)
}

func billableTrafficAggregate(mode string, totalIn, totalOut int64) int64 {
	switch normalizeTrafficBillingMode(mode) {
	case "in":
		return totalIn
	case "out":
		return totalOut
	case "max":
		if totalIn > totalOut {
			return totalIn
		}
		return totalOut
	default:
		return saturatingAddNonNegativeInt64(totalIn, totalOut)
	}
}

func billingPeriodKey(ts time.Time, resetDay int) string {
	return billingPeriodFor(ts, resetDay).Key
}

type billingPeriod struct {
	Key       string
	StartDate string
	EndDate   string
}

func billingPeriodFor(ts time.Time, resetDay int) billingPeriod {
	resetDay = normalizeBillingResetDay(resetDay)
	loc := effectiveTimeLocation(ts)
	now := ts.In(loc)
	currentReset := resetDateInLocation(now.Year(), now.Month(), resetDay, loc)
	start := currentReset
	if now.Before(currentReset) {
		previousYear, previousMonth := monthOffset(now.Year(), now.Month(), -1)
		start = resetDateInLocation(previousYear, previousMonth, resetDay, loc)
	}
	nextYear, nextMonth := monthOffset(start.Year(), start.Month(), 1)
	nextReset := resetDateInLocation(nextYear, nextMonth, resetDay, loc)
	return billingPeriod{
		Key:       start.Format("2006-01"),
		StartDate: start.Format("2006-01-02"),
		EndDate:   nextReset.AddDate(0, 0, -1).Format("2006-01-02"),
	}
}

func resetDateInLocation(year int, month time.Month, resetDay int, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	day := resetDay
	if maxDay := daysInMonth(year, month); day > maxDay {
		day = maxDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, loc)
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func monthOffset(year int, month time.Month, offset int) (int, time.Month) {
	shifted := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC).AddDate(0, offset, 0)
	return shifted.Year(), shifted.Month()
}

func nullableUnix(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}
