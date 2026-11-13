package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type gameAPI struct {
	base, club string
	http       *http.Client
}

func newGameAPI(port uint, club string) *gameAPI {
	return &gameAPI{
		base: "http://127.0.0.1:" + strconv.FormatUint(uint64(port), 10) + "/api/v1/",
		club: fold(club),
		http: &http.Client{
			Timeout:   60 * time.Second,
			Transport: &http.Transport{},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (g *gameAPI) request(ctx context.Context, method, endpoint string, body, out any) error {
	var input io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.base+endpoint, input)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("FM27 bridge request failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("FM27 bridge returned HTTP %d for %s", res.StatusCode, endpoint)
	}
	const responseLimit = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(res.Body, responseLimit+1))
	if err != nil || len(raw) > responseLimit {
		return fmt.Errorf("FM27 bridge response could not be read")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("invalid FM27 bridge response: %w", err)
	}
	return nil
}

type liveSummary struct {
	Date, Competition, Venue, Opponent, Formation      string
	GoalsFor, GoalsAgainst, ShotsFor, ShotsAgainst     *int
	ShotsOnTargetFor, ShotsOnTargetAgainst, Possession *int
	ExpectedGoalsFor, ExpectedGoalsAgainst             *float64
}

type liveTeamStats struct {
	ExpectedGoals, NonPenaltyExpectedGoals                *float64
	Possession, Shots, ShotsOnTarget, LongShots, RedCards *int
}

type liveMatchDetail struct {
	Summary                  liveSummary
	TeamStats, OpponentStats liveTeamStats
}

type liveSeason struct {
	TeamName string
	Played   []liveSummary
}

func (g *gameAPI) matchDetail(ctx context.Context, in matchInput) (json.RawMessage, error) {
	var season liveSeason
	if err := g.request(ctx, http.MethodGet, "matches?includeFriendlies=true", nil, &season); err != nil {
		return nil, err
	}
	var found []liveSummary
	for _, m := range season.Played {
		if (in.Date == "" || in.Date == m.Date) && strings.Contains(fold(m.Opponent), fold(in.Opponent)) {
			found = append(found, m)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no matching played match in the live career")
	}
	slices.SortFunc(found, func(a, b liveSummary) int { return cmp.Compare(b.Date, a.Date) })
	date := found[0].Date
	sameDate := 0
	for _, m := range season.Played {
		if m.Date == date {
			sameDate++
		}
	}
	if sameDate != 1 {
		return nil, fmt.Errorf("the live provider cannot distinguish multiple matches on the same date")
	}
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return nil, fmt.Errorf("the live provider returned an invalid match date")
	}
	var raw json.RawMessage
	if err := g.request(ctx, http.MethodGet, "matches/"+date, nil, &raw); err != nil {
		return nil, err
	}
	var detail liveMatchDetail
	if err := json.Unmarshal(raw, &detail); err != nil || detail.Summary.Date != date ||
		fold(detail.Summary.Opponent) != fold(found[0].Opponent) {
		return nil, fmt.Errorf("live match changed while being read")
	}
	return raw, nil
}

func (g *gameAPI) match(ctx context.Context, in matchInput) (matchRecord, error) {
	raw, err := g.matchDetail(ctx, in)
	if err != nil {
		return matchRecord{}, err
	}
	var d liveMatchDetail
	if err := json.Unmarshal(raw, &d); err != nil {
		return matchRecord{}, err
	}
	s := d.Summary
	ours, theirs := d.TeamStats, d.OpponentStats
	return matchRecord{
		Date: s.Date, Competition: s.Competition, Venue: s.Venue, Opponent: s.Opponent, Formation: s.Formation, Source: "live_fm27_bridge",
		For: matchSide{s.GoalsFor, first(ours.ExpectedGoals, s.ExpectedGoalsFor), ours.NonPenaltyExpectedGoals,
			first(ours.Possession, s.Possession), first(ours.Shots, s.ShotsFor), first(ours.ShotsOnTarget, s.ShotsOnTargetFor), ours.LongShots, ours.RedCards},
		Against: matchSide{s.GoalsAgainst, first(theirs.ExpectedGoals, s.ExpectedGoalsAgainst), theirs.NonPenaltyExpectedGoals,
			theirs.Possession, first(theirs.Shots, s.ShotsAgainst), first(theirs.ShotsOnTarget, s.ShotsOnTargetAgainst), theirs.LongShots, theirs.RedCards},
	}, nil
}

func first[T any](a, b *T) *T {
	if a == nil {
		return b
	}
	return a
}

type phaseKeys struct {
	In  string `json:"inPossession,omitempty"`
	Out string `json:"outOfPossession,omitempty"`
}

type liveLineup struct {
	PlayerID int        `json:"playerId"`
	Position *phaseKeys `json:"position,omitempty"`
	Role     *phaseKeys `json:"role,omitempty"`
}

type liveTacticInput struct {
	Slot         int               `json:"slot" jsonschema:"Existing tactic slot from 1 to 3; applying makes it active"`
	Mentality    string            `json:"mentality,omitempty" jsonschema:"Exact game key from live_tactic_options"`
	Instructions map[string]string `json:"teamInstructions,omitempty" jsonschema:"Exact team instruction and option keys from live_tactic_options"`
	Formation    *phaseKeys        `json:"formation,omitempty"`
	Lineup       []liveLineup      `json:"lineup,omitempty" jsonschema:"FM live player IDs and position or role keys; include all 11 to change selections"`
	Bench        []int             `json:"bench,omitempty" jsonschema:"FM live player IDs in the intended substitute order"`
}

type trialInput struct {
	PlayerID int `json:"player_id" jsonschema:"Player ID from find_live_recruits, not the save parser"`
	Weeks    int `json:"weeks" jsonschema:"Trial duration of 1 to 4 weeks"`
}

type scoutActionInput struct {
	PlayerID int    `json:"player_id" jsonschema:"Live player ID from the FM27 bridge"`
	Action   string `json:"action" jsonschema:"scout or shortlist"`
}

type liveWrite struct {
	endpoint, stateEndpoint string
	body                    any
	target                  int
	state                   [32]byte
	dryRun                  [32]byte
}

type bridgePreview struct {
	Token string         `json:"token,omitempty"`
	Plan  map[string]any `json:"plan"`
}

type bridgeResult struct {
	Applied bool           `json:"applied"`
	Result  map[string]any `json:"game_result"`
	Note    string         `json:"note,omitempty"`
}

func (g *gameAPI) liveState(ctx context.Context, endpoint string) ([32]byte, error) {
	var season json.RawMessage
	if err := g.request(ctx, http.MethodGet, "matches?includeFriendlies=true", nil, &season); err != nil {
		return [32]byte{}, err
	}
	var identity liveSeason
	if err := json.Unmarshal(season, &identity); err != nil || g.club == "" || fold(identity.TeamName) != g.club {
		return [32]byte{}, fmt.Errorf("the running career is not managing the configured game club")
	}
	var state json.RawMessage
	if err := g.request(ctx, http.MethodGet, endpoint, nil, &state); err != nil {
		return [32]byte{}, err
	}
	return bridgeFingerprint(season, state)
}

// JSON member ordering does not change the approved plan or game state.
func bridgeFingerprint(messages ...json.RawMessage) ([32]byte, error) {
	values := make([]any, 0, len(messages))
	for _, message := range messages {
		decoder := json.NewDecoder(bytes.NewReader(message))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return [32]byte{}, err
		}
		values = append(values, value)
	}
	canonical, err := json.Marshal(values)
	return sha256.Sum256(canonical), err
}

