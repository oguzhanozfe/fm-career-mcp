package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var positions = []string{"GK", "DL", "DC", "DR", "WBL", "WBR", "DM", "ML", "MC", "MR", "AML", "AMC", "AMR", "ST"}

func fold(s string) string {
	s = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "ı", "i")
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, norm.NFD.String(s))
}

type playerRow struct {
	UID    uint32 `json:"uid"`
	Name   string `json:"name,omitempty"`
	Club   string `json:"club,omitempty"`
	CA     int    `json:"ca"`
	PA     *int   `json:"pa,omitempty"`
	Age    *int   `json:"age,omitempty"`
	Fit    int    `json:"position_rating,omitempty"`
	Loan   string `json:"loan_status,omitempty"`
	Wage   *int   `json:"weekly_wage_gbp,omitempty"`
	Parent string `json:"parent_club,omitempty"`
	Value  *int   `json:"unverified_value_gbp,omitempty"`
}

func (p player) row(date string) playerRow {
	row := playerRow{UID: p.UID, Name: p.Name, CA: p.CA, Value: p.Value}
	if p.PA > 0 && p.PA <= 200 {
		row.PA = &p.PA
	}
	if p.Club != nil {
		row.Club = p.Club.Name
	}
	if p.Parent != nil {
		row.Wage, row.Parent = p.Parent.Wage, p.Parent.Name
	} else if p.Contract != nil && !p.Contract.Loan {
		row.Wage = p.Contract.Wage
	}
	today, e1 := time.Parse(time.DateOnly, date)
	born, e2 := time.Parse(time.DateOnly, p.DOB)
	if e1 == nil && e2 == nil && !today.Before(born) {
		age := today.Year() - born.Year()
		if today.Month() < born.Month() || today.Month() == born.Month() && today.Day() < born.Day() {
			age--
		}
		row.Age = &age
	}
	return row
}

func (p player) onLoan() bool {
	return p.Parent != nil || p.Contract != nil && p.Contract.Loan
}

type clubInput struct {
	Name string `json:"name" jsonschema:"Full or partial club name"`
}

type clubList struct {
	Clubs []club `json:"clubs"`
}

func findClubs(data *playerData, in clubInput) (clubList, error) {
	query := fold(in.Name)
	if query == "" {
		return clubList{}, fmt.Errorf("enter a club name")
	}
	out := clubList{Clubs: []club{}}
	seen := make(map[uint32]bool)
	for _, p := range data.Players {
		c := p.Club
		if c == nil || c.UID == 0 || seen[c.UID] || !strings.Contains(fold(c.Name+" "+c.Short), query) {
			continue
		}
		seen[c.UID] = true
		out.Clubs = append(out.Clubs, club{UID: c.UID, Name: c.Name, Short: c.Short})
	}
	slices.SortFunc(out.Clubs, func(a, b club) int {
		return cmp.Or(cmp.Compare(fold(a.Name), fold(b.Name)), cmp.Compare(a.UID, b.UID))
	})
	if len(out.Clubs) > 25 {
		out.Clubs = out.Clubs[:25]
	}
	return out, nil
}

type squadInput struct {
	ClubUID  uint32 `json:"club_uid" jsonschema:"Club UID from find_clubs"`
	AllTeams bool   `json:"all_teams,omitempty" jsonschema:"Include reserves and youth when the parser provides team slots"`
	BelowCA  *int   `json:"below_ca,omitempty" jsonschema:"Exclusive CA cutoff for the exit shortlist; provide with below_pa"`
	BelowPA  *int   `json:"below_pa,omitempty" jsonschema:"Exclusive PA cutoff for the exit shortlist; provide with below_ca"`
}

type depthRow struct {
	Position string `json:"position"`
	Coverage int    `json:"coverage"`
}

type squadOutput struct {
	Roster []playerRow `json:"roster"`
	Depth  []depthRow  `json:"depth"`
	Exits  []playerRow `json:"exit_shortlist,omitempty"`
	Out    []playerRow `json:"loaned_out,omitempty"`
}

func squadReview(data *playerData, in squadInput) (squadOutput, error) {
	out := squadOutput{Roster: []playerRow{}}
	if in.ClubUID == 0 {
		return out, fmt.Errorf("club_uid must be positive")
	}
	if (in.BelowCA == nil) != (in.BelowPA == nil) || !between(in.BelowCA, 1, 201) || !between(in.BelowPA, 1, 201) {
		return out, fmt.Errorf("provide both exit thresholds, each from 1 to 201")
	}
	depth := make(map[string]int)
	found := false
	for _, p := range data.Players {
		here := p.Club != nil && p.Club.UID == in.ClubUID
		oursOnLoan := p.Parent != nil && p.Parent.UID == in.ClubUID
		if !here && !oursOnLoan {
			continue
		}
		found = true
		row := p.row(data.Version.Date)
		if !here {
			row.Loan = "out"
			out.Out = append(out.Out, row)
		} else {
			if !in.AllTeams && p.Club.TeamSlot != 0 {
				continue
			}
			if p.onLoan() {
				row.Loan = "in"
			}
			out.Roster = append(out.Roster, row)
			for _, pos := range positions {
				if p.Positions[pos] >= 15 {
					depth[pos]++
				}
			}
		}
		if (!p.onLoan() || oursOnLoan) && in.BelowCA != nil && row.PA != nil && p.CA < *in.BelowCA && *row.PA < *in.BelowPA {
			out.Exits = append(out.Exits, row)
		}
	}
	if !found {
		return out, fmt.Errorf("club UID not found in player data")
	}
	for _, pos := range positions {
		out.Depth = append(out.Depth, depthRow{pos, depth[pos]})
	}
	for _, rows := range [][]playerRow{out.Roster, out.Out, out.Exits} {
		slices.SortFunc(rows, byAbility)
	}
	return out, nil
}

