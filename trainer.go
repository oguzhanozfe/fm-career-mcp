package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type trainerClient struct {
	socket string
	club   uint32
}

func trainerRequest[T any](ctx context.Context, c trainerClient, cmd string, args map[string]any) (T, error) {
	var out T
	info, err := os.Lstat(c.socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return out, fmt.Errorf("Trainer socket unavailable; start FM from FM27 Trainer")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.socket)
	if err != nil {
		return out, fmt.Errorf("could not connect to FM27 Trainer: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if args == nil {
		args = map[string]any{}
	}
	args["cmd"] = cmd
	if err := json.NewEncoder(conn).Encode(args); err != nil {
		return out, err
	}
	var response struct {
		OK    bool
		Data  json.RawMessage
		Error string
	}
	if err := json.NewDecoder(io.LimitReader(conn, 1<<20)).Decode(&response); err != nil {
		return out, fmt.Errorf("invalid Trainer response: %w", err)
	}
	if !response.OK {
		return out, fmt.Errorf("Trainer rejected %s: %.300s", cmd, response.Error)
	}
	return out, json.Unmarshal(response.Data, &out)
}

type trainerStatus struct {
	PID      int    `json:"pid"`
	Build    string `json:"build"`
	Known    bool   `json:"known_build"`
	Platform string `json:"platform"`
}

type trainerDate struct {
	Year int `json:"year"`
	Day  int `json:"doy"`
}

func (d trainerDate) time() time.Time {
	t := time.Date(d.Year, time.January, d.Day, 0, 0, 0, 0, time.UTC)
	if d.Year < 1900 || d.Year > 2200 || t.Year() != d.Year {
		return time.Time{}
	}
	return t
}

type trainerWeek struct {
	Start trainerDate `json:"start"`
	Days  [][]uint16  `json:"days"`
}

type trainerCalendar struct {
	Today trainerDate   `json:"today"`
	Weeks []trainerWeek `json:"weeks"`
}

func (c trainerClient) calendar(ctx context.Context, writing bool) (trainerStatus, trainerCalendar, error) {
	status, err := trainerRequest[trainerStatus](ctx, c, "ping", nil)
	if err != nil {
		return status, trainerCalendar{}, err
	}
	if status.PID <= 0 || status.Platform != "macos" || writing && (!status.Known || status.Build == "") {
		return status, trainerCalendar{}, fmt.Errorf("a supported macOS Trainer session is required for this action")
	}
	club, err := trainerRequest[struct{ UID uint32 }](ctx, c, "club", nil)
	if err != nil {
		return status, trainerCalendar{}, err
	}
	if club.UID == 0 || club.UID != c.club {
		return status, trainerCalendar{}, fmt.Errorf("the loaded career is not managing the configured Trainer club")
	}
	calendar, err := trainerRequest[trainerCalendar](ctx, c, "training_weeks", nil)
	if err == nil && calendar.Today.time().IsZero() {
		err = fmt.Errorf("Trainer returned an invalid game date")
	}
	return status, calendar, err
}

type trainerDayChange struct {
	Date     string   `json:"date" jsonschema:"ISO date in the selected training week"`
	Sessions []string `json:"sessions" jsonschema:"Exactly three session names from trainer_training_calendar"`
}

type trainerInput struct {
	Week    string             `json:"week_start" jsonschema:"ISO Monday date from trainer_training_calendar"`
	Changes []trainerDayChange `json:"changes"`
}

type trainerPlan struct {
	Status   trainerStatus       `json:"-"`
	Today    trainerDate         `json:"-"`
	Before   trainerWeek         `json:"-"`
	After    trainerWeek         `json:"-"`
	Week     string              `json:"week_start"`
	Changes  []trainerDayChange  `json:"changes"`
	Previous map[string][]uint16 `json:"previous_session_codes_by_date"`
	Token    string              `json:"token,omitempty"`
}

// Selected session identifiers from FM27 Trainer's public session mapping.
var trainerSessions = map[string]uint16{
	"Passing Style": 0, "Build Up": 1, "Chance Creation": 2, "In Possession Shape": 3,
	"Line of Engagement": 4, "Pressing Intensity": 5, "Pressing Trap": 6, "Out Of Possession Shape": 7,
	"Press Avoidance": 100, "Ball Progression": 101, "Final Third": 102, "Defensive Block": 103,
	"Defensive Positioning": 104, "Defensive Engagement": 105, "Corners": 106, "Free Kicks": 107, "Penalties": 108,
	"Individual Development": 200, "Rest": 300, "Recovery": 301, "Light Conditioning": 302,
	"Conditioning": 303, "Team Bonding": 400,
}

func trainerProtected(code uint16) bool {
	return !(code <= 7 || code >= 100 && code <= 108 || code >= 200 && code <= 228 ||
		code >= 300 && code <= 307 || code == 400 || code == 401)
}

func findTrainerWeek(calendar trainerCalendar, date string) (trainerWeek, error) {
	var out trainerWeek
	for _, week := range calendar.Weeks {
		if start := week.Start.time(); start.Weekday() == time.Monday && start.Format(time.DateOnly) == date {
			if out.Days != nil || len(week.Days) != 7 {
				return trainerWeek{}, fmt.Errorf("training week is ambiguous or incomplete")
			}
			for _, day := range week.Days {
				if len(day) != 3 {
					return trainerWeek{}, fmt.Errorf("training day has an invalid session count")
				}
			}
			out = week
		}
	}
	if out.Days == nil {
		return out, fmt.Errorf("training week was not found")
	}
	return out, nil
}

func buildTrainerPlan(status trainerStatus, calendar trainerCalendar, in trainerInput) (trainerPlan, error) {
	before, err := findTrainerWeek(calendar, in.Week)
	if err != nil {
		return trainerPlan{}, err
	}
	out := trainerPlan{Status: status, Today: calendar.Today, Before: before, After: before, Week: in.Week, Previous: map[string][]uint16{}}
	out.After.Days = slices.Clone(before.Days)
	seen := [7]bool{}
	if len(in.Changes) < 1 || len(in.Changes) > 7 {
		return out, fmt.Errorf("provide 1 to 7 training days to change")
	}
	for _, change := range in.Changes {
		date, err := time.Parse(time.DateOnly, change.Date)
		offset := int(date.Sub(before.Start.time()).Hours() / 24)
		if err != nil || offset < 0 || offset >= 7 || seen[offset] || !date.After(calendar.Today.time()) || len(change.Sessions) != 3 {
			return out, fmt.Errorf("choose distinct future dates within the week with three sessions each")
		}
		seen[offset] = true
		for _, code := range before.Days[offset] {
			if trainerProtected(code) {
				return out, fmt.Errorf("cannot change a match, travel or unsupported day: %s", change.Date)
			}
		}
		var codes []uint16
		for _, name := range change.Sessions {
			code, ok := trainerSessions[strings.TrimSpace(name)]
			if !ok {
				return out, fmt.Errorf("unsupported training session %q", name)
			}
			codes = append(codes, code)
		}
		if !slices.Equal(codes, before.Days[offset]) {
			out.After.Days[offset] = codes
			out.Changes = append(out.Changes, change)
			out.Previous[change.Date] = before.Days[offset]
		}
	}
	if len(out.Changes) == 0 {
		return out, fmt.Errorf("the requested sessions already match the calendar")
	}
	return out, nil
}

func registerTrainer(ctx context.Context, server *mcp.Server, c trainerClient, autoFocus string) {
	var mu sync.Mutex
	var pending approval[trainerPlan]
	if autoFocus != "" {
		registerAutomaticTraining(ctx, server, c, &mu, autoFocus)
	}
	addTool(server, "trainer_training_calendar", "Read the configured club's current live training weeks through FM27 Trainer.", true,
		func(ctx context.Context, _ struct{}) (map[string]any, error) {
			mu.Lock()
			defer mu.Unlock()
			status, calendar, err := c.calendar(ctx, false)
			if err != nil {
				return nil, err
			}
			type weekView struct {
				Start string     `json:"week_start"`
				Days  [][]uint16 `json:"session_codes_by_day"`
			}
			weeks := []weekView{}
			for _, week := range calendar.Weeks {
				start := week.Start.time()
				if !start.IsZero() && start.AddDate(0, 0, 6).After(calendar.Today.time()) {
					weeks = append(weeks, weekView{start.Format(time.DateOnly), week.Days})
				}
			}
			return map[string]any{"game_date": calendar.Today.time().Format(time.DateOnly), "weeks": weeks, "supported_sessions": trainerSessions, "verified_build": status.Known}, nil
		})
	addTool(server, "preview_live_training", "Preview changes to future training sessions on one week. Review the dates and sessions with the user before applying.", true,
		func(ctx context.Context, in trainerInput) (trainerPlan, error) {
			mu.Lock()
			defer mu.Unlock()
			status, calendar, err := c.calendar(ctx, true)
			if err != nil {
				return trainerPlan{}, err
			}
			plan, err := buildTrainerPlan(status, calendar, in)
			if err == nil {
				plan.Token = pending.issue(plan)
			}
			return plan, err
		})
	addTool(server, "plan_live_training", "Plan the upcoming training days from the live calendar. Use the plan to preview changes or let configured automatic training apply them.", true,
		func(ctx context.Context, in trainingPlanInput) (trainingPlanReport, error) {
			mu.Lock()
			defer mu.Unlock()
			_, calendar, err := c.calendar(ctx, false)
			if err != nil {
				return trainingPlanReport{}, err
			}
			return liveTrainingPlan(calendar, in)
		})
	addTool(server, "apply_live_training", "Apply the reviewed training week and read it back. Save in FM to persist the change.", false,
		func(ctx context.Context, in tokenInput) (map[string]any, error) {
			mu.Lock()
			defer mu.Unlock()
			plan, err := pending.take(in.Token)
			if err != nil {
				return nil, err
			}
			return c.applyPlan(ctx, plan)
		})
}

func (c trainerClient) applyPlan(ctx context.Context, plan trainerPlan) (map[string]any, error) {
	status, calendar, err := c.calendar(ctx, true)
	if err != nil {
		return nil, err
	}
	before, err := findTrainerWeek(calendar, plan.Week)
	equalDays := func(a, b [][]uint16) bool { return slices.EqualFunc(a, b, slices.Equal[[]uint16]) }
	if err != nil || status != plan.Status || calendar.Today != plan.Today || !equalDays(before.Days, plan.Before.Days) {
		return nil, fmt.Errorf("the live calendar changed; preview again")
	}
	writeDays := slices.Clone(plan.After.Days)
	for i := range writeDays {
		if slices.Equal(writeDays[i], plan.Before.Days[i]) {
			// Trainer skips a requested row containing the match code.
			writeDays[i] = []uint16{500, 500, 500}
		}
	}
	written, err := trainerRequest[struct{ Days int }](ctx, c, "set_week", map[string]any{"start": plan.After.Start, "days": writeDays})
	if err != nil {
		return nil, fmt.Errorf("training write returned an error; check the game before retrying: %w", err)
	}
	afterStatus, afterCalendar, err := c.calendar(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("could not verify the write; check the game: %w", err)
	}
	after, err := findTrainerWeek(afterCalendar, plan.Week)
	if err != nil || afterStatus != status || written.Days != len(plan.Changes) || !equalDays(after.Days, plan.After.Days) {
		return nil, fmt.Errorf("training read-back differs from the approved plan; inspect FM before retrying")
	}
	return map[string]any{"applied": true, "days_changed": written.Days, "week_start": plan.Week}, nil
}
