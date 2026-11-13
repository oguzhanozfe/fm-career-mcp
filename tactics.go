package main

import (
	"fmt"
	"math/bits"
	"slices"
)

type tacticInput struct {
	ClubUID uint32   `json:"club_uid" jsonschema:"Club UID from find_clubs"`
	Style   string   `json:"style,omitempty" jsonschema:"balanced, press, or counter; defaults to balanced"`
	Exclude []uint32 `json:"exclude_player_uids,omitempty" jsonschema:"Club player UIDs to rest or leave out of the XI"`
}

type tacticSlot struct {
	Position  string `json:"position"`
	Role      string `json:"role_suggestion"`
	PlayerUID uint32 `json:"player_uid,omitempty"`
	Player    string `json:"player,omitempty"`
	Fit       int    `json:"position_rating,omitempty"`
}

type tacticOutput struct {
	InPossession    string       `json:"in_possession_shape"`
	OutOfPossession string       `json:"out_of_possession_shape"`
	Mentality       string       `json:"mentality"`
	Instructions    []string     `json:"team_instructions"`
	XI              []tacticSlot `json:"xi"`
	Advice          []string     `json:"advice"`
}

type slotSpec struct {
	position, role string
}

func tacticShape(style string) (tacticOutput, []slotSpec, error) {
	out := tacticOutput{Advice: []string{}}
	slots := []slotSpec{
		{"GK", "Goalkeeper"}, {"DR", "Full-back"}, {"DC", "Centre-back"}, {"DC", "Centre-back"}, {"DL", "Full-back"},
	}
	switch style {
	case "", "balanced":
		out.InPossession, out.OutOfPossession, out.Mentality = "4-2-3-1", "4-4-1-1", "balanced"
		out.Instructions = []string{"Keep one pivot behind the ball.", "Drop wide attackers alongside the pivots in the defensive phase.", "Use a mid-block and distribute short when the passing lanes are open."}
		slots = append(slots, slotSpec{"DM", "Holding midfielder"}, slotSpec{"DM", "Deep-lying playmaker"},
			slotSpec{"AMR", "Winger"}, slotSpec{"AMC", "Attacking midfielder"}, slotSpec{"AML", "Inside forward"}, slotSpec{"ST", "Centre forward"})
	case "press":
		out.InPossession, out.OutOfPossession, out.Mentality = "4-3-3", "4-1-4-1", "positive"
		out.Instructions = []string{"Press high with the striker screening the opposition pivot.", "Counter-press on turnovers with the defensive midfielder covering.", "Lower the line if the opponent repeatedly gets behind the centre-backs."}
		slots = append(slots, slotSpec{"DM", "Holding midfielder"}, slotSpec{"MC", "Central midfielder"},
			slotSpec{"MC", "Advanced playmaker"}, slotSpec{"AMR", "Winger"}, slotSpec{"AML", "Inside forward"}, slotSpec{"ST", "Centre forward"})
	case "counter":
		out.InPossession, out.OutOfPossession, out.Mentality = "4-4-2", "4-4-2", "cautious"
		out.Instructions = []string{"Defend in a compact mid-block with two outlets.", "Pass into space after regaining possession.", "Keep one striker available for layoffs and the other running in behind."}
		slots = append(slots, slotSpec{"MC", "Holding midfielder"}, slotSpec{"MC", "Central midfielder"},
			slotSpec{"MR", "Winger"}, slotSpec{"ML", "Wide midfielder"}, slotSpec{"ST", "Linking striker"}, slotSpec{"ST", "Running striker"})
	default:
		return out, nil, fmt.Errorf("style must be balanced, press, or counter")
	}
	return out, slots, nil
}

func buildTactic(data *playerData, in tacticInput) (tacticOutput, error) {
	if in.ClubUID == 0 {
		return tacticOutput{}, fmt.Errorf("club_uid must be positive")
	}
	out, slots, err := tacticShape(in.Style)
	if err != nil {
		return out, err
	}
	var squad []player
	found := false
	for _, p := range data.Players {
		if p.UID != 0 && p.Club != nil && p.Club.UID == in.ClubUID && p.Club.TeamSlot == 0 {
			found = true
			if !slices.Contains(in.Exclude, p.UID) {
				squad = append(squad, p)
			}
		}
	}
	if !found {
		return out, fmt.Errorf("no first-team players found for this club")
	}
	slices.SortFunc(squad, func(a, b player) int {
		if a.UID < b.UID {
			return -1
		}
		if a.UID > b.UID {
			return 1
		}
		return 0
	})
	squad = slices.CompactFunc(squad, func(a, b player) bool { return a.UID == b.UID })
	out.XI = selectXI(squad, slots)
	for _, slot := range out.XI {
		if slot.PlayerUID == 0 {
			out.Advice = append(out.Advice, "No eligible "+slot.Position+" selected. Scout this position or try a different shape.")
		} else if slot.Fit < 15 {
			out.Advice = append(out.Advice, "Only an emergency fit at "+slot.Position+". Compare an alternative shape or scout a natural fit.")
		}
	}
	out.Advice = append(out.Advice, "Confirm roles and eligibility in FM before a match. This XI uses position familiarity and CA and does not evaluate injuries, suspensions or registration.")
	return out, nil
}

// Keep the best unique assignment for each set of filled slots.
func selectXI(squad []player, slots []slotSpec) []tacticSlot {
	return assignXI(squad, slots, func(p player, _ slotSpec) int { return p.CA })
}

func assignXI(squad []player, slots []slotSpec, preference func(player, slotSpec) int) []tacticSlot {
	type assignment struct {
		score int
		picks [11]int
	}
	states := make([]assignment, 1<<len(slots))
	for i := 1; i < len(states); i++ {
		states[i].score = -1
	}
	for index, p := range squad {
		for mask := len(states) - 1; mask >= 0; mask-- {
			current := states[mask]
			if current.score < 0 {
				continue
			}
			for slot, spec := range slots {
				bit := 1 << slot
				fit := p.Positions[spec.position]
				if mask&bit != 0 || fit < 10 {
					continue
				}
				score := current.score + fit*50 + preference(p, spec)
				if score > states[mask|bit].score {
					next := current
					next.score = score
					next.picks[slot] = index + 1
					states[mask|bit] = next
				}
			}
		}
	}
	best := 0
	for mask, state := range states {
		if state.score >= 0 && (bits.OnesCount(uint(mask)) > bits.OnesCount(uint(best)) ||
			bits.OnesCount(uint(mask)) == bits.OnesCount(uint(best)) && state.score > states[best].score) {
			best = mask
		}
	}
	out := make([]tacticSlot, len(slots))
	for i, slot := range slots {
		out[i] = tacticSlot{Position: slot.position, Role: slot.role}
		if index := states[best].picks[i]; index != 0 {
			p := squad[index-1]
			out[i].PlayerUID, out[i].Player, out[i].Fit = p.UID, p.Name, p.Positions[slot.position]
		}
	}
	return out
}
