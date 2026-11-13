package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type comparePlayersInput struct {
	UIDs []uint32 `json:"uids" jsonschema:"Two to five player UIDs from scout_players or squad_review"`
}

type playerComparison struct {
	Player    playerRow      `json:"player"`
	Positions map[string]int `json:"positions"`
}

func comparePlayers(data *playerData, in comparePlayersInput) ([]playerComparison, error) {
	if len(in.UIDs) < 2 || len(in.UIDs) > 5 {
		return nil, fmt.Errorf("provide two to five distinct player UIDs")
	}
	out := make([]playerComparison, 0, len(in.UIDs))
	seen := map[uint32]bool{}
	for _, uid := range in.UIDs {
		if uid == 0 || seen[uid] {
			return nil, fmt.Errorf("provide distinct positive player UIDs")
		}
		seen[uid] = true
		i := slices.IndexFunc(data.Players, func(p player) bool { return p.UID == uid })
		if i < 0 {
			return nil, fmt.Errorf("player UID %d was not found", uid)
		}
		p := data.Players[i]
		out = append(out, playerComparison{Player: p.row(data.Version.Date), Positions: p.Positions})
	}
	return out, nil
}

type gapInput struct {
	ClubUID uint32   `json:"club_uid" jsonschema:"Club UID from find_clubs"`
	Style   string   `json:"style,omitempty" jsonschema:"balanced, press, or counter"`
	Exclude []uint32 `json:"exclude_player_uids,omitempty" jsonschema:"Players to leave out, including known unavailability"`
}

type positionGap struct {
	Position             string `json:"position"`
	StartingSlots        int    `json:"starting_slots"`
	NaturalOptions       int    `json:"options_at_15_or_higher"`
	MissingWithOneBackup int    `json:"shortfall_including_one_backup"`
}

func depthGaps(data *playerData, in gapInput) ([]positionGap, error) {
	tactic, err := buildTactic(data, tacticInput{ClubUID: in.ClubUID, Style: in.Style, Exclude: in.Exclude})
	if err != nil {
		return nil, err
	}
	needed := map[string]int{}
	for _, slot := range tactic.XI {
		needed[slot.Position]++
	}
	out := []positionGap{}
	for _, pos := range positions {
		if needed[pos] == 0 {
			continue
		}
		seen := map[uint32]bool{}
		for _, p := range data.Players {
			if p.UID != 0 && p.Club != nil && p.Club.UID == in.ClubUID && p.Club.TeamSlot == 0 && p.Positions[pos] >= 15 && !slices.Contains(in.Exclude, p.UID) {
				seen[p.UID] = true
			}
		}
		if gap := needed[pos] + 1 - len(seen); gap > 0 {
			out = append(out, positionGap{pos, needed[pos], len(seen), gap})
		}
	}
	slices.SortStableFunc(out, func(a, b positionGap) int { return cmp.Compare(b.MissingWithOneBackup, a.MissingWithOneBackup) })
	return out, nil
}

type loanDraftInput struct {
	UID                uint32 `json:"uid" jsonschema:"Player UID from scout_players"`
	Weeks              int    `json:"weeks" jsonschema:"Proposed duration from 1 to 52 weeks"`
	WeeklyContribution int    `json:"weekly_contribution_gbp" jsonschema:"Proposed weekly wage contribution in GBP"`
	MonthlyFee         int    `json:"monthly_fee_gbp" jsonschema:"Proposed monthly fee in GBP, including zero"`
	TotalLimit         *int64 `json:"total_limit_gbp,omitempty" jsonschema:"Optional total cost ceiling in GBP"`
}

type loanDraft struct {
	Candidate          string `json:"candidate"`
	Club               string `json:"club,omitempty"`
	Weeks              int    `json:"weeks"`
	WeeklyContribution int    `json:"weekly_contribution_gbp"`
	MonthlyFee         int    `json:"monthly_fee_gbp"`
	EstimatedTotal     int64  `json:"estimated_total_gbp"`
	Note               string `json:"note"`
}

func draftLoan(data *playerData, in loanDraftInput) (loanDraft, error) {
	var out loanDraft
	if in.UID == 0 || in.Weeks < 1 || in.Weeks > 52 || in.WeeklyContribution < 0 || in.WeeklyContribution > 2_000_000 || in.MonthlyFee < 0 || in.MonthlyFee > 100_000_000 || (in.TotalLimit != nil && *in.TotalLimit < 0) {
		return out, fmt.Errorf("invalid candidate, duration or proposed loan terms")
	}
	// Round up calendar months at 52 weeks per year for a conservative estimate.
	months := (int64(in.Weeks)*12 + 51) / 52
	total := int64(in.Weeks)*int64(in.WeeklyContribution) + months*int64(in.MonthlyFee)
	if in.TotalLimit != nil && total > *in.TotalLimit {
		return out, fmt.Errorf("proposal exceeds the total cost ceiling")
	}
	i := slices.IndexFunc(data.Players, func(p player) bool { return p.UID == in.UID && p.Name != "" })
	if i < 0 {
		return out, fmt.Errorf("player was not found")
	}
	p := data.Players[i]
	if p.Club == nil || p.onLoan() {
		return out, fmt.Errorf("player must have a club and not already be on loan")
	}
	return loanDraft{p.Name, p.Club.Name, in.Weeks, in.WeeklyContribution, in.MonthlyFee, total,
		"Draft only. Confirm the actual fees, wage share and acceptance in FM. The estimate rounds monthly fees up and excludes optional clauses."}, nil
}

func registerSquadTools(server *mcp.Server, current func(context.Context) (*career, error)) {
	addCareerTool(server, "compare_players", "Compare position ratings, CA, PA and saved contract wages for two to five players.", current,
		func(c *career, in comparePlayersInput) (map[string]any, error) {
			rows, err := comparePlayers(c.players, in)
			return map[string]any{"players": rows, "note": "Stored values are unverified and do not establish an asking price."}, err
		})
	addCareerTool(server, "depth_gaps", "Find formation-specific depth gaps, including one backup per position. Eligible players may cover multiple positions.", current,
		func(c *career, in gapInput) (map[string]any, error) {
			rows, err := depthGaps(c.players, in)
			return map[string]any{"gaps": rows}, err
		})
	addCareerTool(server, "draft_loan", "Draft a loan and estimate total fees and wages against a supplied ceiling. No offer is sent.", current,
		func(c *career, in loanDraftInput) (loanDraft, error) { return draftLoan(c.players, in) })
}
