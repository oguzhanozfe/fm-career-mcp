package main

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type automaticTrainingStatus struct {
	Focus       string `json:"focus"`
	State       string `json:"state"`
	CheckedAt   string `json:"checked_at,omitempty"`
	GameDate    string `json:"game_date,omitempty"`
	DaysChanged int    `json:"days_changed_since_start"`
	Detail      string `json:"detail,omitempty"`
	session     trainerStatus
	lastDate    time.Time
}

func nextTrainingChange(status trainerStatus, calendar trainerCalendar, focus string) (*trainerPlan, error) {
	report, err := liveTrainingPlan(calendar, trainingPlanInput{Days: 14, Focus: focus})
	if err != nil {
		return nil, err
	}
	var week trainerInput
	for _, row := range report.Days {
		if len(row.Sessions) == 0 {
			continue
		}
		day, _ := time.Parse(time.DateOnly, row.Date)
		monday := trainingWeekStart(day).Format(time.DateOnly)
		if week.Week != monday {
			if len(week.Changes) > 0 {
				break
			}
			week = trainerInput{Week: monday}
		}
		current, err := findTrainerWeek(calendar, monday)
		if err != nil {
			return nil, err
		}
		offset := (int(day.Weekday()) + 6) % 7
		var codes []uint16
		for _, name := range row.Sessions {
			codes = append(codes, trainerSessions[name])
		}
		if !slices.Equal(codes, current.Days[offset]) {
			week.Changes = append(week.Changes, trainerDayChange{Date: row.Date, Sessions: row.Sessions})
		}
	}
	if len(week.Changes) == 0 {
		return nil, nil
	}
	plan, err := buildTrainerPlan(status, calendar, week)
	return &plan, err
}

func automaticTrainingTick(ctx context.Context, c trainerClient, state *automaticTrainingStatus) {
	state.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	status, calendar, err := c.calendar(ctx, true)
	if err != nil {
		state.State, state.Detail = "waiting", err.Error()
		return
	}
	today := calendar.Today.time()
	if state.session.PID != 0 && (state.session != status || today.Before(state.lastDate)) {
		state.State, state.Detail = "paused", "The FM session changed or its date moved backwards. Restart automatic training for the loaded career."
		return
	}
	state.session, state.lastDate = status, today
	state.GameDate = today.Format(time.DateOnly)
	plan, err := nextTrainingChange(status, calendar, state.Focus)
	if err != nil {
		state.State, state.Detail = "waiting", err.Error()
		return
	}
	if plan == nil {
		state.State, state.Detail = "up_to_date", ""
		return
	}
	if _, err := c.applyPlan(ctx, *plan); err != nil {
		state.State, state.Detail = "paused", fmt.Sprintf("Check the live calendar before restarting automatic training: %v", err)
		return
	}
	state.State, state.Detail = "applied", plan.Week
	state.DaysChanged += len(plan.Changes)
}

func registerAutomaticTraining(ctx context.Context, server *mcp.Server, c trainerClient, mu *sync.Mutex, focus string) {
	state := automaticTrainingStatus{Focus: focus, State: "waiting"}
	addTool(server, "automatic_training_status", "Show automatic training's latest check, changes and any reason it is waiting or paused.", true,
		func(_ context.Context, _ struct{}) (automaticTrainingStatus, error) {
			mu.Lock()
			defer mu.Unlock()
			return state, nil
		})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for ctx.Err() == nil {
			mu.Lock()
			automaticTrainingTick(ctx, c, &state)
			paused := state.State == "paused"
			mu.Unlock()
			if paused {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
