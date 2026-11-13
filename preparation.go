package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fixtureInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"Maximum fixtures, 1 to 20; defaults to 5"`
}

type fixturePlan struct {
	Date              string `json:"date"`
	Kick              string `json:"kick_off_time,omitempty"`
	Opponent          string `json:"opponent"`
	Venue             string `json:"venue"`
	DaysSincePrevious *int   `json:"calendar_days_since_previous_match,omitempty"`
}

func fixturePlans(fixtures []savedFixture, date string, limit int) []fixturePlan {
	ordered := slices.Clone(fixtures)
	slices.SortStableFunc(ordered, func(a, b savedFixture) int {
		return cmp.Or(cmp.Compare(a.Date, b.Date), cmp.Compare(a.Kick, b.Kick))
	})
	out := []fixturePlan{}
	var previous time.Time
	for _, f := range ordered {
		day, err := time.Parse(time.DateOnly, f.Date)
		if err != nil || (!f.Played && f.Date < date) {
			continue
		}
		if !f.Played {
			row := fixturePlan{Date: f.Date, Kick: f.Kick, Opponent: f.opponent(), Venue: f.venue()}
			if !previous.IsZero() {
				gap := int(day.Sub(previous).Hours() / 24)
				row.DaysSincePrevious = &gap
			}
			out = append(out, row)
			if len(out) == limit {
				break
			}
		}
		previous = day
	}
	return out
}

type formInput struct {
	Limit int    `json:"limit,omitempty" jsonschema:"Number of played matches, 1 to 20; defaults to 5"`
	Venue string `json:"venue,omitempty" jsonschema:"Optional home, away, or neutral filter"`
}

type matchForm struct {
	Matches      []matchRecord `json:"matches"`
	Wins         int           `json:"wins"`
	Draws        int           `json:"draws"`
	Losses       int           `json:"losses"`
	GoalsFor     int           `json:"goals_for"`
	GoalsAgainst int           `json:"goals_against"`
	TeamReports  int           `json:"matches_with_team_reports"`
	ShotQuality  shotQuality   `json:"shot_quality"`
	Notes        []string      `json:"notes,omitempty"`
}

type shotQuality struct {
	XGPerShot     *float64 `json:"xg_per_shot,omitempty"`
	LongShotShare *float64 `json:"long_shot_share,omitempty"`
}

func quality(sides []matchSide) shotQuality {
	var xg float64
	var xgShots, longShots, allShots int
	for _, side := range sides {
		if value(side.Shots) <= 0 {
			continue
		}
		if side.XG != nil {
			xg += *side.XG
			xgShots += *side.Shots
		}
		if side.LongShots != nil {
			longShots += *side.LongShots
			allShots += *side.Shots
		}
	}
	var out shotQuality
	if xgShots > 0 {
		out.XGPerShot = new(xg / float64(xgShots))
	}
	if allShots > 0 {
		out.LongShotShare = new(float64(longShots) / float64(allShots))
	}
	return out
}

func recentForm(fixtures []savedFixture, stats []savedStats, limit int, venue string) matchForm {
	out := matchForm{Matches: []matchRecord{}}
	ordered := slices.Clone(fixtures)
	slices.SortStableFunc(ordered, func(a, b savedFixture) int {
		return cmp.Or(cmp.Compare(b.Date, a.Date), cmp.Compare(b.Kick, a.Kick))
	})
	var sides []matchSide
	for _, f := range ordered {
		if !f.Played || (venue != "" && f.venue() != venue) {
			continue
		}
		m := f.report(stats)
		if m.For.Goals != nil && m.Against.Goals != nil {
			out.GoalsFor += *m.For.Goals
			out.GoalsAgainst += *m.Against.Goals
			switch cmp.Compare(*m.For.Goals, *m.Against.Goals) {
			case 1:
				out.Wins++
			case 0:
				out.Draws++
			case -1:
				out.Losses++
			}
		}
		if m.Source == "saved_match_report" {
			out.TeamReports++
			sides = append(sides, m.For)
		}
		out.Matches = append(out.Matches, m)
		if len(out.Matches) == limit {
			break
		}
	}
	out.ShotQuality = quality(sides)
	return out
}

type compareMatchesInput struct {
	Baseline string `json:"baseline_date" jsonschema:"YYYY-MM-DD of the baseline match"`
	FollowUp string `json:"follow_up_date" jsonschema:"YYYY-MM-DD of the later match"`
}

type matchComparison struct {
	Baseline        matchRecord `json:"baseline"`
	FollowUp        matchRecord `json:"follow_up"`
	BaselineQuality shotQuality `json:"baseline_shot_quality"`
	FollowUpQuality shotQuality `json:"follow_up_shot_quality"`
	XGDelta         *float64    `json:"xg_delta,omitempty"`
	OpponentXGDelta *float64    `json:"opponent_xg_delta,omitempty"`
	Note            string      `json:"note"`
}

func compareMatches(fixtures []savedFixture, stats []savedStats, in compareMatchesInput) (matchComparison, error) {
	var out matchComparison
	for _, date := range []string{in.Baseline, in.FollowUp} {
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			return out, fmt.Errorf("use YYYY-MM-DD for both dates")
		}
	}
	if in.FollowUp <= in.Baseline {
		return out, fmt.Errorf("follow-up must be later than baseline")
	}
	for _, target := range []struct {
		date   string
		result *matchRecord
	}{{in.Baseline, &out.Baseline}, {in.FollowUp, &out.FollowUp}} {
		for _, f := range fixtures {
			if !f.Played || f.Date != target.date {
				continue
			}
			if target.result.Date != "" {
				return out, fmt.Errorf("more than one played fixture on %s", target.date)
			}
			*target.result = f.report(stats)
		}
		if target.result.Date == "" {
			return out, fmt.Errorf("no played fixture on %s", target.date)
		}
	}
	out.BaselineQuality = quality([]matchSide{out.Baseline.For})
	out.FollowUpQuality = quality([]matchSide{out.FollowUp.For})
	if a, b := out.Baseline.For.XG, out.FollowUp.For.XG; a != nil && b != nil {
		out.XGDelta = new(*b - *a)
	}
	if a, b := out.Baseline.Against.XG, out.FollowUp.Against.XG; a != nil && b != nil {
		out.OpponentXGDelta = new(*b - *a)
	}
	out.Note = "Treat changes as descriptive. Opponent, lineup and game state can also affect the figures."
	return out, nil
}

type savedSelection struct {
	Club       uint32 `json:"club_uid"`
	TeamSlot   *int   `json:"team_slot"`
	Substitute bool   `json:"substitute"`
	Slot       int    `json:"slot"`
	UID        uint32 `json:"player_uid,omitempty"`
	Name       string `json:"player_name,omitempty"`
}

type selectionReport struct {
	Starters          []savedSelection `json:"starters"`
	Bench             []savedSelection `json:"bench"`
	EmptyStarterSlots []int            `json:"empty_starter_slots"`
	Note              string           `json:"note"`
}

func savedXI(rows []savedSelection, club uint32) selectionReport {
	out := selectionReport{Starters: []savedSelection{}, Bench: []savedSelection{}, EmptyStarterSlots: []int{}, Note: "These are stored picks. Confirm eligibility and availability in FM."}
	var filled [11]bool
	for _, row := range rows {
		if row.Club != club || row.TeamSlot == nil || *row.TeamSlot != 0 || row.Slot < 0 {
			continue
		}
		if row.Substitute {
			out.Bench = append(out.Bench, row)
		} else if row.Slot < len(filled) {
			out.Starters = append(out.Starters, row)
			filled[row.Slot] = true
		}
	}
	for slot, present := range filled {
		if !present {
			out.EmptyStarterSlots = append(out.EmptyStarterSlots, slot)
		}
	}
	slices.SortFunc(out.Starters, func(a, b savedSelection) int { return cmp.Compare(a.Slot, b.Slot) })
	slices.SortFunc(out.Bench, func(a, b savedSelection) int { return cmp.Compare(a.Slot, b.Slot) })
	return out
}

type trainingInput struct {
	Days int `json:"days,omitempty" jsonschema:"Days from the in-game date, 1 to 28; defaults to 7"`
}

type trainingDay struct {
	Day      string    `json:"day"`
	Sessions []*string `json:"sessions"`
}

type savedTraining struct {
	Club     uint32 `json:"club_uid"`
	TeamSlot *int   `json:"team_slot"`
	Weeks    []struct {
		Days []trainingDay `json:"days"`
	} `json:"weeks"`
}

func trainingDays(rows []savedTraining, club uint32, date time.Time, days int) []trainingDay {
	out := []trainingDay{}
	end := date.AddDate(0, 0, days).Format(time.DateOnly)
	for _, row := range rows {
		if row.Club != club || row.TeamSlot == nil || *row.TeamSlot != 0 {
			continue
		}
		for _, week := range row.Weeks {
			for _, day := range week.Days {
				if day.Day >= date.Format(time.DateOnly) && day.Day < end {
					out = append(out, day)
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b trainingDay) int { return cmp.Compare(a.Day, b.Day) })
	return out
}

type contractInput struct {
	Days int `json:"days,omitempty" jsonschema:"Days from the in-game date, 1 to 730; defaults to 365"`
}

type savedContract struct {
	UID       uint32 `json:"player_uid"`
	Name      string `json:"player_name"`
	Club      uint32 `json:"club_uid"`
	Ends      string `json:"end"`
	EndSource string `json:"end_source,omitempty"`
	OnLoan    bool   `json:"on_loan"`
}

func expiringContracts(rows []savedContract, club uint32, date time.Time, days int) []savedContract {
	out := []savedContract{}
	end := date.AddDate(0, 0, days).Format(time.DateOnly)
	for _, row := range rows {
		if row.Club == club && row.UID != 0 && !row.OnLoan && row.EndSource == "contract" && row.Ends >= date.Format(time.DateOnly) && row.Ends <= end {
			out = append(out, row)
		}
	}
	slices.SortFunc(out, func(a, b savedContract) int { return cmp.Or(cmp.Compare(a.Ends, b.Ends), cmp.Compare(a.Name, b.Name)) })
	return out
}

type financeMonth struct {
	Club              uint32 `json:"club_uid"`
	Month             string `json:"month"`
	Balance           *int64 `json:"balance,omitempty"`
	TransferRemaining *int64 `json:"transfer_budget_remaining,omitempty"`
	WageBudget        *int64 `json:"wage_budget_weekly,omitempty"`
	Payroll           *int64 `json:"wage_payroll_weekly,omitempty"`
}

func bounded(n, fallback, max int) (int, error) {
	if n == 0 {
		return fallback, nil
	}
	if n < 1 || n > max {
		return 0, fmt.Errorf("expected a number from 1 to %d", max)
	}
	return n, nil
}

// Each tool uses one completed save revision for every export it joins.
func addSaveTool[In, Out any](server *mcp.Server, src matchSource, name, description string, run func(context.Context, matchSource, managedClub, In) (Out, error)) {
	addTool(server, name, description, true, func(ctx context.Context, in In) (Out, error) {
		var empty Out
		stable, cleanup, err := src.snapshot()
		if err != nil {
			return empty, err
		}
		defer cleanup()
		club, err := stable.managedClub(ctx)
		if err != nil {
			return empty, err
		}
		return run(ctx, stable, club, in)
	})
}

func (s matchSource) gameDate(ctx context.Context) (time.Time, error) {
	info, err := s.saveInfo(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.DateOnly, info.Date)
}

type savedMetadata struct {
	Date string `json:"game_date,omitempty"`
	Game string `json:"game,omitempty"`
}

func (s matchSource) saveInfo(ctx context.Context) (savedMetadata, error) {
	var info savedMetadata
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, s.cli, "info", s.save, "--json").Output()
	if err != nil {
		return info, fmt.Errorf("could not read save metadata: %w", err)
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return info, err
	}
	return info, nil
}

func registerPreparationTools(server *mcp.Server, src matchSource) {
	registerPerformance(server, src)
	registerTrainingPlan(server, src)
	addSaveTool(server, src, "career_turn", "Read the next fixture, saved XI, latest match review and fixture-aware training plan in one call from a single save revision.",
		func(ctx context.Context, s matchSource, club managedClub, in trainingPlanInput) (map[string]any, error) {
			brief, err := fairCoachBrief(ctx, s, club, struct{}{})
			if err != nil {
				return nil, err
			}
			plan, err := savedTrainingPlan(ctx, s, club, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"brief": brief, "training": plan, "next_step": "Review the suggested schedule and check the starting XI. This save connection cannot advance the game."}, nil
		})
	addSaveTool(server, src, "fixture_plan", "List upcoming first-team fixtures with calendar-day gaps for rotation planning.",
		func(ctx context.Context, s matchSource, c managedClub, in fixtureInput) (map[string]any, error) {
			n, err := bounded(in.Limit, 5, 20)
			if err != nil {
				return nil, err
			}
			fixtures, err := s.clubFixtures(ctx, c.UID)
			if err != nil {
				return nil, err
			}
			date, err := s.gameDate(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]any{"fixtures": fixturePlans(fixtures, date.Format(time.DateOnly), n)}, nil
		})
	addSaveTool(server, src, "recent_form", "Review recent results and shot-quality trends with match-report coverage.",
		func(ctx context.Context, s matchSource, c managedClub, in formInput) (matchForm, error) {
			n, err := bounded(in.Limit, 5, 20)
			if err != nil {
				return matchForm{}, err
			}
			if in.Venue != "" && !slices.Contains([]string{"home", "away", "neutral"}, in.Venue) {
				return matchForm{}, fmt.Errorf("venue must be home, away, or neutral")
			}
			fixtures, err := s.clubFixtures(ctx, c.UID)
			if err != nil {
				return matchForm{}, err
			}
			stats, statsErr := exportRows[savedStats](ctx, s, "team-match-stats")
			out := recentForm(fixtures, stats, n, in.Venue)
			if statsErr != nil || out.TeamReports < len(out.Matches) {
				out.Notes = append(out.Notes, "Some matches lack team reports. Shot quality uses the available metrics only.")
			}
			return out, nil
		})
	addSaveTool(server, src, "compare_matches", "Compare two played matches before and after a tactical experiment.",
		func(ctx context.Context, s matchSource, c managedClub, in compareMatchesInput) (matchComparison, error) {
			fixtures, err := s.clubFixtures(ctx, c.UID)
			if err != nil {
				return matchComparison{}, err
			}
			stats, _ := exportRows[savedStats](ctx, s, "team-match-stats")
			return compareMatches(fixtures, stats, in)
		})
	addSaveTool(server, src, "saved_lineup", "Read saved first-team starters, bench and empty starter slots.",
		func(ctx context.Context, s matchSource, c managedClub, _ struct{}) (selectionReport, error) {
			rows, err := exportRows[savedSelection](ctx, s, "selections")
			if err != nil {
				return selectionReport{}, err
			}
			return savedXI(rows, c.UID), nil
		})
	addSaveTool(server, src, "training_calendar", "Read the saved first-team training sessions for the coming days.",
		func(ctx context.Context, s matchSource, c managedClub, in trainingInput) (map[string]any, error) {
			n, err := bounded(in.Days, 7, 28)
			if err != nil {
				return nil, err
			}
			date, err := s.gameDate(ctx)
			if err != nil {
				return nil, err
			}
			rows, err := exportRows[savedTraining](ctx, s, "training")
			if err != nil {
				return nil, err
			}
			return map[string]any{"days": trainingDays(rows, c.UID, date, n), "note": "Days outside the stored calendar may be absent."}, nil
		})
	addSaveTool(server, src, "contract_watch", "List permanent player contracts ending within a chosen number of days.",
		func(ctx context.Context, s matchSource, c managedClub, in contractInput) (map[string]any, error) {
			n, err := bounded(in.Days, 365, 730)
			if err != nil {
				return nil, err
			}
			date, err := s.gameDate(ctx)
			if err != nil {
				return nil, err
			}
			rows, err := exportRows[savedContract](ctx, s, "contracts")
			if err != nil {
				return nil, err
			}
			return map[string]any{"contracts": expiringContracts(rows, c.UID, date, n), "note": "Check extension options in FM before making decisions."}, nil
		})
	if src.fair {
		return
	}
	addSaveTool(server, src, "finance_history", "Read historical monthly transfer budget, payroll and wage budget for the managed club.",
		func(ctx context.Context, s matchSource, c managedClub, in fixtureInput) (map[string]any, error) {
			n, err := bounded(in.Limit, 3, 20)
			if err != nil {
				return nil, err
			}
			rows, err := exportRows[financeMonth](ctx, s, "finances")
			if err != nil {
				return nil, err
			}
			rows = slices.DeleteFunc(rows, func(r financeMonth) bool { return r.Club != c.UID || r.Month == "" })
			slices.SortStableFunc(rows, func(a, b financeMonth) int { return strings.Compare(b.Month, a.Month) })
			return map[string]any{"months": rows[:min(n, len(rows))], "note": "Historical snapshots in save units. Verify today's available budget and display currency in FM."}, nil
		})
}