type liveWriteResponse struct {
	Applied               bool
	Slot, Count, PlayerID int
	ConfirmationIsWarning bool
	Steps                 []struct{ Status string }
	Players               []struct{ PlayerID int }
}

func (r liveWriteResponse) matches(plan liveWrite, status string) bool {
	if r.ConfirmationIsWarning {
		return false
	}
	switch plan.endpoint {
	case "trials":
		return r.Count == 1 && len(r.Players) == 1 && r.Players[0].PlayerID == plan.target
	case "tactics":
		if r.Slot != plan.target || len(r.Steps) == 0 {
			return false
		}
		for _, step := range r.Steps {
			if !strings.EqualFold(step.Status, status) {
				return false
			}
		}
		return true
	default:
		return r.PlayerID == plan.target
	}
}

func (g *gameAPI) dryRun(ctx context.Context, plan liveWrite) (json.RawMessage, error) {
	var raw json.RawMessage
	err := g.request(ctx, http.MethodPost, plan.endpoint+"?apply=false", plan.body, &raw)
	return raw, err
}

func (g *gameAPI) apply(ctx context.Context, plan liveWrite) (bridgeResult, error) {
	state, err := g.liveState(ctx, plan.stateEndpoint)
	if err != nil {
		return bridgeResult{}, err
	}
	if state != plan.state {
		return bridgeResult{}, fmt.Errorf("the game changed after the preview; preview again")
	}
	dry, err := g.dryRun(ctx, plan)
	if err != nil {
		return bridgeResult{}, err
	}
	fingerprint, err := bridgeFingerprint(dry)
	if err != nil || fingerprint != plan.dryRun {
		return bridgeResult{}, fmt.Errorf("the game reports a different plan; preview again")
	}
	state, err = g.liveState(ctx, plan.stateEndpoint)
	if err != nil {
		return bridgeResult{}, err
	}
	if state != plan.state {
		return bridgeResult{}, fmt.Errorf("the game changed during the final check; preview again")
	}
	var raw json.RawMessage
	if err := g.request(ctx, http.MethodPost, plan.endpoint+"?apply=true", plan.body, &raw); err != nil {
		return bridgeResult{}, fmt.Errorf("apply did not return a result; check the game before retrying: %w", err)
	}
	var result liveWriteResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return bridgeResult{}, fmt.Errorf("check the game; apply returned an unreadable result")
	}
	success := result.Applied && result.matches(plan, "Done")
	details, err := jsonObject(raw)
	if err != nil {
		return bridgeResult{}, err
	}
	out := bridgeResult{Applied: success, Result: details}
	if !success {
		out.Note = "The game did not report full success. Inspect completed, failed or skipped steps before retrying."
	}
	return out, nil
}

