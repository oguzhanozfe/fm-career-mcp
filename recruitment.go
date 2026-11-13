package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var staffRoles = map[string][]string{
	"assistant coach":           {"people_management", "motivating", "tactical_knowledge", "judging_ability"},
	"coach":                     {"technical", "tactical", "motivating"},
	"fitness coach":             {"fitness", "determination", "motivating"},
	"goalkeeping coach":         {"goalkeeping", "determination", "motivating"},
	"set piece coach":           {"set_pieces", "tactical_knowledge", "tactical"},
	"head of youth development": {"working_with_youngsters", "judging_potential", "people_management"},
	"scout":                     {"judging_ability", "judging_potential"},
	"chief scout":               {"judging_ability", "judging_potential", "people_management"},
	"recruitment analyst":       {"data_analysis", "judging_ability", "judging_potential"},
	"performance analyst":       {"data_analysis", "tactical_knowledge"},
	"physio":                    {"physiotherapy", "sports_science"},
	"sports scientist":          {"sports_science", "fitness"},
	"director of football":      {"negotiating", "judging_ability", "judging_potential"},
	"technical director":        {"judging_staff", "negotiating", "people_management"},
}

type staffSearchInput struct {
	Role           string `json:"role" jsonschema:"Requested job, such as Assistant Coach, Scout, Physio, or Director of Football"`
	Name           string `json:"name,omitempty" jsonschema:"Optional name substring"`
	ExcludeClub    string `json:"exclude_club,omitempty" jsonschema:"Exact name of your club"`
	UnemployedOnly bool   `json:"unemployed_only,omitempty"`
	Limit          int    `json:"limit,omitempty" jsonschema:"Maximum results, defaults to 10 and cannot exceed 30"`
}

type staffCandidate struct {
	UID        uint32         `json:"uid"`
	Name       string         `json:"name"`
	Club       string         `json:"club,omitempty"`
	CurrentJob string         `json:"current_job,omitempty"`
	Fit        float64        `json:"role_attribute_average"`
	Ratings    map[string]int `json:"role_attributes"`
}

type staffSearchOutput struct {
	Role       string           `json:"role"`
	Candidates []staffCandidate `json:"candidates"`
	Matches    int              `json:"matches"`
}

func searchStaff(data *staffData, in staffSearchInput) (staffSearchOutput, error) {
	out := staffSearchOutput{Role: in.Role, Candidates: []staffCandidate{}}
	role, ok := staffRoles[fold(in.Role)]
	if !ok {
		jobs := make([]string, 0, len(staffRoles))
		for job := range staffRoles {
			jobs = append(jobs, job)
		}
		slices.Sort(jobs)
		return out, fmt.Errorf("choose a supported staff role: %s", strings.Join(jobs, ", "))
	}
	if in.Limit < 0 || in.Limit > 30 {
		return out, fmt.Errorf("limit must be between 1 and 30, or omitted")
	}
	exclude, name := fold(in.ExcludeClub), fold(in.Name)
	for _, person := range data.Staff {
		if person.UID == 0 || person.Human || person.Name == "" ||
			exclude != "" && fold(person.Club) == exclude ||
			in.UnemployedOnly && (person.Club != "" || person.Job != "") ||
			!strings.Contains(fold(person.Name), name) {
			continue
		}
		ratings := make(map[string]int, len(role))
		total := 0
		for _, key := range role {
			if score := person.Attributes[key]; score >= 1 && score <= 20 {
				ratings[key] = score
				total += score
			}
		}
		if len(ratings) != len(role) {
			continue
		}
		out.Candidates = append(out.Candidates, staffCandidate{person.UID, person.Name, person.Club, person.Job, float64(total) / float64(len(role)), ratings})
	}
	slices.SortFunc(out.Candidates, func(a, b staffCandidate) int {
		return cmp.Or(cmp.Compare(b.Fit, a.Fit), cmp.Compare(fold(a.Name), fold(b.Name)), cmp.Compare(a.UID, b.UID))
	})
	out.Matches = len(out.Candidates)
	limit := in.Limit
	if limit == 0 {
		limit = 10
	}
	out.Candidates = out.Candidates[:min(limit, out.Matches)]
	return out, nil
}

