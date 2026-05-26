package handlers

import (
	"testing"
	"time"

	"btcpp-web/internal/types"
)

func TestSocialZipSchedulePrefix(t *testing.T) {
	loc := time.FixedZone("CEST", 2*60*60)
	conf := &types.Conf{StartDate: time.Date(2026, 4, 17, 9, 0, 0, 0, loc)}
	talk := &types.Talk{
		Venue: "one",
		Sched: &types.Times{
			Start: time.Date(2026, 4, 17, 15, 30, 0, 0, loc),
		},
	}

	if got, want := socialZipSchedulePrefix(conf, talk), "01main1530_"; got != want {
		t.Fatalf("socialZipSchedulePrefix = %q, want %q", got, want)
	}
}

func TestSocialZipSchedulePrefixUsesConferenceTimezone(t *testing.T) {
	vienna := time.FixedZone("CEST", 2*60*60)
	utc := time.UTC
	conf := &types.Conf{
		StartDate: time.Date(2026, 4, 17, 0, 0, 0, 0, utc),
		Timezone:  "Europe/Vienna",
		TZ:        vienna,
	}
	talk := &types.Talk{
		Venue: "two",
		Sched: &types.Times{
			Start: time.Date(2026, 4, 18, 13, 30, 0, 0, utc),
		},
	}

	if got, want := socialZipSchedulePrefix(conf, talk), "02talks1530_"; got != want {
		t.Fatalf("socialZipSchedulePrefix = %q, want %q", got, want)
	}
}
