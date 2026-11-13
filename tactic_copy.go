package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type tacticCopyInput struct {
	Name  string   `json:"name" jsonschema:"New save name without the .fm extension"`
	Width string   `json:"width,omitempty" jsonschema:"standard, much-wider, wider, narrower, or much-narrower"`
	XI    []uint32 `json:"starter_uids,omitempty" jsonschema:"Eleven distinct player UIDs in the saved tactic's slot order, as used by saved_lineup"`
}

type tacticCopyOutput struct {
	File     string   `json:"file"`
	Club     string   `json:"club"`
	Tactic   string   `json:"tactic"`
	Before   string   `json:"width_before"`
	After    string   `json:"width_after"`
	XI       []uint32 `json:"starter_uids,omitempty"`
	Revision string   `json:"revision,omitempty"`
	Token    string   `json:"token,omitempty"`
}

type tacticCopyPlan struct {
	Input    tacticCopyInput
	Revision string
}

func (e editorConfig) tacticCopy(ctx context.Context, save string, in tacticCopyInput, revision string) (tacticCopyOutput, error) {
	var out tacticCopyOutput
	name, err := fileName(in.Name, ".fm")
	if err != nil {
		return out, err
	}
	target := filepath.Join(e.outputDir, name)
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return out, fmt.Errorf("choose an unused output save name")
	}
	if in.Width == "" && len(in.XI) == 0 {
		return out, fmt.Errorf("provide width or a starting XI")
	}
	if in.Width != "" && !slices.Contains([]string{"standard", "much-wider", "wider", "narrower", "much-narrower"}, in.Width) {
		return out, fmt.Errorf("invalid attacking width")
	}
	if len(in.XI) != 0 && len(in.XI) != 11 {
		return out, fmt.Errorf("provide eleven starter UIDs in saved slot order")
	}
	args := []string{"--save", save}
	if in.Width != "" {
		args = append(args, "--width", in.Width)
	}
	if len(in.XI) > 0 {
		args = append(args, "--xi")
		for _, uid := range in.XI {
			args = append(args, strconv.FormatUint(uint64(uid), 10))
		}
	}
	if revision != "" {
		args = append(args, "--revision", revision, "--output", target)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.tacticCopyCLI, args...)
	cmd.Stderr = os.Stderr
	raw, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("tactic copy failed (%w); see local diagnostics", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("invalid tactic copy response")
	}
	hash, err := hex.DecodeString(out.Revision)
	if err != nil || len(hash) != 32 || out.Club == "" || out.Tactic == "" || (in.Width != "" && out.After != in.Width) || !slices.Equal(out.XI, in.XI) || (revision != "" && out.Revision != revision) {
		return tacticCopyOutput{}, fmt.Errorf("unexpected tactic copy response")
	}
	if revision != "" {
		if info, err := os.Lstat(target); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return tacticCopyOutput{}, fmt.Errorf("verified tactic copy was not created")
		}
	}
	out.File = name
	return out, nil
}

func registerTacticCopies(server *mcp.Server, src sources, edit editorConfig) {
	var mu sync.Mutex
	var pending approval[tacticCopyPlan]
	addTool(server, "preview_tactic_copy", "Preview attacking width and starting XI changes to a new FM27 save copy. The tactic is the managed team's first saved slot.", true,
		func(ctx context.Context, in tacticCopyInput) (tacticCopyOutput, error) {
			mu.Lock()
			defer mu.Unlock()
			pending = approval[tacticCopyPlan]{}
			out, err := edit.tacticCopy(ctx, src.save, in, "")
			if err == nil {
				out.Token = pending.issue(tacticCopyPlan{Input: in, Revision: out.Revision})
				out.Revision = ""
			}
			return out, err
		})
	addTool(server, "apply_tactic_copy", "Write and verify the approved new save copy. Load the copy in FM to use it.", false,
		func(ctx context.Context, in tokenInput) (tacticCopyOutput, error) {
			mu.Lock()
			defer mu.Unlock()
			plan, err := pending.take(in.Token)
			if err != nil {
				return tacticCopyOutput{}, err
			}
			out, err := edit.tacticCopy(ctx, src.save, plan.Input, plan.Revision)
			out.Revision = ""
			return out, err
		})
}
