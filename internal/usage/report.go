package usage

import (
	"errors"
	"math"
	"strings"
	"time"
)

type RangePreset string

const (
	RangeToday  RangePreset = "today"
	Range7Days  RangePreset = "7d"
	Range30Days RangePreset = "30d"
	RangeAll    RangePreset = "all"
)

type BucketUnit string

const (
	BucketHour  BucketUnit = "hour"
	BucketDay   BucketUnit = "day"
	BucketMonth BucketUnit = "month"
	BucketYear  BucketUnit = "year"
)

type TimeBucket struct {
	StartUnixMS int64
	EndUnixMS   int64
}

type ResolvedRange struct {
	Preset      RangePreset
	StartUnixMS *int64
	EndUnixMS   int64
	BucketUnit  BucketUnit
	TimeZone    string
	Buckets     []TimeBucket
}

func ParseRangePreset(value string) (RangePreset, error) {
	preset := RangePreset(strings.ToLower(strings.TrimSpace(value)))
	switch preset {
	case RangeToday, Range7Days, Range30Days, RangeAll:
		return preset, nil
	default:
		return "", errors.New("unsupported usage range")
	}
}

func ResolveRange(preset RangePreset, now time.Time, earliestDatedUnixMS int64) (ResolvedRange, error) {
	if _, err := ParseRangePreset(string(preset)); err != nil {
		return ResolvedRange{}, err
	}
	location := now.Location()
	if location == nil {
		location = time.Local
		now = now.In(location)
	}
	endUnixMS := now.UnixMilli()
	if endUnixMS < math.MaxInt64 {
		endUnixMS++
	}

	resolved := ResolvedRange{
		Preset:     preset,
		EndUnixMS:  endUnixMS,
		TimeZone:   location.String(),
		BucketUnit: BucketDay,
	}
	var bucketStart time.Time
	switch preset {
	case RangeToday:
		bucketStart = localDayStart(now)
		start := bucketStart.UnixMilli()
		resolved.StartUnixMS = &start
		resolved.BucketUnit = BucketHour
		resolved.Buckets = buildBuckets(bucketStart, endUnixMS, func(value time.Time) time.Time {
			return value.Add(time.Hour)
		})
	case Range7Days:
		bucketStart = localDayStart(now).AddDate(0, 0, -6)
		start := bucketStart.UnixMilli()
		resolved.StartUnixMS = &start
		resolved.Buckets = buildBuckets(bucketStart, endUnixMS, func(value time.Time) time.Time {
			return value.AddDate(0, 0, 1)
		})
	case Range30Days:
		bucketStart = localDayStart(now).AddDate(0, 0, -29)
		start := bucketStart.UnixMilli()
		resolved.StartUnixMS = &start
		resolved.Buckets = buildBuckets(bucketStart, endUnixMS, func(value time.Time) time.Time {
			return value.AddDate(0, 0, 1)
		})
	case RangeAll:
		earliest := now
		if earliestDatedUnixMS > 0 {
			earliest = time.UnixMilli(earliestDatedUnixMS).In(location)
			if earliest.After(now) {
				earliest = now
			}
		}
		monthStart := time.Date(earliest.Year(), earliest.Month(), 1, 0, 0, 0, 0, location)
		monthCount := (now.Year()-monthStart.Year())*12 + int(now.Month()-monthStart.Month()) + 1
		if monthCount <= 36 {
			resolved.BucketUnit = BucketMonth
			resolved.Buckets = buildBuckets(monthStart, endUnixMS, func(value time.Time) time.Time {
				return value.AddDate(0, 1, 0)
			})
		} else {
			resolved.BucketUnit = BucketYear
			yearStart := time.Date(earliest.Year(), time.January, 1, 0, 0, 0, 0, location)
			resolved.Buckets = buildBuckets(yearStart, endUnixMS, func(value time.Time) time.Time {
				return value.AddDate(1, 0, 0)
			})
		}
	}
	return resolved, nil
}

func localDayStart(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
}

func buildBuckets(start time.Time, endUnixMS int64, next func(time.Time) time.Time) []TimeBucket {
	if start.UnixMilli() >= endUnixMS {
		return nil
	}
	buckets := make([]TimeBucket, 0)
	for cursor := start; cursor.UnixMilli() < endUnixMS; {
		following := next(cursor)
		followingUnixMS := following.UnixMilli()
		if followingUnixMS <= cursor.UnixMilli() {
			break
		}
		if followingUnixMS > endUnixMS {
			followingUnixMS = endUnixMS
		}
		buckets = append(buckets, TimeBucket{StartUnixMS: cursor.UnixMilli(), EndUnixMS: followingUnixMS})
		cursor = following
	}
	return buckets
}
