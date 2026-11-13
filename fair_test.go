package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFairSaveTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture reader uses a POSIX shell")
	}
	ctx := context.Background()
	dir := t.TempDir()
	save := filepath.Join(dir, "Career.fm")
	writeFixture(t, save, "original save")
	cli := filepath.Join(dir, "reader")
	if err := os.WriteFile(cli, []byte(`#!/bin/sh
test "$2" != "${0%/*}/Career.fm" || exit 1
case "$1:$3" in
  info:--json) echo '{"game_date":"2026-07-12","game":"FM27","potential_ability":200}' ;;
  export:managed-clubs) echo '{"club_uid":7,"club_name":"Home"}' ;;
  export:fixtures) echo '{"date":"2026-07-11","home_team_id":1,"away_team_id":2,"home_club_uid":7,"home_club_name":"Home","away_club_name":"Rivals","home_team_slot":0,"home_goals":2,"away_goals":0,"played":true}' ;;
  export:team-match-stats) exit 1 ;;
  export:tactics) echo '{"club_uid":7,"team_slot":0,"name":"Balanced","potential_ability":200}' ;;
  export:selections) echo '{"club_uid":7,"team_slot":0,"slot":0,"player_name":"Keeper","current_ability":199}' ;;
  export:player-season-stats) echo '{"club_uid":7,"team_slot":0,"kind":"overall","player_uid":1,"player_name":"Keeper","minutes":90,"current_ability":199}' ;;
  *) touch "${0%/*}/unexpected"; exit 1 ;;
esac
`), 0700); err != nil {
		t.Fatal(err)
	}
	src := sources{save: save, matchCLI: cli, parser: "must-not-run"}
	if _, err := newFairServer(src, editorConfig{gamePort: 5120}); err == nil {
		t.Fatal("fair mode accepted live API")
	}
	server, err := newFairServer(src, editorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	left, right := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, left, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, right, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s should be read-only", tool.Name)
		}
	}
	slices.Sort(names)
	want := []string{"career_info", "coach_brief", "compare_matches", "contract_watch", "fixture_plan", "plan_team_training", "plan_visible_xi", "player_performance", "rank_scout_reports", "recent_form", "review_match", "saved_lineup", "training_calendar"}
	if !slices.Equal(names, want) {
		t.Fatalf("unexpected fair tools: %v", names)
	}
	out := callTool[map[string]any](t, session, "coach_brief", nil)
	brief := callTool[fairBrief](t, session, "coach_brief", nil)
	if brief.Career.File != "Career.fm" || brief.Career.Date != "2026-07-12" || brief.Club != "Home" ||
		brief.LastMatch == nil || brief.LastMatch.Match.Source != "saved_fixture" || len(brief.SavedTactics) != 1 ||
		brief.SavedLineup == nil || len(brief.SavedLineup.Starters) != 1 {
		t.Fatalf("unexpected fair brief: %+v", brief)
	}
	if strings.Contains(fmt.Sprint(out), "_ability") {
		t.Fatalf("fair response included ability data: %v", out)
	}
	performance := callTool[map[string]any](t, session, "player_performance", nil)
	if !strings.Contains(fmt.Sprint(performance), "Keeper") || strings.Contains(fmt.Sprint(performance), "_ability") {
		t.Fatalf("unexpected fair performance report: %v", performance)
	}
	if _, err := os.Stat(filepath.Join(dir, "unexpected")); !os.IsNotExist(err) {
		t.Fatal("fair mode attempted a non-allowlisted export")
	}
}

func TestPlanVisibleXI(t *testing.T) {
	out, err := planVisibleXI(context.Background(), visibleXIInput{Players: []visiblePlayer{
		{Name: "Multi", Positions: []string{"DC", "DR"}},
		{Name: "Centre", Positions: []string{"DC"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out.XI[1].Player != "Multi" || out.XI[2].Player != "Centre" || out.XI[1].PlayerUID != 0 || out.XI[1].Fit != 0 {
		t.Fatalf("unexpected selection: %+v", out.XI)
	}
}
