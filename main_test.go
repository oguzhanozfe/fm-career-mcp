package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPReports(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := sources{playerJSON: filepath.Join(dir, "players.json"), staffJSON: filepath.Join(dir, "staff.json")}
	writeFixture(t, src.playerJSON, `{
	  "version":{"major":27,"current_date":"2026-06-30"},"probe":null,
	  "players":[
	    {"uid":1,"name":"Alex","club":{"uid":7,"name":"Şehir FC"},"current_ability":129,"potential_ability":144,"positions":{"ST":18}},
	    {"uid":2,"name":"Cem","club":{"uid":7,"name":"Şehir FC"},"current_ability":130,"potential_ability":144,"positions":{"ST":20}},
	    {"uid":3,"name":"Jo","club":{"uid":7,"name":"Şehir FC"},"current_ability":129,"potential_ability":145},
	    {"uid":4,"name":"Den","club":{"uid":7,"name":"Şehir FC"},"current_ability":90,"potential_ability":-8},
	    {"uid":5,"name":"Leo","club":{"uid":7,"name":"Şehir FC"},"current_ability":125,"potential_ability":140,"contract":{"loan":true}},
	    {"uid":6,"name":"Ali","club":{"uid":8,"name":"Town FC"},"loan_from":{"club_uid":7},"current_ability":128,"potential_ability":140},
	    {"uid":7,"name":"Kim","club":{"uid":8,"name":"Town FC"},"current_ability":126,"potential_ability":150,"date_of_birth":"2004-07-01","positions":{"DM":18},"contract":{"weekly_wage":1000}}
	  ]
	}`)
	writeFixture(t, src.staffJSON, `{"gameDate":"2026-06-29","staff":[{"uid":9,"name":"Sam","club":"Şehir FC","jobName":"Physio","attributes":{"physiotherapy":17}}]}`)
	server, err := newServer(ctx, src, editorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 10 {
		t.Fatalf("unexpected default tools: %v, %v", tools, err)
	}
	for _, tool := range tools.Tools {
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s should be read-only", tool.Name)
		}
	}
	info := callTool[careerInfo](t, session, "career_info", nil)
	if info.Players == nil || *info.Players != 7 || len(info.Warnings) != 1 {
		t.Fatalf("unexpected career info: %+v", info)
	}
	clubs := callTool[clubList](t, session, "find_clubs", map[string]any{"name": "sehir"})
	if len(clubs.Clubs) != 1 || clubs.Clubs[0].UID != 7 {
		t.Fatalf("unexpected clubs: %+v", clubs)
	}
	squad := callTool[squadOutput](t, session, "squad_review", map[string]any{"club_uid": 7, "below_ca": 130, "below_pa": 145})
	if len(squad.Exits) != 2 || squad.Exits[0].UID != 1 || squad.Exits[1].UID != 6 {
		t.Fatalf("unexpected exit shortlist: %+v", squad.Exits)
	}
	scout := callTool[scoutOutput](t, session, "scout_players", map[string]any{"position": "DM", "max_age": 21, "max_weekly_wage_gbp": 1000})
	if len(scout.Players) != 1 || scout.Players[0].Age == nil || *scout.Players[0].Age != 21 {
		t.Fatalf("unexpected scout results: %+v", scout)
	}
	staff := callTool[staffOutput](t, session, "staff_audit", map[string]any{
		"club": "sehir FC", "targets": map[string]int{"Physio": 3, "Scout": 1},
	})
	if len(staff.Roles) != 2 || *staff.Roles[0].Vacancies != 2 || *staff.Roles[1].Vacancies != 1 {
		t.Fatalf("unexpected vacancies: %+v", staff.Roles)
	}

	writeFixture(t, src.playerJSON, `invalid JSON`)
	failed, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "reload_career"})
	if err != nil || !failed.IsError {
		t.Fatalf("reload should fail for invalid input: %v, %v", failed, err)
	}
	after := callTool[careerInfo](t, session, "career_info", nil)
	if after.Players == nil || *after.Players != 7 {
		t.Fatal("failed reload dropped previous data")
	}
}

func writeFixture(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func callTool[T any](t *testing.T, session *mcp.ClientSession, name string, in map[string]any) T {
	t.Helper()
	out, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: in})
	if err != nil || out.IsError {
		t.Fatalf("%s failed: %v, %v", name, out, err)
	}
	raw, err := json.Marshal(out.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestXIReservesVersatilePlayerForOnlyFit(t *testing.T) {
	squad := []player{
		{UID: 1, CA: 140, Positions: map[string]int{"DC": 20, "DR": 18}},
		{UID: 2, CA: 125, Positions: map[string]int{"DC": 17}},
	}
	out := selectXI(squad, []slotSpec{{"DC", "Centre-back"}, {"DR", "Full-back"}})
	if out[0].PlayerUID != 2 || out[1].PlayerUID != 1 {
		t.Fatalf("unexpected assignment: %+v", out)
	}
}

func TestApprovalCannotBeReused(t *testing.T) {
	var a approval[int]
	token := a.issue(42)
	if _, err := a.take("wrong"); err == nil {
		t.Fatal("accepted the wrong token")
	}
	if value, err := a.take(token); err != nil || value != 42 {
		t.Fatalf("valid token failed: %d, %v", value, err)
	}
	if _, err := a.take(token); err == nil {
		t.Fatal("accepted the same token twice")
	}
}

func TestPublishCopyRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.fm")
	if err := publishCopy(path, []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := publishCopy(path, []byte("changed")); err == nil {
		t.Fatal("overwrote an existing file")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("original changed: %q, %v", data, err)
	}
}

func TestHolidayLayoutAndClubIsolation(t *testing.T) {
	data := &playerData{Players: []player{
		{UID: 1, Offset: 50, CA: 100, Club: &club{UID: 7}},
		{UID: 2, Offset: 500, CA: 100, Club: &club{UID: 8}},
	}}
	data.Version.Date = "2026-07-14"
	db := make([]byte, 1000)
	for _, p := range data.Players {
		binary.LittleEndian.PutUint32(db[p.Offset-15:], p.UID)
		binary.LittleEndian.PutUint32(db[p.Offset-11:], p.UID)
		binary.LittleEndian.PutUint16(db[p.Offset:], uint16(p.CA))
		at := p.Offset + 330
		copy(db[at-16:], holidayPrefix)
		binary.LittleEndian.PutUint16(db[at-9:], 22)
		copy(db[at-7:], holidayMiddle)
		binary.LittleEndian.PutUint16(db[at-4:], 177)
		binary.LittleEndian.PutUint16(db[at-2:], 2026)
		copy(db[at:], holidaySuffix)
	}
	edits, err := holidayChanges(data, db, 7)
	if err != nil || len(edits) != 1 || edits[0].offset != 366 || edits[0].player.UID != 1 {
		t.Fatalf("unexpected holiday changes: %v, %v", edits, err)
	}
	db[366] = 2
	if _, err := holidayChanges(data, db, 7); err == nil {
		t.Fatal("unsupported holiday layout was accepted")
	}
}
