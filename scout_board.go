package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type scoutCard struct {
	Name       string         `json:"name"`
	Attributes map[string]int `json:"attributes" jsonschema:"Known FM attributes from 1 to 20; omit unknown values"`
	Fee        *int           `json:"fee,omitempty" jsonschema:"Known asking fee in the same currency as the budget"`
	Wage       *int           `json:"weekly_wage,omitempty" jsonschema:"Known requested weekly wage in the same currency as the budget"`
}

type scoutBoardInput struct {
	Players   []scoutCard    `json:"players" jsonschema:"Two to fifty candidate reports visible to the manager"`
	Weights   map[string]int `json:"attribute_weights" jsonschema:"Role attributes and weights from 1 to 10, using the same names as the reports"`
	Reference *scoutCard     `json:"reference,omitempty" jsonschema:"Optional player profile to find stylistic replacements"`
	MaxFee    *int           `json:"max_fee,omitempty"`
	MaxWage   *int           `json:"max_weekly_wage,omitempty"`
}

type scoutBoardRow struct {
	Name       string   `json:"name"`
	RoleScore  *float64 `json:"role_score_out_of_20,omitempty"`
	Similarity *float64 `json:"similarity_out_of_100,omitempty"`
	Fee        *int     `json:"fee,omitempty"`
	Wage       *int     `json:"weekly_wage,omitempty"`
	Reasons    []string `json:"reasons,omitempty"`
}

type scoutBoardReport struct {
	Ranked  []scoutBoardRow `json:"ranked"`
	Pending []scoutBoardRow `json:"unranked"`
	Note    string          `json:"note"`
}

func scoutAttributes(raw map[string]int, maxValue int) (map[string]int, error) {
	if len(raw) > 60 {
		return nil, fmt.Errorf("provide up to 60 attributes")
	}
	out := make(map[string]int, len(raw))
	for k, v := range raw {
		k = strings.Join(strings.Fields(strings.ReplaceAll(fold(k), "_", " ")), " ")
		if k == "" || v < 1 || v > maxValue || out[k] != 0 {
			return nil, fmt.Errorf("attribute names must be distinct and values must be between 1 and %d", maxValue)
		}
		out[k] = v
	}
	return out, nil
}

func buildScoutBoard(_ context.Context, in scoutBoardInput) (scoutBoardReport, error) {
	out := scoutBoardReport{Ranked: []scoutBoardRow{}, Pending: []scoutBoardRow{}}
	if len(in.Players) < 2 || len(in.Players) > 50 || len(in.Weights) == 0 || !between(in.MaxFee, 0, 1_000_000_000) ||
		!between(in.MaxWage, 0, 1_000_000_000) {
		return out, fmt.Errorf("provide 2 to 50 candidate reports, role weights and valid budget limits")
	}
	weights, err := scoutAttributes(in.Weights, 10)
	if err != nil {
		return out, err
	}
	keys := make([]string, 0, len(weights))
	total := 0
	for key, weight := range weights {
		keys = append(keys, key)
		total += weight
	}
	slices.Sort(keys)
	var reference map[string]int
	if in.Reference != nil {
		reference, err = scoutAttributes(in.Reference.Attributes, 20)
		if err != nil {
			return out, err
		}
		for _, key := range keys {
			if reference[key] == 0 {
				return out, fmt.Errorf("reference profile is missing %s", key)
			}
		}
	}
	seen := map[string]bool{}
	for _, p := range in.Players {
		name := fold(p.Name)
		if name == "" || seen[name] || !between(p.Fee, 0, 1_000_000_000) || !between(p.Wage, 0, 1_000_000_000) {
			return out, fmt.Errorf("candidate names must be distinct and monetary amounts valid")
		}
		seen[name] = true
		attrs, err := scoutAttributes(p.Attributes, 20)
		if err != nil {
			return out, err
		}
		row := scoutBoardRow{Name: strings.TrimSpace(p.Name), Fee: p.Fee, Wage: p.Wage}
		score, distance := 0, 0
		for _, key := range keys {
			v, w := attrs[key], weights[key]
			if v == 0 {
				row.Reasons = append(row.Reasons, "Missing "+key)
				continue
			}
			score += v * w
			delta := v - reference[key]
			distance += max(delta, -delta) * w
		}
		for _, cost := range []struct {
			name          string
			actual, limit *int
		}{{"asking fee", p.Fee, in.MaxFee}, {"weekly wage", p.Wage, in.MaxWage}} {
			if cost.limit == nil {
				continue
			}
			if cost.actual == nil {
				row.Reasons = append(row.Reasons, "Unknown "+cost.name)
			} else if *cost.actual > *cost.limit {
				row.Reasons = append(row.Reasons, "Above "+cost.name+" limit")
			}
		}
		if len(row.Reasons) > 0 {
			out.Pending = append(out.Pending, row)
			continue
		}
		role := float64(score) / float64(total)
		row.RoleScore = &role
		if reference != nil {
			similarity := 100 * (1 - float64(distance)/float64(19*total))
			row.Similarity = &similarity
		}
		out.Ranked = append(out.Ranked, row)
	}
	slices.SortStableFunc(out.Ranked, func(a, b scoutBoardRow) int {
		if reference != nil {
			if order := cmp.Compare(*b.Similarity, *a.Similarity); order != 0 {
				return order
			}
		}
		return cmp.Compare(*b.RoleScore, *a.RoleScore)
	})
	out.Note = "Scores use supplied attributes and weights. Similarity compares attribute profiles. Verify availability, interest and fees in FM."
	return out, nil
}

func registerScoutBoard(server *mcp.Server) {
	addTool(server, "rank_scout_reports", "Compare player reports using custom role weights, an optional reference player for similarity, and fee and wage ceilings.", true, buildScoutBoard)
}