type scoutReportInput struct {
	Source string `json:"source,omitempty" jsonschema:"recommendations, shortlist, or search; defaults to recommendations"`
}

type liveSearch struct {
	Name string `json:"name" jsonschema:"Player or staff name as used in the in-game search"`
	Kind string `json:"kind" jsonschema:"player or staff"`
}

func jsonObject(raw json.RawMessage) (map[string]any, error) {
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return nil, fmt.Errorf("expected an object from the FM27 bridge")
	}
	return out, nil
}

func registerGameAPI(server *mcp.Server, g *gameAPI) {
	addTool(server, "live_bridge_status", "Check whether the configured local FootballManagerMCP port can read the current squad.", true,
		func(ctx context.Context, _ struct{}) (map[string]any, error) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			var squad struct{ TeamName string }
			if err := g.request(ctx, http.MethodGet, "squad", nil, &squad); err != nil {
				return map[string]any{"connected": false, "detail": err.Error()}, nil
			}
			if strings.TrimSpace(squad.TeamName) == "" {
				return map[string]any{"connected": false, "detail": "The bridge did not identify a loaded squad."}, nil
			}
			return map[string]any{"connected": true, "team": squad.TeamName, "writes_configured_for_current_team": g.club != "" && fold(squad.TeamName) == g.club}, nil
		})
	registerLiveReads(server, g)
	addTool(server, "live_match_detail", "Read both teams' stats, goals, substitutions and lineup for a played match from FootballManagerMCP.", true,
		func(ctx context.Context, in matchInput) (map[string]any, error) {
			raw, err := g.matchDetail(ctx, in)
			if err != nil {
				return nil, err
			}
			return jsonObject(raw)
		})
	for _, entry := range []struct{ name, path, description string }{
		{"live_tactic_options", "tactics/options", "Get the game's current valid tactic, shape, instruction and role keys."},
		{"live_tactics", "tactics", "Read all live tactic slots and the active tactic."},
		{"live_squad", "squad", "Read the live first-team squad and player IDs."},
		{"live_scouting", "scouting", "Read current scouts, focuses and scouting budget from the running game."},
	} {
		addTool(server, entry.name, entry.description, true, func(ctx context.Context, _ struct{}) (map[string]any, error) {
			var out map[string]any
			err := g.request(ctx, http.MethodGet, entry.path, nil, &out)
			return out, err
		})
	}
	addTool(server, "find_live_recruits", "Search Football Manager's live database by name for a player or staff member. This opens the in-game search.", false,
		func(ctx context.Context, in liveSearch) (map[string]any, error) {
			name := strings.TrimSpace(in.Name)
			if len(name) == 0 || len(name) > 150 || in.Kind != "player" && in.Kind != "staff" {
				return nil, fmt.Errorf("enter a name and choose player or staff")
			}
			kind := map[string]string{"player": "Player", "staff": "Staff"}[in.Kind]
			query := url.Values{"query": {name}, "kinds": {kind}, "limit": {"15"}}
			var out map[string]any
			err := g.request(ctx, http.MethodGet, "database/search?"+query.Encode(), nil, &out)
			return out, err
		})

	addTool(server, "live_scout_reports", "Read player reports from the game's scouting recommendations, shortlist or current search, including fee, wage demands and interest where known.", true,
		func(ctx context.Context, in scoutReportInput) (map[string]any, error) {
			sources := map[string]string{"": "Recommendations", "recommendations": "Recommendations", "shortlist": "Shortlist", "search": "Search"}
			source, ok := sources[in.Source]
			if !ok {
				return nil, fmt.Errorf("source must be recommendations, shortlist, or search")
			}
			query := url.Values{"source": {source}, "includeReport": {"true"}, "limit": {"15"}}
			var out map[string]any
			err := g.request(ctx, http.MethodGet, "scouting/players?"+query.Encode(), nil, &out)
			return out, err
		})

	if g.club == "" {
		return
	}
	var mu sync.Mutex
	var pending approval[liveWrite]
	preview := func(ctx context.Context, plan liveWrite) (bridgePreview, error) {
		mu.Lock()
		defer mu.Unlock()
		pending = approval[liveWrite]{}
		state, err := g.liveState(ctx, plan.stateEndpoint)
		if err != nil {
			return bridgePreview{}, err
		}
		dry, err := g.dryRun(ctx, plan)
		if err != nil {
			return bridgePreview{}, err
		}
		var response liveWriteResponse
		if err := json.Unmarshal(dry, &response); err != nil || response.Applied {
			return bridgePreview{}, fmt.Errorf("bridge did not return a valid dry run")
		}
		details, err := jsonObject(dry)
		if err != nil {
			return bridgePreview{}, err
		}
		if !response.matches(plan, "Planned") {
			return bridgePreview{Plan: details}, nil
		}
		fingerprint, err := bridgeFingerprint(dry)
		if err != nil {
			return bridgePreview{}, err
		}
		plan.state, plan.dryRun = state, fingerprint
		return bridgePreview{Token: pending.issue(plan), Plan: details}, nil
	}
	addTool(server, "preview_live_tactic", "Validate a tactic change in the game without applying it. Show the plan and ask the user before applying.", true,
		func(ctx context.Context, in liveTacticInput) (bridgePreview, error) {
			if in.Slot < 1 || in.Slot > 3 || len(in.Lineup) > 11 || len(in.Bench) > 15 ||
				in.Mentality == "" && len(in.Instructions) == 0 && in.Formation == nil && len(in.Lineup) == 0 && len(in.Bench) == 0 {
				return bridgePreview{}, fmt.Errorf("provide a slot from 1 to 3 and at least one tactic change")
			}
			return preview(ctx, liveWrite{endpoint: "tactics", stateEndpoint: "tactics", body: in, target: in.Slot})
		})
	addTool(server, "preview_trial_offer", "Preview an in-game player trial invitation. A trial is not a transfer bid or employment offer.", true,
		func(ctx context.Context, in trialInput) (bridgePreview, error) {
			if in.PlayerID <= 0 || in.Weeks < 1 || in.Weeks > 4 {
				return bridgePreview{}, fmt.Errorf("provide a live player ID and a trial duration of 1 to 4 weeks")
			}
			body := map[string]any{"playerIds": []int{in.PlayerID}, "weeks": in.Weeks, "expectedPlayers": 1}
			return preview(ctx, liveWrite{endpoint: "trials", stateEndpoint: "squad", body: body, target: in.PlayerID})
		})
	addTool(server, "preview_scouting_action", "Preview sending a scout or adding a player to the shortlist. Review the game's confirmation and scouting costs before applying.", true,
		func(ctx context.Context, in scoutActionInput) (bridgePreview, error) {
			if in.PlayerID <= 0 || in.Action != "scout" && in.Action != "shortlist" {
				return bridgePreview{}, fmt.Errorf("provide a live player ID and choose scout or shortlist")
			}
			body := map[string]string{"duration": "UntilFullKnowledge"}
			if in.Action == "shortlist" {
				body["duration"] = "Indefinitely"
			}
			endpoint := fmt.Sprintf("scouting/players/%d/%s", in.PlayerID, in.Action)
			return preview(ctx, liveWrite{endpoint: endpoint, stateEndpoint: "scouting", body: body, target: in.PlayerID})
		})
	addTool(server, "apply_live_action", "Apply a reviewed tactic, scouting action or trial invitation using its single-use token. Inspect partial results and save in FM afterward.", false,
		func(ctx context.Context, in tokenInput) (bridgeResult, error) {
			mu.Lock()
			defer mu.Unlock()
			plan, err := pending.take(in.Token)
			if err != nil {
				return bridgeResult{}, err
			}
			return g.apply(ctx, plan)
		})
}