func byAbility(a, b playerRow) int {
	return cmp.Or(cmp.Compare(b.CA, a.CA), cmp.Compare(a.UID, b.UID))
}

func between(v *int, min, max int) bool {
	return v == nil || *v >= min && *v <= max
}

type scoutInput struct {
	Name     string `json:"name,omitempty" jsonschema:"Optional partial player name"`
	Position string `json:"position,omitempty" jsonschema:"Position code such as GK, DC, DM, AMR, or ST; requires at least 15/20"`
	Exclude  uint32 `json:"exclude_club_uid,omitempty" jsonschema:"Exclude players at or contracted to this club"`
	MaxAge   *int   `json:"max_age,omitempty"`
	MinCA    *int   `json:"min_ca,omitempty"`
	MinPA    *int   `json:"min_pa,omitempty"`
	MaxWage  *int   `json:"max_weekly_wage_gbp,omitempty" jsonschema:"Ceiling on the player’s permanent contract wage, not the loan contribution"`
	MaxValue *int   `json:"max_unverified_value_gbp,omitempty" jsonschema:"Ceiling on an unverified stored figure, not an asking price"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum results, 1 to 50, defaults to 15"`
}

type scoutOutput struct {
	Matches int         `json:"matches"`
	Players []playerRow `json:"players"`
}

func scout(data *playerData, in scoutInput) (scoutOutput, error) {
	out := scoutOutput{Players: []playerRow{}}
	pos := strings.ToUpper(strings.TrimSpace(in.Position))
	if pos != "" && !slices.Contains(positions, pos) {
		return out, fmt.Errorf("position must be one of %s", strings.Join(positions, ", "))
	}
	if in.Limit < 0 || in.Limit > 50 || !between(in.MaxAge, 1, 100) || !between(in.MinCA, 1, 200) || !between(in.MinPA, 1, 200) ||
		!between(in.MaxWage, 0, 1_000_000_000) || !between(in.MaxValue, 0, 1_000_000_000) {
		return out, fmt.Errorf("filters outside their valid ranges")
	}
	name := fold(in.Name)
	for _, p := range data.Players {
		if in.Exclude != 0 && (p.Club != nil && p.Club.UID == in.Exclude || p.Parent != nil && p.Parent.UID == in.Exclude) {
			continue
		}
		if pos != "" && p.Positions[pos] < 15 || name != "" && !strings.Contains(fold(p.Name), name) {
			continue
		}
		row := p.row(data.Version.Date)
		if in.MaxAge != nil && (row.Age == nil || *row.Age > *in.MaxAge) ||
			in.MinCA != nil && p.CA < *in.MinCA ||
			in.MinPA != nil && (row.PA == nil || *row.PA < *in.MinPA) ||
			in.MaxWage != nil && (row.Wage == nil || *row.Wage > *in.MaxWage) ||
			in.MaxValue != nil && (row.Value == nil || *row.Value > *in.MaxValue) {
			continue
		}
		row.Fit = p.Positions[pos]
		out.Players = append(out.Players, row)
	}
	slices.SortFunc(out.Players, byAbility)
	out.Matches = len(out.Players)
	limit := in.Limit
	if limit == 0 {
		limit = 15
	}
	out.Players = out.Players[:min(limit, out.Matches)]
	return out, nil
}

type staffInput struct {
	Club    string         `json:"club" jsonschema:"Exact club name from the staff export; matching ignores case and accents"`
	Targets map[string]int `json:"targets,omitempty" jsonschema:"Optional desired headcounts by job name as shown in the export"`
}

type staffRole struct {
	Job       string `json:"job"`
	Current   int    `json:"current"`
	Target    *int   `json:"target,omitempty"`
	Vacancies *int   `json:"vacancies,omitempty"`
}

type staffOutput struct {
	Club    string      `json:"club"`
	Roles   []staffRole `json:"roles"`
	Members []staff     `json:"members"`
}

func auditStaff(data *staffData, in staffInput) (staffOutput, error) {
	out := staffOutput{Club: in.Club, Roles: []staffRole{}, Members: []staff{}}
	name := fold(in.Club)
	if name == "" {
		return out, fmt.Errorf("enter the club name as shown in the staff export")
	}
	counts := map[string]staffRole{}
	for job, target := range in.Targets {
		key := fold(job)
		if key == "" || target < 0 || target > 100 {
			return out, fmt.Errorf("role targets must name a job and be between 0 and 100")
		}
		if _, exists := counts[key]; exists {
			return out, fmt.Errorf("duplicate role target: %s", job)
		}
		counts[key] = staffRole{Job: strings.TrimSpace(job), Target: &target}
	}
	for _, person := range data.Staff {
		if fold(person.Club) != name {
			continue
		}
		out.Members = append(out.Members, person)
		if person.Job != "" {
			key := fold(person.Job)
			row := counts[key]
			row.Job = person.Job
			row.Current++
			counts[key] = row
		}
	}
	if len(out.Members) == 0 {
		return out, fmt.Errorf("club not found in staff data; check the export filter and exact club name")
	}
	for _, row := range counts {
		if row.Target != nil {
			vacancies := max(0, *row.Target-row.Current)
			row.Vacancies = &vacancies
		}
		out.Roles = append(out.Roles, row)
	}
	slices.SortFunc(out.Roles, func(a, b staffRole) int { return cmp.Compare(fold(a.Job), fold(b.Job)) })
	slices.SortFunc(out.Members, func(a, b staff) int {
		return cmp.Or(cmp.Compare(fold(a.Job), fold(b.Job)), cmp.Compare(b.CA, a.CA), cmp.Compare(a.UID, b.UID))
	})
	return out, nil
}
