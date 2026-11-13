package main

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"testing"
)

func TestPlayerPerformanceScopeAndSample(t *testing.T) {
	var rows []savedPerformance
	err := json.Unmarshal([]byte(`[
		{"player_uid":1,"player_name":"Starter","club_uid":7,"team_slot":0,"kind":"league","minutes":900,"expected_goals":5,"potential_ability":200},
		{"player_uid":2,"player_name":"Brief appearance","club_uid":7,"team_slot":0,"kind":"league","minutes":30,"expected_goals":2},
		{"player_uid":3,"player_name":"Other club","club_uid":8,"team_slot":0,"kind":"league","minutes":900,"expected_goals":20},
		{"player_uid":4,"player_name":"Reserves","club_uid":7,"team_slot":1,"kind":"league","minutes":900,"expected_goals":20},
		{"player_uid":5,"player_name":"Cup player","club_uid":7,"team_slot":0,"kind":"cup","minutes":900,"expected_goals":20},
		{"player_uid":6,"player_name":"No data","club_uid":7,"team_slot":0,"kind":"league","minutes":900},
		{"player_uid":7,"player_name":"Zero","club_uid":7,"team_slot":0,"kind":"league","minutes":600,"expected_goals":0}
	]`), &rows)
	if err != nil {
		t.Fatal(err)
	}
	report, err := playerPerformance(rows, managedClub{UID: 7}, performanceInput{Competition: "league", Sort: "xg_per_90"})
	if err != nil || report.MinMinutes != 450 || report.Matched != 2 || report.Players[0].UID != 1 ||
		math.Abs(*report.Players[0].XGPer90-0.5) > 0.001 || *report.Players[1].XGPer90 != 0 ||
		!slices.Equal(report.Available, []string{"league", "cup"}) {
		t.Fatalf("unexpected report: %+v, %v", report, err)
	}
}

func TestScoutBoardRanksSimilarityAndPreservesUnknowns(t *testing.T) {
	budget, affordable, costly := 20, 10, 25
	report, err := buildScoutBoard(context.Background(), scoutBoardInput{
		Weights:   map[string]int{"Work Rate": 3, "Passing": 1},
		Reference: &scoutCard{Attributes: map[string]int{"work_rate": 13, "passing": 12}},
		MaxFee:    &budget,
		Players: []scoutCard{
			{Name: "Higher rating", Attributes: map[string]int{"work_rate": 20, "passing": 20}, Fee: &affordable},
			{Name: "Best match", Attributes: map[string]int{"work_rate": 13, "passing": 12}, Fee: &affordable},
			{Name: "Unknown", Attributes: map[string]int{"passing": 15}},
			{Name: "Over budget", Attributes: map[string]int{"work_rate": 13, "passing": 12}, Fee: &costly},
		},
	})
	if err != nil || len(report.Ranked) != 2 || report.Ranked[0].Name != "Best match" ||
		*report.Ranked[0].RoleScore != 12.75 || *report.Ranked[0].Similarity != 100 || len(report.Pending) != 2 ||
		len(report.Pending[0].Reasons) != 2 || report.Pending[1].Reasons[0] != "Above asking fee limit" {
		t.Fatalf("unexpected board: %+v, %v", report, err)
	}
}

func TestVisibleXICustomShape(t *testing.T) {
	var slots []visibleSlot
	for _, code := range []string{"GK", "DC", "DC", "DC", "WBR", "WBL", "DM", "MC", "MC", "ST", "ST"} {
		slots = append(slots, visibleSlot{Position: code})
	}
	out, err := planVisibleXI(context.Background(), visibleXIInput{Slots: slots,
		Players: []visiblePlayer{{Name: "Wingback", Positions: []string{"WBR"}}}})
	if err != nil || len(out.XI) != 11 || out.XI[4].Player != "Wingback" || out.XI[5].Player != "" {
		t.Fatalf("unexpected XI: %+v, %v", out.XI, err)
	}
}
