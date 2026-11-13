package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type matchSide struct {
	Goals      *int     `json:"goals,omitempty"`
	XG         *float64 `json:"xg,omitempty"`
	NPXG       *float64 `json:"non_penalty_xg,omitempty"`
	Possession *int     `json:"possession,omitempty"`
	Shots      *int     `json:"shots,omitempty"`
	OnTarget   *int     `json:"shots_on_target,omitempty"`
	LongShots  *int     `json:"long_shots,omitempty"`
	RedCards   *int     `json:"red_cards,omitempty"`
}

type matchInput struct {
	Date     string     `json:"date,omitempty" jsonschema:"YYYY-MM-DD; defaults to the latest played first-team match"`
	Opponent string     `json:"opponent,omitempty" jsonschema:"Optional opponent name or substring"`
	For      *matchSide `json:"for,omitempty" jsonschema:"Stats from the match report for your team; omit to read from a configured source"`
	Against  *matchSide `json:"against,omitempty" jsonschema:"Stats from the match report for the opponent"`
}

type matchRecord struct {
	Date        string             `json:"date,omitempty"`
	Opponent    string             `json:"opponent,omitempty"`
	Venue       string             `json:"venue,omitempty"`
	Competition string             `json:"competition,omitempty"`
	Formation   string             `json:"formation,omitempty"`
	For         matchSide          `json:"for"`
	Against     matchSide          `json:"against"`
	Source      string             `json:"source"`
	Players     []savedPlayerMatch `json:"player_stats,omitempty"`
}

type matchFinding struct {
	Issue    string `json:"issue"`
	Evidence string `json:"evidence"`
	Change   string `json:"suggested_change"`
	Measure  string `json:"measure_next_match"`
}

type matchReview struct {
	Match    matchRecord    `json:"match"`
	Findings []matchFinding `json:"findings"`
	Notes    []string       `json:"notes,omitempty"`
}

