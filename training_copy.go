package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type trainingCopyInput struct {
	trainingPlanInput
	Name string `json:"name" jsonschema:"New local save name without the .fm extension"`
}

type trainingCopyResult struct {
	Club     string              `json:"club"`
	Date     string              `json:"game_date"`
	Changed  []string            `json:"changed_days"`
	Revision string              `json:"revision,omitempty"`
	File     string              `json:"file,omitempty"`
	Token    string              `json:"token,omitempty"`
	Plan     *trainingPlanReport `json:"plan,omitempty"`
}

type trainingCopyApproval struct {
	file, revision string
	days           map[string][]uint16
	changed        []string
}

func runTrainingCopy(ctx context.Context, src sources, edit editorConfig, plan trainingCopyApproval) (trainingCopyResult, error) {
	var out trainingCopyResult
	target := filepath.Join(edit.outputDir, plan.file)
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return out, fmt.Errorf("choose an unused output save name")
	}
	payload, err := json.Marshal(plan.days)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	args := []string{"--training", "--save", src.save}
	if plan.revision != "" {
		args = append(args, "--output", target, "--revision", plan.revision)
	}
	cmd := exec.CommandContext(ctx, edit.tacticCopyCLI, args...)
	cmd.Stdin, cmd.Stderr = bytes.NewReader(payload), os.Stderr
	raw, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("training copy failed: %w; see local diagnostics", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("invalid training copy response")
	}
	hash, err := hex.DecodeString(out.Revision)
	if err != nil || len(hash) != 32 || out.Club == "" || len(out.Changed) > 14 {
		return trainingCopyResult{}, fmt.Errorf("unexpected training copy response")
	}
	if plan.revision != "" {
		if out.Revision != plan.revision || !slices.Equal(out.Changed, plan.changed) {
			return trainingCopyResult{}, fmt.Errorf("training copy differed from the preview")
		}
		if info, err := os.Lstat(target); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return trainingCopyResult{}, fmt.Errorf("verified training copy was not created")
		}
		out.File = plan.file
	}
	return out, nil
}

func registerTrainingCopy(ctx context.Context, server *mcp.Server, src sources, edit editorConfig) {
	var mu sync.Mutex
	var pending approval[trainingCopyApproval]
	if edit.autoTraining != "" {
		registerAutomaticTraining(ctx, server, src, edit)
	}
	addSaveTool(server, matchSource{save: src.save, cli: src.matchCLI}, "preview_training_copy",
		"Plan training around the latest saved fixtures and preview the exact changes for a separate FM27 save.",
		func(ctx context.Context, snapshot matchSource, club managedClub, in trainingCopyInput) (trainingCopyResult, error) {
			mu.Lock()
			defer mu.Unlock()
			file, err := fileName(in.Name, ".fm")
			if err != nil {
				return trainingCopyResult{}, err
			}
			report, err := savedTrainingPlan(ctx, snapshot, club, in.trainingPlanInput)
			if err != nil {
				return trainingCopyResult{}, err
			}
			plan := trainingCopyApproval{file: file, days: trainingCopyDays(report)}
			if len(plan.days) == 0 {
				return trainingCopyResult{Club: club.Name, Date: report.GameDate, Changed: []string{}, Plan: &report}, nil
			}
			stable := src
			stable.save = snapshot.save
			out, err := runTrainingCopy(ctx, stable, edit, plan)
			if err != nil {
				return out, err
			}
			out.Plan = &report
			if len(out.Changed) > 0 {
				plan.revision, plan.changed = out.Revision, out.Changed
				out.Token = pending.issue(plan)
			}
			out.Revision = ""
			return out, nil
		})
	addTool(server, "apply_training_copy", "Write and reopen-verify the previewed training changes in a new local save copy.", false,
		func(ctx context.Context, in tokenInput) (trainingCopyResult, error) {
			mu.Lock()
			defer mu.Unlock()
			plan, err := pending.take(in.Token)
			if err != nil {
				return trainingCopyResult{}, err
			}
			out, err := runTrainingCopy(ctx, src, edit, plan)
			out.Revision = ""
			return out, err
		})
}

func trainingCopyDays(report trainingPlanReport) map[string][]uint16 {
	days := map[string][]uint16{}
	for _, row := range report.Days {
		for _, session := range row.Sessions {
			days[row.Date] = append(days[row.Date], trainingSessionCodes[session])
		}
	}
	return days
}
