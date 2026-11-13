package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type trainingPlanInput struct {
	Days  int    `json:"days,omitempty" jsonschema:"Upcoming days, 1 to 14; defaults to 7"`
	Focus string `json:"focus,omitempty" jsonschema:"balanced, build-up, chance-creation, or pressing"`
}

type plannedTrainingDay struct {
	Date     string   `json:"date"`
	Reason   string   `json:"reason"`
	Sessions []string `json:"sessions,omitempty"`
}

type trainingPlanReport struct {
	GameDate string               `json:"game_date"`
	Focus    string               `json:"focus"`
	Days     []plannedTrainingDay `json:"days"`
}

func trainingFocus(input string) (string, string, string, error) {
	switch input {
	case "", "balanced":
		return "balanced", "In Possession Shape", "Defensive Positioning", nil
	case "build-up":
		return input, "Build Up", "Press Avoidance", nil
	case "chance-creation":
		return input, "Chance Creation", "Final Third", nil
	case "pressing":
		return input, "Pressing Intensity", "Defensive Engagement", nil
	default:
		return "", "", "", fmt.Errorf("choose balanced, build-up, chance-creation, or pressing")
	}
}

func trainingWeekStart(day time.Time) time.Time {
	return day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
}

func planTeamTraining(today time.Time, in trainingPlanInput, matches, protected, completedPreparation map[string]bool) (trainingPlanReport, error) {
	out := trainingPlanReport{GameDate: today.Format(time.DateOnly), Days: []plannedTrainingDay{}}
	n, err := bounded(in.Days, 7, 14)
	if err != nil {
		return out, err
	}
	focus, development, preparation, err := trainingFocus(in.Focus)
	if err != nil {
		return out, err
	}
	out.Focus = focus
	// Match preparation sessions are limited to one per calendar week in FM27.
	prepared := make(map[string]bool)
	for week := range completedPreparation {
		prepared[week] = true
	}
	for offset := 1; offset <= n; offset++ {
		day := today.AddDate(0, 0, offset)
		date := day.Format(time.DateOnly)
		prior := matches[day.AddDate(0, 0, -1).Format(time.DateOnly)]
		next := matches[day.AddDate(0, 0, 1).Format(time.DateOnly)]
		week := trainingWeekStart(day).Format(time.DateOnly)
		row := plannedTrainingDay{Date: date}
		switch {
		case matches[date]:
			row.Reason = "match_day"
		case protected[date]:
			row.Reason = "protected_calendar_day"
		case prior:
			row.Reason = "post_match_recovery"
			row.Sessions = []string{"Recovery", "Rest", "Rest"}
		case next && !prepared[week]:
			row.Reason = "pre_match_preparation"
			row.Sessions = []string{preparation, "Rest", "Rest"}
			prepared[week] = true
		case next:
			row.Reason = "pre_match_light_load"
			row.Sessions = []string{development, "Rest", "Rest"}
		default:
			row.Reason = "tactical_development"
			row.Sessions = []string{development, "Individual Development", "Rest"}
		}
		out.Days = append(out.Days, row)
	}
	return out, nil
}

func registerTrainingPlan(server *mcp.Server, src matchSource) {
	addSaveTool(server, src, "plan_team_training", "Plan the first team's next 1 to 14 days around the saved fixtures and travel, using a chosen tactical focus.",
		func(ctx context.Context, s matchSource, club managedClub, in trainingPlanInput) (trainingPlanReport, error) {
			today, err := s.gameDate(ctx)
			if err != nil {
				return trainingPlanReport{}, err
			}
			fixtures, err := s.clubFixtures(ctx, club.UID)
			if err != nil {
				return trainingPlanReport{}, err
			}
			rows, err := exportRows[savedTraining](ctx, s, "training")
			if err != nil {
				return trainingPlanReport{}, err
			}
			matches, protected, prepared := map[string]bool{}, map[string]bool{}, map[string]bool{}
			_, _, prep, err := trainingFocus(in.Focus)
			if err != nil {
				return trainingPlanReport{}, err
			}
			prepName := strings.ToLower(strings.ReplaceAll(prep, " ", "_"))
			for _, f := range fixtures {
				matches[f.Date] = true
			}
			start := trainingWeekStart(today)
			for _, day := range trainingDays(rows, club.UID, start, 22) {
				for _, session := range day.Sessions {
					if session != nil {
						if *session == "match" {
							matches[day.Day] = true
						}
						if *session == "travel" {
							protected[day.Day] = true
						}
						if day.Day <= today.Format(time.DateOnly) && *session == prepName {
							prepared[start.Format(time.DateOnly)] = true
						}
					}
				}
			}
			return planTeamTraining(today, in, matches, protected, prepared)
		})
}

func liveTrainingPlan(calendar trainerCalendar, in trainingPlanInput) (trainingPlanReport, error) {
	matches, protected, prepared := map[string]bool{}, map[string]bool{}, map[string]bool{}
	today := calendar.Today.time()
	_, _, prep, err := trainingFocus(in.Focus)
	if err != nil {
		return trainingPlanReport{}, err
	}
	for offset := 1; offset <= 14; offset++ {
		protected[today.AddDate(0, 0, offset).Format(time.DateOnly)] = true
	}
	for _, week := range calendar.Weeks {
		start := week.Start.time()
		if start.IsZero() || start.Weekday() != time.Monday {
			continue
		}
		valid, err := findTrainerWeek(calendar, start.Format(time.DateOnly))
		if err != nil {
			return trainingPlanReport{}, err
		}
		allRest := true
		for _, day := range valid.Days {
			allRest = allRest && slices.Equal(day, []uint16{300, 300, 300})
		}
		for i, day := range valid.Days {
			at := start.AddDate(0, 0, i)
			date := at.Format(time.DateOnly)
			matches[date] = slices.Contains(day, uint16(500))
			protected[date] = allRest || slices.ContainsFunc(day, trainerProtected)
			if !at.After(today) && slices.Contains(day, trainerSessions[prep]) {
				prepared[start.Format(time.DateOnly)] = true
			}
		}
	}
	return planTeamTraining(today, in, matches, protected, prepared)
}