type offerInput struct {
	Kind          string `json:"kind" jsonschema:"transfer or staff_contract"`
	UID           uint32 `json:"uid" jsonschema:"Candidate UID from scout_players or search_staff_candidates"`
	Role          string `json:"role,omitempty" jsonschema:"Offered staff job for a staff contract draft"`
	UpfrontFee    *int   `json:"upfront_fee_gbp,omitempty" jsonschema:"Explicit proposed fee for a transfer, including zero when appropriate"`
	WeeklyWage    int    `json:"weekly_wage_gbp" jsonschema:"Explicit proposed wage, not a valuation or an automatic offer"`
	Months        int    `json:"months" jsonschema:"Proposed contract length in months"`
	TransferLimit *int   `json:"transfer_limit_gbp,omitempty" jsonschema:"Optional available budget from the game's transfer screen"`
	WageLimit     *int   `json:"weekly_wage_limit_gbp,omitempty" jsonschema:"Optional available weekly wage budget from FM"`
}

type offerDraft struct {
	Kind       string `json:"kind"`
	Candidate  string `json:"candidate"`
	Club       string `json:"current_club,omitempty"`
	Role       string `json:"role,omitempty"`
	Fee        *int   `json:"upfront_fee_gbp,omitempty"`
	WeeklyWage int    `json:"weekly_wage_gbp"`
	Months     int    `json:"months"`
	Action     string `json:"in_game_next_step"`
}

func draftOffer(c career, in offerInput) (offerDraft, error) {
	out := offerDraft{Kind: in.Kind, Role: in.Role, Fee: in.UpfrontFee, WeeklyWage: in.WeeklyWage, Months: in.Months}
	if in.UID == 0 || in.WeeklyWage < 0 || in.WeeklyWage > 2_000_000 ||
		in.Months < 1 || in.Months > 120 || !between(in.UpfrontFee, 0, 2_000_000_000) ||
		!between(in.TransferLimit, 0, 2_000_000_000) || !between(in.WageLimit, 0, 2_000_000) {
		return out, fmt.Errorf("invalid candidate or proposed terms")
	}
	if exceeds(in.UpfrontFee, in.TransferLimit) || in.WageLimit != nil && in.WeeklyWage > *in.WageLimit {
		return out, fmt.Errorf("proposal exceeds the budget supplied")
	}
	switch in.Kind {
	case "transfer":
		if c.players == nil || in.UpfrontFee == nil || in.Role != "" {
			return out, fmt.Errorf("transfer draft requires a player snapshot and an explicit upfront fee")
		}
		for _, p := range c.players.Players {
			if p.UID != in.UID || p.Name == "" {
				continue
			}
			out.Candidate = p.Name
			if p.Club != nil {
				out.Club = p.Club.Name
			}
			if p.Parent != nil && p.Parent.Name != "" {
				out.Club = p.Parent.Name
			}
			out.Action = "Open the player's profile and verify the selling club and transfer budget. Enter the fee under Make an Offer, then use the wage and length if contract talks open."
			return out, nil
		}
	case "staff_contract":
		if c.staff == nil || strings.TrimSpace(in.Role) == "" || in.UpfrontFee != nil {
			return out, fmt.Errorf("staff draft requires the staff export and a job, with no transfer fee")
		}
		for _, p := range c.staff.Staff {
			if p.UID == in.UID && !p.Human && p.Name != "" {
				out.Candidate, out.Club = p.Name, p.Club
				out.Action = "Open the staff profile and choose Approach to Sign. Verify any compensation, the allowed job and the available wage budget before entering these terms."
				return out, nil
			}
		}
	default:
		return out, fmt.Errorf("kind must be transfer or staff_contract")
	}
	return out, fmt.Errorf("candidate not found in the configured snapshot")
}

func registerRecruitmentTools(server *mcp.Server, initial career, current func(context.Context) (*career, error)) {
	if initial.staff != nil {
		addTool(server, "search_staff_candidates", "Rank staff by the attributes relevant to the requested job. Use a full staff JSON export to find outside candidates.", true,
			func(ctx context.Context, in staffSearchInput) (staffSearchOutput, error) {
				c, _ := current(ctx)
				return searchStaff(c.staff, in)
			})
	}
	if initial.staff != nil || initial.players != nil {
		addTool(server, "draft_recruitment_offer", "Prepare explicit transfer or staff contract terms and check them against any supplied budget. This returns a draft for the game's offer screen.", true,
			func(ctx context.Context, in offerInput) (offerDraft, error) {
				c, err := current(ctx)
				if err != nil && in.Kind != "staff_contract" {
					return offerDraft{}, err
				}
				return draftOffer(*c, in)
			})
	}
}
