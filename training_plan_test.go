package main

import (
	"slices"
	"testing"
)

func TestLiveTrainingReplansAfterFixtureMoves(t *testing.T) {
	days := make([][]uint16, 7)
	for i := range days {
		days[i] = []uint16{302, 300, 300}
	}
	days[1], days[4] = []uint16{402, 300, 300}, []uint16{500, 300, 300}
	rest := make([][]uint16, 7)
	for i := range rest {
		rest[i] = []uint16{300, 300, 300}
	}
	calendar := trainerCalendar{Today: trainerDate{2026, 201}, Weeks: []trainerWeek{
		{Start: trainerDate{2026, 201}, Days: days},
		{Start: trainerDate{2026, 208}, Days: rest},
	}}
	in := trainingPlanInput{Days: 14, Focus: "build-up"}
	before, err := liveTrainingPlan(calendar, in)
	if err != nil {
		t.Fatal(err)
	}
	assertDay := func(report trainingPlanReport, offset int, reason string, sessions []string) {
		t.Helper()
		if row := report.Days[offset-1]; row.Reason != reason || !slices.Equal(row.Sessions, sessions) {
			t.Fatalf("day %d: %+v", offset, row)
		}
	}
	assertDay(before, 1, "protected_calendar_day", nil)
	assertDay(before, 3, "pre_match_preparation", []string{"Press Avoidance", "Rest", "Rest"})
	assertDay(before, 4, "match_day", nil)
	assertDay(before, 5, "post_match_recovery", []string{"Recovery", "Rest", "Rest"})
	assertDay(before, 7, "protected_calendar_day", nil)
	days[4], days[5] = days[5], days[4]
	after, err := liveTrainingPlan(calendar, in)
	if err != nil {
		t.Fatal(err)
	}
	assertDay(after, 4, "pre_match_preparation", []string{"Press Avoidance", "Rest", "Rest"})
	assertDay(after, 5, "match_day", nil)
	assertDay(after, 6, "post_match_recovery", []string{"Recovery", "Rest", "Rest"})
	calendar.Today = trainerDate{2026, 204}
	days[3] = []uint16{100, 300, 300}
	after, err = liveTrainingPlan(calendar, in)
	if err != nil {
		t.Fatal(err)
	}
	assertDay(after, 1, "pre_match_light_load", []string{"Build Up", "Rest", "Rest"})
}
