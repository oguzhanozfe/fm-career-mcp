package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type club struct {
	UID      uint32 `json:"uid"`
	Name     string `json:"name"`
	Short    string `json:"short_name"`
	TeamSlot int    `json:"team_slot"`
}

type deal struct {
	Wage *int `json:"weekly_wage"`
	Loan bool `json:"loan"`
}

type parentClub struct {
	UID  uint32 `json:"club_uid"`
	Name string `json:"club_name"`
	Wage *int   `json:"weekly_wage"`
}

type player struct {
	Offset    int            `json:"db_offset"`
	UID       uint32         `json:"uid"`
	Name      string         `json:"name"`
	DOB       string         `json:"date_of_birth"`
	CA        int            `json:"current_ability"`
	PA        int            `json:"potential_ability"`
	Club      *club          `json:"club"`
	Parent    *parentClub    `json:"loan_from"`
	Contract  *deal          `json:"contract"`
	Positions map[string]int `json:"positions"`
	Value     *int           `json:"valuation_unverified"`
}

type playerData struct {
	Version struct {
		Major     int    `json:"major"`
		Date      string `json:"current_date"`
		DBFormat  int    `json:"game_db_format"`
		DBVersion int    `json:"db_version"`
		DBTag     string `json:"db_tag"`
	} `json:"version"`
	Warnings []string        `json:"warnings"`
	Probe    json.RawMessage `json:"probe"`
	Players  []player        `json:"players"`
}

type staff struct {
	UID        uint32         `json:"uid"`
	Name       string         `json:"name"`
	Club       string         `json:"club"`
	Job        string         `json:"jobName"`
	CA         int            `json:"currentAbility"`
	PA         int            `json:"potentialAbility"`
	Human      bool           `json:"humanManager,omitempty"`
	Attributes map[string]int `json:"attributes"`
}

type staffData struct {
	Date  string  `json:"gameDate"`
	Staff []staff `json:"staff"`
}

type career struct {
	players *playerData
	staff   *staffData
	save    os.FileInfo
}

type sources struct {
	save, playerJSON, staffJSON, parser, matchCLI string
}

func (s sources) load(ctx context.Context) (career, error) {
	var out career
	playerPath := s.playerJSON
	if s.save != "" {
		info, err := os.Stat(s.save)
		if err != nil || !info.Mode().IsRegular() {
			return out, errors.New("cannot read the configured FM save")
		}
		out.save = info
		temp, err := os.MkdirTemp("", "fm-career-mcp-")
		if err != nil {
			return out, fmt.Errorf("create parser workspace: %w", err)
		}
		defer os.RemoveAll(temp)
		playerPath = filepath.Join(temp, "players.json")
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, s.parser, "players", s.save, "--top", "0", "--json", playerPath)
		cmd.WaitDelay = 2 * time.Second
		if err := cmd.Run(); err != nil {
			return out, fmt.Errorf("save parser failed (%w); check the executable and try a completed save", err)
		}
	}
	if playerPath != "" {
		data, err := readJSON[playerData](playerPath)
		if err != nil {
			return out, fmt.Errorf("player data: %w", err)
		}
		if data.Version.Major == 0 || data.Players == nil {
			return out, errors.New("player data must come from fm_save_parser players --json")
		}
		out.players = &data
	}
	if s.staffJSON != "" {
		data, err := readJSON[staffData](s.staffJSON)
		if err != nil {
			return out, fmt.Errorf("staff data: %w", err)
		}
		if data.Staff == nil {
			return out, errors.New("staff data must be a FMLibreEditor staff JSON export")
		}
		out.staff = &data
	}
	if out.save != nil {
		after, err := os.Stat(s.save)
		if err != nil || !sameSaveRevision(out.save, after) {
			return career{}, errors.New("the FM save changed while reading it; retry after saving finishes")
		}
	}
	return out, nil
}

func readJSON[T any](path string) (T, error) {
	var data T
	f, err := os.Open(path)
	if err != nil {
		return data, err
	}
	defer f.Close()
	const maxBytes = 512 << 20
	info, err := f.Stat()
	if err != nil {
		return data, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return data, errors.New("expected a regular JSON file up to 512 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(f, maxBytes+1))
	if err := decoder.Decode(&data); err != nil {
		return data, err
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return data, errors.New("unexpected data after the JSON document")
	}
	return data, nil
}
