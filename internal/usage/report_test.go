package usage

import (
	"testing"
	"time"
)

func TestResolveRangeUsesLocalCalendarBoundaries(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, time.July, 10, 13, 45, 0, 0, location)

	today, err := ResolveRange(RangeToday, now, 0)
	if err != nil {
		t.Fatalf("resolve today: %v", err)
	}
	if today.BucketUnit != BucketHour || len(today.Buckets) != 14 {
		t.Fatalf("unexpected today range: %#v", today)
	}
	wantTodayStart := time.Date(2026, time.July, 10, 0, 0, 0, 0, location).UnixMilli()
	if today.StartUnixMS == nil || *today.StartUnixMS != wantTodayStart {
		t.Fatalf("unexpected today start: %#v", today.StartUnixMS)
	}

	sevenDays, err := ResolveRange(Range7Days, now, 0)
	if err != nil {
		t.Fatalf("resolve 7d: %v", err)
	}
	if sevenDays.BucketUnit != BucketDay || len(sevenDays.Buckets) != 7 {
		t.Fatalf("unexpected 7d range: %#v", sevenDays)
	}
	wantSevenStart := time.Date(2026, time.July, 4, 0, 0, 0, 0, location).UnixMilli()
	if sevenDays.StartUnixMS == nil || *sevenDays.StartUnixMS != wantSevenStart {
		t.Fatalf("unexpected 7d start: %#v", sevenDays.StartUnixMS)
	}

	thirtyDays, err := ResolveRange(Range30Days, now, 0)
	if err != nil {
		t.Fatalf("resolve 30d: %v", err)
	}
	if thirtyDays.BucketUnit != BucketDay || len(thirtyDays.Buckets) != 30 {
		t.Fatalf("unexpected 30d range: %#v", thirtyDays)
	}
}

func TestResolveRangeHandlesDSTDays(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	spring, err := ResolveRange(RangeToday, time.Date(2026, time.March, 8, 23, 30, 0, 0, location), 0)
	if err != nil {
		t.Fatalf("resolve spring range: %v", err)
	}
	if len(spring.Buckets) != 23 {
		t.Fatalf("expected 23 spring-forward buckets, got %d", len(spring.Buckets))
	}

	fall, err := ResolveRange(RangeToday, time.Date(2026, time.November, 1, 23, 30, 0, 0, location), 0)
	if err != nil {
		t.Fatalf("resolve fall range: %v", err)
	}
	if len(fall.Buckets) != 25 {
		t.Fatalf("expected 25 fall-back buckets, got %d", len(fall.Buckets))
	}
}

func TestResolveAllRangeUsesMonthThenYearBuckets(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, time.July, 10, 12, 0, 0, 0, location)

	monthly, err := ResolveRange(RangeAll, now, time.Date(2024, time.January, 15, 0, 0, 0, 0, location).UnixMilli())
	if err != nil {
		t.Fatalf("resolve monthly all range: %v", err)
	}
	if monthly.StartUnixMS != nil || monthly.BucketUnit != BucketMonth || len(monthly.Buckets) != 31 {
		t.Fatalf("unexpected monthly all range: %#v", monthly)
	}

	yearly, err := ResolveRange(RangeAll, now, time.Date(2020, time.December, 31, 0, 0, 0, 0, location).UnixMilli())
	if err != nil {
		t.Fatalf("resolve yearly all range: %v", err)
	}
	if yearly.StartUnixMS != nil || yearly.BucketUnit != BucketYear || len(yearly.Buckets) != 7 {
		t.Fatalf("unexpected yearly all range: %#v", yearly)
	}
}

func TestParseRangePresetRejectsUnsupportedValue(t *testing.T) {
	if _, err := ParseRangePreset("14d"); err == nil {
		t.Fatal("expected unsupported range to fail")
	}
}