func analyzeMatch(m matchRecord) (matchReview, error) {
	out := matchReview{Match: m, Findings: []matchFinding{}}
	for _, s := range []matchSide{m.For, m.Against} {
		if !between(s.Goals, 0, 100) || !between(s.Possession, 0, 100) || !between(s.Shots, 0, 200) ||
			!between(s.OnTarget, 0, 200) || !between(s.LongShots, 0, 200) || !between(s.RedCards, 0, 11) ||
			!validXG(s.XG) || !validXG(s.NPXG) || exceeds(s.OnTarget, s.Shots) || exceeds(s.LongShots, s.Shots) {
			return out, fmt.Errorf("inconsistent match stats")
		}
	}
	if value(m.For.RedCards) > 0 || value(m.Against.RedCards) > 0 {
		out.Notes = append(out.Notes, "A red card can distort full-match numbers. Check the periods before and after it before changing the base tactic.")
	}
	ownXG := m.For.NPXG
	if ownXG == nil {
		ownXG = m.For.XG
	}
	theirXG := m.Against.NPXG
	if theirXG == nil {
		theirXG = m.Against.XG
	}
	if ownXG == nil || theirXG == nil {
		out.Notes = append(out.Notes, "Expected goals were unavailable for at least one team. Do not diagnose finishing or chance quality from the score alone.")
	}
	add := func(issue, evidence, change, measure string) {
		out.Findings = append(out.Findings, matchFinding{issue, evidence, change, measure})
	}
	if theirXG != nil && *theirXG >= 1.6 && value(m.Against.Shots) >= 5 &&
		*theirXG/float64(*m.Against.Shots) >= 0.13 {
		add("Opponent created high-quality chances",
			fmt.Sprintf("%.2f expected goals from %d opponent shots.", *theirXG, *m.Against.Shots),
			"Review the chances and separate transitions from sustained pressure. If transitions dominate, hold one midfielder and keep one full-back behind the attack.",
			"Compare opponent expected goals per shot and chances conceded immediately after turnovers.")
	}
	if ownXG != nil && value(m.For.Shots) >= 8 && *ownXG/float64(*m.For.Shots) < 0.085 {
		evidence := fmt.Sprintf("%.2f expected goals from %d shots.", *ownXG, *m.For.Shots)
		change := "Review the shot map and add a passing option between the lines to create closer shots."
		if m.For.LongShots != nil && float64(*m.For.LongShots)/float64(*m.For.Shots) >= 0.45 {
			evidence += fmt.Sprintf(" %d shots came from outside the box.", *m.For.LongShots)
			change = "Try Work Ball Into Box and give the wide players a cutback option."
		}
		add("Low-quality shot selection", evidence, change, "Compare expected goals per shot and the share of shots inside the box.")
	}
	if ownXG != nil && m.For.Shots != nil && *m.For.Shots <= 8 && *ownXG < 1 &&
		value(m.For.Possession) >= 55 {
		add("Possession produced few chances",
			fmt.Sprintf("%d%% possession, %d shots and %.2f expected goals.", *m.For.Possession, *m.For.Shots, *ownXG),
			"Add one runner beyond the ball and test a slightly higher tempo if the match report shows slow circulation.",
			"Compare shots in the box and expected goals against a similar opponent.")
	}
	if m.For.Goals != nil && m.For.XG != nil && *m.For.XG >= 1.5 && *m.For.XG-float64(*m.For.Goals) >= 1.1 {
		add("Finishing fell below chance creation",
			fmt.Sprintf("%d goals from %.2f xG.", *m.For.Goals, *m.For.XG),
			"Keep the chance-creation setup for another match and review who took the best chances.",
			"Track goals and xG across several matches before changing the striker or system.")
	}
	for _, player := range m.Players {
		if player.Attempted != nil && player.Completed != nil && *player.Attempted >= 12 &&
			*player.Completed >= 0 && *player.Completed <= *player.Attempted &&
			float64(*player.Completed)/float64(*player.Attempted) <= 0.70 {
			add("Low pass completion for "+player.Name,
				fmt.Sprintf("%d of %d passes completed in %d minutes.", *player.Completed, *player.Attempted, value(player.Minutes)),
				"Review this player's failed passes and nearby passing options. If he was repeatedly isolated, move a teammate closer or ask him to take fewer risks.",
				"Review where his next failed passes occur and whether he has a short outlet.")
			break
		}
	}
	if len(out.Findings) == 0 {
		out.Notes = append(out.Notes, "The available numbers do not support a specific change. Check the shot map, goal sequences and substitutions for tactical context.")
	}
	return out, nil
}

