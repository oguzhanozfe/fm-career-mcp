package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestReviewMatchDistinguishesChanceQualityFromScore(t *testing.T) {
	m := matchRecord{For: matchSide{XG: new(0.71), Shots: new(13), LongShots: new(8), Goals: new(1)},
		Against: matchSide{XG: new(1.8), Shots: new(8), Goals: new(2)}}
	result, err := analyzeMatch(m)
	if err != nil || len(result.Findings) != 2 || result.Findings[1].Issue != "Low-quality shot selection" ||
		!strings.Contains(result.Findings[1].Change, "Work Ball Into Box") {
		t.Fatalf("unexpected analysis: %+v, %v", result, err)
	}
	m.For = matchSide{Goals: new(0)}
	m.Against = matchSide{Goals: new(3)}
	result, err = analyzeMatch(m)
	if err != nil || len(result.Findings) != 0 || len(result.Notes) == 0 {
		t.Fatalf("the score alone should not produce a diagnosis: %+v, %v", result, err)
	}
}

func TestStaffRoleAndOfferBudget(t *testing.T) {
	data := &staffData{Staff: []staff{
		{UID: 10, Name: "A", Job: "Coach", Club: "Other", Attributes: map[string]int{"physiotherapy": 20, "sports_science": 20}},
		{UID: 11, Name: "B", Attributes: map[string]int{"physiotherapy": 18, "sports_science": 16}},
		{UID: 12, Name: "C", Attributes: map[string]int{"physiotherapy": 20}},
	}}
	out, err := searchStaff(data, staffSearchInput{Role: "Physio", UnemployedOnly: true})
	if err != nil || out.Matches != 1 || out.Candidates[0].UID != 11 || out.Candidates[0].Fit != 17 {
		t.Fatalf("unexpected candidates: %+v, %v", out, err)
	}
	in := offerInput{Kind: "staff_contract", UID: 11, Role: "Physio", Months: 24, WeeklyWage: 1000, WageLimit: new(900)}
	if _, err := draftOffer(career{staff: data}, in); err == nil {
		t.Fatal("expected over-budget rejection")
	}
	in.WageLimit = new(1200)
	draft, err := draftOffer(career{staff: data}, in)
	if err != nil || draft.Candidate != "B" || draft.Action == "" {
		t.Fatalf("unexpected draft: %+v, %v", draft, err)
	}
}

