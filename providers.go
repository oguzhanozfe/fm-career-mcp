package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func callProvider(ctx context.Context, cmd *exec.Cmd, name string, args any) (map[string]any, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "fm-career-mcp", Version: version}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, fmt.Errorf("could not connect to provider: %w", err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("provider call failed: %w", err)
	}
	if result.IsError {
		for _, content := range result.Content {
			if msg, ok := content.(*mcp.TextContent); ok {
				runes := []rune(msg.Text)
				return nil, fmt.Errorf("provider reported: %s", string(runes[:min(len(runes), 1000)]))
			}
		}
		return nil, fmt.Errorf("provider could not complete %s", name)
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok && len(result.Content) == 1 {
		if msg, yes := result.Content[0].(*mcp.TextContent); yes && len(msg.Text) <= 256<<10 {
			_ = json.Unmarshal([]byte(msg.Text), &out)
		}
	}
	raw, err := json.Marshal(out)
	if err != nil || out == nil || len(raw) > 256<<10 {
		return nil, fmt.Errorf("provider returned an invalid or oversized report")
	}
	return out, nil
}

type napasMatchInput struct {
	Date string `json:"date,omitempty" jsonschema:"YYYY-MM-DD; omit for the managed first team's latest result"`
}

type napasPlayerInput struct {
	UID   uint32 `json:"uid" jsonschema:"Player UID from the Napas match or lineup report"`
	Limit int    `json:"limit,omitempty" jsonschema:"1 to 30 matches; defaults to 10"`
}

func registerNapas(server *mcp.Server, src sources, executable string) {
	run := func(ctx context.Context, name string, args any) (map[string]any, error) {
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		stable, cleanup, err := (matchSource{save: src.save}).snapshot()
		if err != nil {
			return nil, err
		}
		defer cleanup()
		cmd := exec.CommandContext(ctx, executable)
		cmd.Env = append(cmd.Environ(), "FM_SAVE="+stable.save, "FM_FOG_OF_WAR=on")
		return callProvider(ctx, cmd, name, args)
	}
	addTool(server, "saved_match_detail", "Use Napas to read events, player match statistics and available team figures from the managed first team's save.", true,
		func(ctx context.Context, in napasMatchInput) (map[string]any, error) {
			if in.Date != "" {
				if _, err := time.Parse(time.DateOnly, in.Date); err != nil {
					return nil, fmt.Errorf("date must be YYYY-MM-DD")
				}
			}
			return run(ctx, "get_match", in)
		})
	addTool(server, "saved_player_matches", "Read a player's recent match lines from the Napas save reader.", true,
		func(ctx context.Context, in napasPlayerInput) (map[string]any, error) {
			limit, err := bounded(in.Limit, 10, 30)
			if err != nil || in.UID == 0 {
				return nil, fmt.Errorf("provide a positive player UID and a limit from 1 to 30")
			}
			in.Limit = limit
			return run(ctx, "get_player_matches", in)
		})
	addTool(server, "saved_tactic_detail", "Read the saved tactic, roles and instructions in and out of possession with the Napas reader.", true,
		func(ctx context.Context, in struct{}) (map[string]any, error) { return run(ctx, "get_tactic", in) })
	addTool(server, "saved_role_xi", "Use Napas to rank a unique XI for the saved tactic's roles in both phases, excluding injured players by default. Check fatigue and eligibility separately.", true,
		func(ctx context.Context, in struct{}) (map[string]any, error) { return run(ctx, "best_xi", in) })
}

type recordingListInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"1 to 50; defaults to 15"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Next cursor from the previous page"`
}

type recordingInput struct {
	MatchID  string `json:"matchId" jsonschema:"Match ID from list_match_recordings"`
	Revision string `json:"recordingRevisionId,omitempty" jsonschema:"Pin the recording revision from list_match_recordings"`
	TeamID   string `json:"teamId,omitempty" jsonschema:"Optional team ID from the same recording"`
}

type recordingWindow struct {
	FromMinute *float64 `json:"fromMinute,omitempty"`
	ToMinute   *float64 `json:"toMinute,omitempty"`
	Period     int      `json:"period,omitempty" jsonschema:"Period 1 to 6"`
}

type recordingComparisonInput struct {
	recordingInput
	A recordingWindow `json:"windowA"`
	B recordingWindow `json:"windowB"`
}

func registerMatchLens(server *mcp.Server, executable, home string) {
	run := func(ctx context.Context, name string, args any) (map[string]any, error) {
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		flags := []string{"--no-auto-import"}
		if home != "" {
			flags = append(flags, "--home", home)
		}
		return callProvider(ctx, exec.CommandContext(ctx, executable, flags...), name, args)
	}
	addTool(server, "list_match_recordings", "List imported MatchLens recordings. Identify the correct career, match and revision before reviewing.", true,
		func(ctx context.Context, in recordingListInput) (map[string]any, error) {
			limit, err := bounded(in.Limit, 15, 50)
			if err != nil {
				return nil, err
			}
			in.Limit = limit
			return run(ctx, "list_matches", in)
		})
	for _, entry := range []struct{ name, upstream, description string }{
		{"recorded_match_summary", "get_match_summary", "Read recorded team metrics, substitutions and roster. Respect the recording's completion and coverage evidence."},
		{"recorded_passing_network", "get_passing_network", "Read recorded passing links. Use the recipient evidence gate before interpreting missing links."},
		{"recorded_opposition_threat", "get_opposition_threat", "Read the opposition's recorded chance creation by attacking lane."},
	} {
		addTool(server, entry.name, entry.description, true, func(ctx context.Context, in recordingInput) (map[string]any, error) {
			return run(ctx, entry.upstream, in)
		})
	}
	addTool(server, "compare_recorded_periods", "Compare two windows in one MatchLens recording. Use coverage and score-state context before suggesting a tactical change.", true,
		func(ctx context.Context, in recordingComparisonInput) (map[string]any, error) {
			return run(ctx, "compare_match_periods", in)
		})
}
