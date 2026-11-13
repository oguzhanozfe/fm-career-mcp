package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
)

type coachInput struct {
	Style   string   `json:"style,omitempty" jsonschema:"balanced, press, or counter; defaults to balanced"`
	Exclude []uint32 `json:"exclude_player_uids,omitempty" jsonschema:"Club player UIDs to rest or leave out of the proposed XI"`
}

type coachFixture struct {
	Date     string `json:"date"`
	Kick     string `json:"kick_off_time,omitempty"`
	Opponent string `json:"opponent"`
	Venue    string `json:"venue"`
}

type tacticSummary struct {
	Index     int    `json:"index"`
	Name      string `json:"name"`
	Style     string `json:"style_name,omitempty"`
	Mentality string `json:"mentality,omitempty"`
}

type savedTactic struct {
	Club     uint32 `json:"club_uid"`
	TeamSlot int    `json:"team_slot"`
	tacticSummary
}

type coachOutput struct {
	Career       careerInfo      `json:"career"`
	ClubUID      uint32          `json:"club_uid"`
	Upcoming     []coachFixture  `json:"upcoming,omitempty"`
	LastMatch    *matchReview    `json:"last_match,omitempty"`
	SavedTactics []tacticSummary `json:"saved_tactics,omitempty"`
	Proposal     tacticOutput    `json:"proposed_tactic"`
	Notes        []string        `json:"notes,omitempty"`
}

func coachBrief(ctx context.Context, src sources, in coachInput) (coachOutput, error) {
	stable, cleanup, err := (matchSource{cli: src.matchCLI, save: src.save}).snapshot()
	if err != nil {
		return coachOutput{}, err
	}
	defer cleanup()
	// Use the same revision for the squad and the match exports.
	src.save, src.staffJSON = stable.save, ""
	career, err := src.load(ctx)
	if err != nil {
		return coachOutput{}, err
	}
	career.save = stable.revision
	managed, fixtures, err := stable.firstTeamFixtures(ctx)
	if err != nil {
		return coachOutput{}, err
	}
	uid, err := managedSquadUID(career.players, managed.Name)
	if err != nil {
		return coachOutput{}, err
	}
	tactic, err := buildTactic(career.players, tacticInput{ClubUID: uid, Style: in.Style, Exclude: in.Exclude})
	if err != nil {
		return coachOutput{}, err
	}
	out := coachOutput{Career: career.info(), ClubUID: uid, Proposal: tactic}
	out.Upcoming = upcomingFixtures(fixtures, out.Career.GameDate)

	review, err := stable.analyzeSavedFixture(ctx, fixtures, matchInput{})
	if err == nil {
		out.LastMatch = &review
	} else {
		out.Notes = append(out.Notes, "Last match unavailable: "+err.Error())
	}

	out.SavedTactics, err = stable.savedTactics(ctx, managed.UID)
	if err != nil {
		out.Notes = append(out.Notes, "Saved tactic slots were unavailable.")
	}
	if len(out.SavedTactics) > 0 {
		out.Notes = append(out.Notes, "Saved tactic slots do not identify which tactic is active.")
	}
	return out, nil
}

func (s matchSource) savedTactics(ctx context.Context, club uint32) ([]tacticSummary, error) {
	tactics, err := exportRows[savedTactic](ctx, s, "tactics")
	if err != nil {
		return nil, err
	}
	var out []tacticSummary
	for _, tactic := range tactics {
		if tactic.Club == club && tactic.TeamSlot == 0 && tactic.Name != "" {
			out = append(out, tactic.tacticSummary)
		}
	}
	return out, nil
}

// The squad parser and match reader can assign different IDs to the same club.
func managedSquadUID(data *playerData, name string) (uint32, error) {
	name = fold(name)
	var uid uint32
	for _, p := range data.Players {
		club := p.Club
		if name == "" || club == nil || club.UID == 0 || club.TeamSlot != 0 || fold(club.Name) != name {
			continue
		}
		if uid != 0 && uid != club.UID {
			return 0, fmt.Errorf("managed club name matches multiple first teams")
		}
		uid = club.UID
	}
	if uid == 0 {
		return 0, fmt.Errorf("managed club name could not be matched to a first-team squad")
	}
	return uid, nil
}

func upcomingFixtures(fixtures []savedFixture, gameDate string) []coachFixture {
	var upcoming []coachFixture
	for _, f := range fixtures {
		if f.Played || f.Date < gameDate || f.opponent() == "" {
			continue
		}
		venue := "away"
		if f.HomeOwn {
			venue = "home"
		}
		if f.Neutral {
			venue = "neutral"
		}
		upcoming = append(upcoming, coachFixture{f.Date, f.Kick, f.opponent(), venue})
	}
	slices.SortStableFunc(upcoming, func(a, b coachFixture) int {
		return cmp.Or(cmp.Compare(a.Date, b.Date), cmp.Compare(a.Kick, b.Kick))
	})
	return upcoming[:min(len(upcoming), 3)]
}