func TestGameBridgeThroughMCP(t *testing.T) {
	var writes atomic.Int32
	var changed atomic.Bool
	var partial atomic.Bool
	var driftDuringPreview atomic.Bool
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/matches":
			fmt.Fprint(w, `{"teamName":"City","played":[{"date":"2026-07-04","opponent":"Town","goalsFor":1,"goalsAgainst":2}]}`)
		case "/api/v1/matches/2026-07-04":
			fmt.Fprint(w, `{"summary":{"date":"2026-07-04","opponent":"Town","goalsFor":1,"goalsAgainst":2},"teamStats":{"expectedGoals":0.7,"shots":12,"longShots":7},"opponentStats":{"expectedGoals":1.9,"shots":8},"substitutions":[]}`)
		case "/api/v1/tactics/options":
			fmt.Fprint(w, `{"mentalities":[{"key":"Positive"}]}`)
		case "/api/v1/tactics/role-fit":
			q := r.URL.Query()
			if q.Get("position") != "DM" || q.Get("inPossession") != "false" || q.Get("top") != "4" {
				http.Error(w, "unexpected role filters", http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, `{"roles":[{"role":"Screening Defensive Midfielder"}]}`)
		case "/api/v1/inbox":
			if r.URL.Query().Get("unreadOnly") != "true" {
				http.Error(w, "expected unread filter", http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, `{"messages":[]}`)
		case "/api/v1/teams/Górnik Zabrze":
			fmt.Fprint(w, `{"name":"Górnik Zabrze"}`)
		case "/api/v1/tactics":
			if r.Method == http.MethodGet {
				if changed.Load() {
					fmt.Fprint(w, `{"slot":2,"changed":true}`)
				} else {
					fmt.Fprint(w, `{"changed":false,"slot":2}`)
				}
				return
			}
			var in liveTacticInput
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Slot != 2 || in.Mentality != "Positive" {
				http.Error(w, "bad request body", http.StatusBadRequest)
				return
			}
			if r.URL.Query().Get("apply") == "true" {
				writes.Add(1)
				if partial.Load() {
					fmt.Fprint(w, `{"slot":2,"applied":true,"steps":[{"status":"Done"},{"status":"Failed"}]}`)
				} else {
					fmt.Fprint(w, `{"slot":2,"applied":true,"steps":[{"status":"Done"}]}`)
				}
			} else {
				fmt.Fprint(w, `{"slot":2,"applied":false,"steps":[{"status":"Planned"}]}`)
				if driftDuringPreview.Load() {
					changed.Store(false)
				}
			}
		case "/api/v1/squad":
			fmt.Fprint(w, `{"teamName":"City","players":[{"playerId":7}]}`)
		case "/api/v1/trials":
			if r.URL.Query().Get("apply") == "true" {
				writes.Add(1)
				fmt.Fprint(w, `{"applied":true,"count":1,"players":[{"playerId":7,"name":"Sam"}]}`)
			} else {
				fmt.Fprint(w, `{"applied":false,"count":1,"players":[{"playerId":7,"name":"Sam"}]}`)
			}
		case "/api/v1/trials/end":
			var in struct {
				PlayerIDs       []int
				ExpectedPlayers int
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.PlayerIDs) != 2 || in.ExpectedPlayers != 2 {
				http.Error(w, "bad trial request", http.StatusBadRequest)
				return
			}
			if r.URL.Query().Get("apply") == "true" {
				writes.Add(1)
				fmt.Fprint(w, `{"applied":true,"count":1,"players":[{"playerId":7}],"skipped":[{"playerId":9}]}`)
			} else {
				fmt.Fprint(w, `{"applied":false,"count":2,"players":[{"playerId":9},{"playerId":7}]}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer httpServer.Close()
	port := httpServer.Listener.Addr().(*net.TCPAddr).Port
	ctx := context.Background()
	srv, err := newServer(ctx, sources{}, editorConfig{gamePort: uint(port), gameClub: "City"})
	if err != nil {
		t.Fatal(err)
	}
	left, right := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, left, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, right, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	match := callTool[matchReview](t, session, "review_match", nil)
	if match.Match.Source != "live_fm27_bridge" || len(match.Findings) != 2 {
		t.Fatalf("unexpected live match: %+v", match)
	}
	options := callTool[map[string]any](t, session, "live_tactic_options", nil)
	if options["mentalities"] == nil {
		t.Fatal("missing tactic options")
	}
	status := callTool[map[string]any](t, session, "live_bridge_status", nil)
	if status["connected"] != true || status["writes_configured_for_current_team"] != true {
		t.Fatalf("unexpected bridge status: %+v", status)
	}
	inbox := callTool[map[string]any](t, session, "live_inbox", map[string]any{"unread_only": true})
	if inbox["messages"] == nil {
		t.Fatal("missing inbox")
	}
	opponent := callTool[map[string]any](t, session, "live_opponent_report", map[string]any{"team": "Górnik Zabrze"})
	if opponent["name"] != "Górnik Zabrze" {
		t.Fatalf("unexpected opponent: %+v", opponent)
	}
	roles := callTool[map[string]any](t, session, "live_role_fit", map[string]any{"position": "dm", "phase": "out_of_possession", "top": 4})
	if roles["roles"] == nil {
		t.Fatal("missing role fit")
	}
	args := map[string]any{"slot": 2, "mentality": "Positive"}
	preview := callTool[bridgePreview](t, session, "preview_live_tactic", args)
	if preview.Token == "" || writes.Load() != 0 {
		t.Fatal("preview should not write")
	}
	changed.Store(true)
	stale, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "apply_live_action", Arguments: map[string]any{"token": preview.Token}})
	if err != nil || !stale.IsError || writes.Load() != 0 {
		t.Fatalf("stale preview was accepted: %v %v", stale, err)
	}
	partial.Store(true)
	preview = callTool[bridgePreview](t, session, "preview_live_tactic", args)
	result := callTool[bridgeResult](t, session, "apply_live_action", map[string]any{"token": preview.Token})
	if result.Applied || result.Note == "" || writes.Load() != 1 {
		t.Fatalf("failed step should be exposed as partial: %+v", result)
	}
	preview = callTool[bridgePreview](t, session, "preview_live_tactic", args)
	driftDuringPreview.Store(true)
	stale, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "apply_live_action", Arguments: map[string]any{"token": preview.Token}})
	if err != nil || !stale.IsError || writes.Load() != 1 {
		t.Fatalf("state drift during the final dry run was accepted: %v %v", stale, err)
	}
	mismatched := callTool[bridgePreview](t, session, "preview_trial_offer", map[string]any{"player_id": 99, "weeks": 2})
	if mismatched.Token != "" || writes.Load() != 1 {
		t.Fatal("preview for a different player was accepted")
	}
	trial := callTool[bridgePreview](t, session, "preview_trial_offer", map[string]any{"player_id": 7, "weeks": 2})
	result = callTool[bridgeResult](t, session, "apply_live_action", map[string]any{"token": trial.Token})
	if !result.Applied || writes.Load() != 2 {
		t.Fatalf("trial failed: %s, %+v", strconv.Itoa(int(writes.Load())), result)
	}
	batch := callTool[bridgePreview](t, session, "preview_trial_batch", map[string]any{"player_ids": []int{7, 9}, "action": "end"})
	if batch.Token == "" {
		t.Fatalf("batch did not match the requested players: %+v", batch)
	}
	result = callTool[bridgeResult](t, session, "apply_live_action", map[string]any{"token": batch.Token})
	if result.Applied || result.Note == "" || writes.Load() != 3 {
		t.Fatalf("partial batch must report incomplete: %+v", result)
	}
}

func TestSavedMatchSnapshotSurvivesAutosave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "career.fm")
	writeFixture(t, path, "first revision")
	copied, cleanup, err := (matchSource{save: path}).snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	writeFixture(t, path, "next revision")
	snapshot, err := os.ReadFile(copied.save)
	if err != nil || string(snapshot) != "first revision" {
		t.Fatalf("snapshot changed: %q, %v", snapshot, err)
	}
	cleanup()
	if _, err := os.Stat(copied.save); !os.IsNotExist(err) {
		t.Fatalf("temporary save was not cleaned up: %v", err)
	}
}

func TestScoutUsesPermanentWageForLoanPlayers(t *testing.T) {
	data := &playerData{Players: []player{
		{UID: 1, Name: "One", Contract: &deal{Loan: true, Wage: new(100)}, Parent: &parentClub{Name: "Parent", Wage: new(2000)}},
		{UID: 2, Name: "Two", Contract: &deal{Loan: true, Wage: new(100)}},
		{UID: 3, Name: "Three", Contract: &deal{Loan: true, Wage: new(100)}, Parent: &parentClub{Name: "Parent", Wage: new(400)}},
	}}
	result, err := scout(data, scoutInput{MaxWage: new(1000)})
	if err != nil || len(result.Players) != 1 || result.Players[0].UID != 3 ||
		result.Players[0].Parent != "Parent" || value(result.Players[0].Wage) != 400 {
		t.Fatalf("incorrect wage filter: %+v, %v", result, err)
	}
}
