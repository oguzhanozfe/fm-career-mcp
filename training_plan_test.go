package main

import (
	"testing"
	"time"
)

func TestTrainingPlanMovesWithFixtures(t *testing.T) {
	today := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		match string
		days  []string
	}{
		{"2026-07-23", []string{"2026-07-22", "2026-07-23", "2026-07-24"}},
		{"2026-07-24", []string{"2026-07-23", "2026-07-24", "2026-07-25"}},
	}
	for _, c := range cases {
		plan, err := planTeamTraining(today, trainingPlanInput{Focus: "build-up"}, map[string]bool{c.match: true}, map[string]bool{"2026-07-21": true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		byDay := map[string]string{}
		for _, day := range plan.Days {
			byDay[day.Date] = day.Reason
		}
		if byDay["2026-07-21"] != "protected_calendar_day" || byDay[c.days[0]] != "pre_match_preparation" ||
			byDay[c.days[1]] != "match_day" || byDay[c.days[2]] != "post_match_recovery" {
			t.Fatalf("%s: %+v", c.match, plan)
		}
	}
}
