package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type performanceInput struct {
	Competition string `json:"competition,omitempty" jsonschema:"overall, league, cup, continental, or non_competitive; defaults to overall"`
	Sort        string `json:"sort,omitempty" jsonschema:"minutes, rating, goals_per_90, xg_per_90, xa_per_90, or progressive_passes_per_90; defaults to minutes"`
	MinMinutes  *int   `json:"min_minutes,omitempty" jsonschema:"Defaults to 450 for per-90 rankings and zero for minutes or rating"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Maximum results from 1 to 50; defaults to 15"`
}

type performanceRow struct {
	UID           uint32   `json:"player_uid"`
	Name          string   `json:"player_name"`
	Starts        *int     `json:"starts,omitempty"`
	SubApps       *int     `json:"substitute_appearances,omitempty"`
	Minutes       int      `json:"minutes"`
	Rating        *float64 `json:"average_rating,omitempty"`
	Goals         *int     `json:"goals,omitempty"`
	Assists       *int     `json:"assists,omitempty"`
	XG            *float64 `json:"expected_goals,omitempty"`
	XA            *float64 `json:"expected_assists,omitempty"`
	Progressive   *int     `json:"progressive_passes,omitempty"`
	GoalsPer90    *float64 `json:"goals_per_90,omitempty"`
	XGPer90       *float64 `json:"xg_per_90,omitempty"`
	XAPer90       *float64 `json:"xa_per_90,omitempty"`
	ProgressPer90 *float64 `json:"progressive_passes_per_90,omitempty"`
}

type savedPerformance struct {
	performanceRow
	Club     uint32 `json:"club_uid"`
	TeamSlot *int   `json:"team_slot"`
	Kind     string `json:"kind"`
}

type performanceReport struct {
	Club        string           `json:"club"`
	Competition string           `json:"competition"`
	Sort        string           `json:"sort"`
	MinMinutes  int              `json:"min_minutes"`
	Matched     int              `json:"matched"`
	Players     []performanceRow `json:"players"`
	Available   []string         `json:"available_competitions"`
	Note        string           `json:"note"`
}

func intMetric(v *int) *float64 {
	if v == nil {
		return nil
	}
	n := float64(*v)
	return &n
}

func per90(v *float64, minutes int) *float64 {
	if v == nil || minutes <= 0 {
		return nil
	}
	rate := *v * 90 / float64(minutes)
	return &rate
}

func playerPerformance(rows []savedPerformance, club managedClub, in performanceInput) (performanceReport, error) {
	scopes := []string{"overall", "league", "cup", "continental", "non_competitive"}
	out := performanceReport{Club: club.Name, Competition: cmp.Or(in.Competition, "overall"),
		Sort: cmp.Or(in.Sort, "minutes"), Players: []performanceRow{}, Available: []string{}}
	if !slices.Contains(scopes, out.Competition) {
		return out, fmt.Errorf("unsupported competition scope")
	}
	limit, err := bounded(in.Limit, 15, 50)
	if err != nil || !between(in.MinMinutes, 0, 100000) {
		return out, fmt.Errorf("limit must be 1 to 50 and min_minutes must be 0 to 100000")
	}
	var metric func(performanceRow) *float64
	switch out.Sort {
	case "minutes":
		metric = func(p performanceRow) *float64 { return intMetric(&p.Minutes) }
	case "rating":
		metric = func(p performanceRow) *float64 { return p.Rating }
	case "goals_per_90":
		metric = func(p performanceRow) *float64 { return p.GoalsPer90 }
	case "xg_per_90":
		metric = func(p performanceRow) *float64 { return p.XGPer90 }
	case "xa_per_90":
		metric = func(p performanceRow) *float64 { return p.XAPer90 }
	case "progressive_passes_per_90":
		metric = func(p performanceRow) *float64 { return p.ProgressPer90 }
	default:
		return out, fmt.Errorf("unsupported sort metric")
	}
	if in.MinMinutes != nil {
		out.MinMinutes = *in.MinMinutes
	} else if out.Sort != "minutes" && out.Sort != "rating" {
		out.MinMinutes = 450
	}
	seen := map[uint32]bool{}
	available := map[string]bool{}
	for _, row := range rows {
		p := row.performanceRow
		if row.Club != club.UID || row.TeamSlot == nil || *row.TeamSlot != 0 || p.UID == 0 || p.Name == "" || p.Minutes <= 0 {
			continue
		}
		available[row.Kind] = true
		if row.Kind != out.Competition || p.Minutes < out.MinMinutes {
			continue
		}
		if seen[p.UID] {
			return out, fmt.Errorf("multiple rows for a player in the same club and competition scope")
		}
		seen[p.UID] = true
		p.GoalsPer90, p.XGPer90 = per90(intMetric(p.Goals), p.Minutes), per90(p.XG, p.Minutes)
		p.XAPer90, p.ProgressPer90 = per90(p.XA, p.Minutes), per90(intMetric(p.Progressive), p.Minutes)
		if metric(p) != nil {
			out.Players = append(out.Players, p)
		}
	}
	slices.SortFunc(out.Players, func(a, b performanceRow) int {
		return cmp.Or(cmp.Compare(*metric(b), *metric(a)), cmp.Compare(b.Minutes, a.Minutes), cmp.Compare(a.UID, b.UID))
	})
	out.Matched = len(out.Players)
	out.Players = out.Players[:min(limit, out.Matched)]
	for _, scope := range scopes {
		if available[scope] {
			out.Available = append(out.Available, scope)
		}
	}
	out.Note = "Current-season first-team statistics. Use minutes, role and opposition context when interpreting rankings. Empty results mean no qualifying records in this scope."
	return out, nil
}

func registerPerformance(server *mcp.Server, src matchSource) {
	addSaveTool(server, src, "player_performance", "Rank the managed first team's current-season match statistics by minutes, rating or a per-90 measure.",
		func(ctx context.Context, s matchSource, club managedClub, in performanceInput) (performanceReport, error) {
			rows, err := exportRows[savedPerformance](ctx, s, "player-season-stats")
			if err != nil {
				return performanceReport{}, err
			}
			return playerPerformance(rows, club, in)
		})
}