func validXG(v *float64) bool { return v == nil || !math.IsNaN(*v) && *v >= 0 && *v <= 100 }
func exceeds(a, b *int) bool  { return a != nil && b != nil && *a > *b }
func value(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

type matchSource struct {
	cli, save string
	live      *gameAPI
	revision  os.FileInfo
	fair      bool
}

func (s matchSource) review(ctx context.Context, in matchInput) (matchReview, error) {
	if in.Date != "" {
		if _, err := time.Parse(time.DateOnly, in.Date); err != nil {
			return matchReview{}, fmt.Errorf("date must be YYYY-MM-DD")
		}
	}
	if in.For != nil || in.Against != nil {
		if in.For == nil || in.Against == nil {
			return matchReview{}, fmt.Errorf("supply stats for both teams")
		}
		return analyzeMatch(matchRecord{Date: in.Date, Opponent: in.Opponent, For: *in.For, Against: *in.Against, Source: "match_report_input"})
	}
	if s.live != nil {
		m, err := s.live.match(ctx, in)
		if err == nil {
			return analyzeMatch(m)
		}
		if s.cli == "" {
			return matchReview{}, err
		}
		log.Printf("live match source unavailable: %v", err)
	}
	if s.cli == "" {
		return matchReview{}, fmt.Errorf("provide stats from the FM match report or configure a match source")
	}
	stable, cleanup, err := s.snapshot()
	if err != nil {
		return matchReview{}, err
	}
	defer cleanup()
	_, fixtures, err := stable.firstTeamFixtures(ctx)
	if err != nil {
		return matchReview{}, err
	}
	return stable.analyzeSavedFixture(ctx, fixtures, in)
}

func (s matchSource) analyzeSavedFixture(ctx context.Context, fixtures []savedFixture, in matchInput) (matchReview, error) {
	m, notes, err := s.reviewSavedFixture(ctx, fixtures, in)
	if err != nil {
		return matchReview{}, err
	}
	out, err := analyzeMatch(m)
	out.Notes = append(notes, out.Notes...)
	return out, err
}

// The export calls must all read the same career revision, even if FM autosaves.
func (s matchSource) snapshot() (matchSource, func(), error) {
	before, err := os.Stat(s.save)
	if err != nil || !before.Mode().IsRegular() {
		return s, nil, fmt.Errorf("cannot read the configured FM save")
	}
	src, err := os.Open(s.save)
	if err != nil {
		return s, nil, fmt.Errorf("cannot open the configured FM save")
	}
	defer src.Close()
	opened, err := src.Stat()
	if err != nil || !sameSaveRevision(before, opened) {
		return s, nil, fmt.Errorf("the save changed while opening it; retry after FM finishes saving")
	}
	dir, err := os.MkdirTemp("", "fm-match-")
	if err != nil {
		return s, nil, fmt.Errorf("cannot create a workspace for match review: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	snapshot := filepath.Join(dir, "snapshot.fm")
	dst, err := os.OpenFile(snapshot, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		cleanup()
		return s, nil, err
	}
	n, copyErr := io.Copy(dst, io.LimitReader(src, opened.Size()+1))
	closeErr := dst.Close()
	after, statErr := os.Stat(s.save)
	if copyErr != nil || closeErr != nil || statErr != nil || n != opened.Size() || !sameSaveRevision(opened, after) {
		cleanup()
		return s, nil, fmt.Errorf("could not snapshot the save; retry after FM finishes saving")
	}
	s.save = snapshot
	s.revision = opened
	return s, cleanup, nil
}

func sameSaveRevision(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

type matchKey struct {
	Date string `json:"date"`
	Kick string `json:"kick_off_time"`
	Comp *int   `json:"competition_id"`
	Home int    `json:"home_team_id"`
	Away int    `json:"away_team_id"`
}

type savedFixture struct {
	matchKey
	HomeClub  uint32 `json:"home_club_uid"`
	AwayClub  uint32 `json:"away_club_uid"`
	HomeName  string `json:"home_club_name"`
	AwayName  string `json:"away_club_name"`
	HomeOwn   bool   `json:"-"`
	HomeGoals *int   `json:"home_goals"`
	AwayGoals *int   `json:"away_goals"`
	Played    bool   `json:"played"`
	Neutral   bool   `json:"is_neutral_venue"`
	HomeSlot  *int   `json:"home_team_slot"`
	AwaySlot  *int   `json:"away_team_slot"`
}

type savedStats struct {
	matchKey
	Team int      `json:"team_id"`
	XG   *float64 `json:"expected_goals"`
	NPXG *float64 `json:"non_penalty_expected_goals"`
	Poss *int     `json:"possession_percent"`
	Sh   *int     `json:"shots"`
	OT   *int     `json:"shots_on_target"`
	Long *int     `json:"shots_outside_box"`
	Red  *int     `json:"red_cards"`
}

func (s savedStats) side(goals *int) matchSide {
	return matchSide{goals, s.XG, s.NPXG, s.Poss, s.Sh, s.OT, s.Long, s.Red}
}

type savedPlayerMatch struct {
	Date       string   `json:"date"`
	Comp       *int     `json:"competition_id"`
	OpponentID int      `json:"opponent_team_id"`
	UID        uint32   `json:"player_uid"`
	Name       string   `json:"player_name"`
	Minutes    *int     `json:"minutes,omitempty"`
	Rating     *float64 `json:"rating,omitempty"`
	Attempted  *int     `json:"passes_attempted,omitempty"`
	Completed  *int     `json:"passes_completed,omitempty"`
	HasStats   bool     `json:"has_stats"`
	InRange    bool     `json:"stats_in_range"`
}

type managedClub struct {
	UID  uint32 `json:"club_uid"`
	Name string `json:"club_name"`
}

func (s matchSource) firstTeamFixtures(ctx context.Context) (managedClub, []savedFixture, error) {
	club, err := s.managedClub(ctx)
	if err != nil {
		return managedClub{}, nil, err
	}
	fixtures, err := s.clubFixtures(ctx, club.UID)
	return club, fixtures, err
}

func (s matchSource) managedClub(ctx context.Context) (managedClub, error) {
	clubs, err := exportRows[managedClub](ctx, s, "managed-clubs")
	if err != nil {
		return managedClub{}, err
	}
	if len(clubs) != 1 || clubs[0].UID == 0 {
		return managedClub{}, fmt.Errorf("one managed club is required for saved career tools")
	}
	return clubs[0], nil
}

func (s matchSource) clubFixtures(ctx context.Context, uid uint32) ([]savedFixture, error) {
	fixtures, err := exportRows[savedFixture](ctx, s, "fixtures")
	if err != nil {
		return nil, err
	}
	rows := fixtures[:0]
	for _, f := range fixtures {
		f.HomeOwn = f.HomeClub == uid
		awayOwn := f.AwayClub == uid
		if f.Date == "" || f.HomeOwn == awayOwn {
			continue
		}
		slot := f.AwaySlot
		if f.HomeOwn {
			slot = f.HomeSlot
		}
		if slot != nil && *slot == 0 {
			rows = append(rows, f)
		}
	}
	return rows, nil
}

func (s matchSource) reviewSavedFixture(ctx context.Context, fixtures []savedFixture, in matchInput) (matchRecord, []string, error) {
	query := fold(in.Opponent)
	var rows []savedFixture
	for _, f := range fixtures {
		if f.Played && (in.Date == "" || f.Date == in.Date) && strings.Contains(fold(f.opponent()), query) {
			rows = append(rows, f)
		}
	}
	if len(rows) == 0 {
		return matchRecord{}, nil, fmt.Errorf("no matching played first-team fixture in the saved career")
	}
	slices.SortFunc(rows, func(a, b savedFixture) int { return cmp.Or(cmp.Compare(b.Date, a.Date), cmp.Compare(b.Kick, a.Kick)) })
	f := rows[0]
	if len(rows) > 1 && rows[1].Date == f.Date {
		return matchRecord{}, nil, fmt.Errorf("more than one fixture on that date; specify the opponent")
	}
	_, other := f.teams()
	stats, err := exportRows[savedStats](ctx, s, "team-match-stats")
	if err != nil {
		if s.fair {
			return f.report(nil), []string{"Team statistics unavailable. Supply the numbers visible in your match report if you want a deeper review."}, nil
		}
		return s.withPlayerStats(ctx, f, other, f.report(nil), "This save build did not yield team match statistics.")
	}
	out := f.report(stats)
	if out.Source != "saved_match_report" {
		if s.fair {
			return out, []string{"This fixture has no unique team report in the save."}, nil
		}
		return s.withPlayerStats(ctx, f, other, out, "The save has no unique pair of team statistics for this fixture.")
	}
	return out, nil, nil
}

func (f savedFixture) teams() (own, other int) {
	if f.HomeOwn {
		return f.Home, f.Away
	}
	return f.Away, f.Home
}

func (f savedFixture) venue() string {
	if f.Neutral {
		return "neutral"
	}
	if f.HomeOwn {
		return "home"
	}
	return "away"
}

func (f savedFixture) report(stats []savedStats) matchRecord {
	out := matchRecord{Date: f.Date, Opponent: f.opponent(), Venue: f.venue(), Source: "saved_fixture"}
	own, other := f.teams()
	out.For.Goals, out.Against.Goals = f.AwayGoals, f.HomeGoals
	if f.HomeOwn {
		out.For.Goals, out.Against.Goals = f.HomeGoals, f.AwayGoals
	}
	var ownRows, otherRows []savedStats
	for _, row := range stats {
		if row.Date != f.Date || row.Kick != f.Kick || row.Home != f.Home || row.Away != f.Away ||
			!sameCompetition(row.Comp, f.Comp) {
			continue
		}
		if row.Team == own {
			ownRows = append(ownRows, row)
		}
		if row.Team == other {
			otherRows = append(otherRows, row)
		}
	}
	if len(ownRows) == 1 && len(otherRows) == 1 {
		out.For, out.Against, out.Source = ownRows[0].side(out.For.Goals), otherRows[0].side(out.Against.Goals), "saved_match_report"
	}
	return out
}

func (f savedFixture) opponent() string {
	if f.HomeOwn {
		return f.AwayName
	}
	return f.HomeName
}

func (s matchSource) withPlayerStats(ctx context.Context, f savedFixture, opponent int, out matchRecord, note string) (matchRecord, []string, error) {
	rows, err := exportRows[savedPlayerMatch](ctx, s, "player-match-stats")
	if err == nil {
		byPlayer := map[uint32]savedPlayerMatch{}
		duplicates := map[uint32]bool{}
		for _, p := range rows {
			if p.Date != f.Date || p.OpponentID != opponent || !sameCompetition(p.Comp, f.Comp) ||
				p.UID == 0 || p.Name == "" || !p.HasStats || !p.InRange || value(p.Minutes) < 30 {
				continue
			}
			if _, exists := byPlayer[p.UID]; exists {
				duplicates[p.UID] = true
			}
			byPlayer[p.UID] = p
		}
		for uid, player := range byPlayer {
			if !duplicates[uid] {
				out.Players = append(out.Players, player)
			}
		}
	}
	if len(out.Players) > 0 {
		slices.SortFunc(out.Players, func(a, b savedPlayerMatch) int {
			return cmp.Or(cmp.Compare(value(b.Minutes), value(a.Minutes)), cmp.Compare(a.UID, b.UID))
		})
		out.Source = "saved_player_match_stats"
		note += " Player ratings and passing are available for players with readable records."
	}
	return out, []string{note + " Supply the match screen's team stats for chance-quality analysis."}, nil
}

func sameCompetition(a, b *int) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func exportRows[T any](ctx context.Context, s matchSource, table string) ([]T, error) {
	if s.fair {
		switch table {
		case "managed-clubs", "fixtures", "team-match-stats", "tactics", "selections", "training", "contracts", "player-season-stats":
		default:
			return nil, fmt.Errorf("this save export is unavailable in fair mode")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	scope := "--managed-club"
	if table == "managed-clubs" {
		scope = "--all"
	}
	cmd := exec.CommandContext(ctx, s.cli, "export", s.save, table, scope, "--format", "jsonl")
	cmd.Stderr = os.Stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("fmsave could not read %s: %w", table, err)
	}
	var rows []T
	scan := bufio.NewScanner(bytes.NewReader(raw))
	scan.Buffer(make([]byte, 4096), 1<<20)
	for scan.Scan() {
		if len(bytes.TrimSpace(scan.Bytes())) == 0 {
			continue
		}
		var row T
		if err := json.Unmarshal(scan.Bytes(), &row); err != nil {
			return nil, fmt.Errorf("invalid %s export: %w", table, err)
		}
		rows = append(rows, row)
	}
	return rows, scan.Err()
}

func registerMatchTools(server *mcp.Server, src matchSource) {
	addTool(server, "review_match", "Diagnose a played match using live or saved team stats, or stats from the FM match screen. Returns evidence, a suggested change and what to measure next.", true, src.review)
}
