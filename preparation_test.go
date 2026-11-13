package main

import (
	"math"
	"testing"
	"time"
)

func TestMatchPlanningUsesCoveredShotsAndCalendarGaps(t *testing.T) {
	fixtures := []savedFixture{
		{matchKey: matchKey{Date: "2026-07-18"}, HomeOwn: true, AwayName: "Next"},
		{matchKey: matchKey{Date: "2026-07-15", Home: 1, Away: 2}, HomeOwn: true, AwayName: "Last", Played: true, HomeGoals: new(2), AwayGoals: new(0)},
		{matchKey: matchKey{Date: "2026-07-12"}, Played: true, HomeGoals: new(1), AwayGoals: new(1)},
	}
	plan := fixturePlans(fixtures, "2026-07-18", 3)
	if len(plan) != 1 || value(plan[0].DaysSincePrevious) != 3 {
		t.Fatalf("unexpected fixture plan: %+v", plan)
	}
	stats := []savedStats{
		{matchKey: fixtures[1].matchKey, Team: 1, XG: new(2.0), Sh: new(20), Long: new(9)},
		{matchKey: fixtures[1].matchKey, Team: 2},
	}
	form := recentForm(fixtures, stats, 5, "")
	if form.TeamReports != 1 || form.Wins != 1 || form.Draws != 1 || form.ShotQuality.LongShotShare == nil || math.Abs(*form.ShotQuality.LongShotShare-0.45) > 1e-9 {
		t.Fatalf("unexpected form: %+v", form)
	}
	comparison, err := compareMatches(fixtures, stats, compareMatchesInput{"2026-07-12", "2026-07-15"})
	if err != nil || comparison.XGDelta != nil || comparison.FollowUpQuality.XGPerShot == nil {
		t.Fatalf("missing report should not generate a delta: %+v %v", comparison, err)
	}
}

func TestContractAndLoanLimits(t *testing.T) {
	start := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	rows := []savedContract{
		{UID: 1, Club: 7, Ends: "2026-08-01", EndSource: "contract"},
		{UID: 2, Club: 7, Ends: "2026-08-01", EndSource: "contract", OnLoan: true},
		{UID: 3, Club: 7, Ends: "2026-08-01", EndSource: "estimate"},
		{UID: 4, Club: 8, Ends: "2026-08-01", EndSource: "contract"},
	}
	if got := expiringContracts(rows, 7, start, 30); len(got) != 1 || got[0].UID != 1 {
		t.Fatalf("unexpected contracts: %+v", got)
	}
	data := &playerData{Players: []player{{UID: 1, Name: "Player", Club: &club{Name: "Town"}}}}
	input := loanDraftInput{UID: 1, Weeks: 26, WeeklyContribution: 5000, MonthlyFee: 10000}
	draft, err := draftLoan(data, input)
	if err != nil || draft.EstimatedTotal != 190000 {
		t.Fatalf("unexpected draft: %+v %v", draft, err)
	}
	input.TotalLimit = new(int64(189999))
	if _, err := draftLoan(data, input); err == nil {
		t.Fatal("over-budget draft was accepted")
	}
}
