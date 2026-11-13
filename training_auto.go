package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type automaticTrainingStatus struct {
	Focus      string `json:"focus"`
	State      string `json:"state"`
	GameDate   string `json:"game_date,omitempty"`
	LatestCopy string `json:"latest_copy,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

func registerAutomaticTraining(ctx context.Context, server *mcp.Server, src sources, edit editorConfig) {
	var mu sync.Mutex
	state := automaticTrainingStatus{Focus: edit.autoTraining, State: "waiting"}
	addTool(server, "automatic_training_status", "Show the result of preparing training from the latest saved career revision.", true,
		func(_ context.Context, _ struct{}) (automaticTrainingStatus, error) {
			mu.Lock()
			defer mu.Unlock()
			return state, nil
		})
	go watchTrainingCopies(ctx, src, edit, func(next automaticTrainingStatus) {
		mu.Lock()
		defer mu.Unlock()
		state = next
	})
}

func watchTrainingCopies(ctx context.Context, src sources, edit editorConfig, report func(automaticTrainingStatus)) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	var last os.FileInfo
	var previous automaticTrainingStatus
	for ctx.Err() == nil {
		info, err := os.Stat(src.save)
		if err != nil || last == nil || !sameSaveRevision(last, info) {
			var out trainingCopyResult
			if err == nil {
				out, err = automaticTrainingCopy(ctx, src, edit)
			}
			next := automaticTrainingStatus{Focus: edit.autoTraining, GameDate: out.Date, LatestCopy: out.File}
			switch {
			case err != nil:
				next.State, next.Detail = "waiting", err.Error()
			case out.File != "":
				next.State = "ready"
			default:
				next.State = "up_to_date"
			}
			if next != previous {
				report(next)
				previous = next
			}
			if err == nil {
				last = info
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func automaticTrainingCopy(ctx context.Context, src sources, edit editorConfig) (trainingCopyResult, error) {
	s, cleanup, err := (matchSource{save: src.save, cli: src.matchCLI}).snapshot()
	if err != nil {
		return trainingCopyResult{}, err
	}
	defer cleanup()
	club, err := s.managedClub(ctx)
	if err != nil {
		return trainingCopyResult{}, err
	}
	report, err := savedTrainingPlan(ctx, s, club, trainingPlanInput{Days: 14, Focus: edit.autoTraining})
	if err != nil {
		return trainingCopyResult{}, err
	}
	plan := trainingCopyApproval{file: "training-preview.fm", days: trainingCopyDays(report)}
	if len(plan.days) == 0 {
		return trainingCopyResult{Date: report.GameDate}, nil
	}
	stable := src
	stable.save = s.save
	out, err := runTrainingCopy(ctx, stable, edit, plan)
	if err != nil || len(out.Changed) == 0 {
		return out, err
	}
	plan.revision, plan.changed = out.Revision, out.Changed
	base := strings.TrimSuffix(filepath.Base(src.save), filepath.Ext(src.save))
	if names := []rune(base); len(names) > 28 {
		base = string(names[:28])
	}
	plan.file = fmt.Sprintf("%s - Training %s %s %s.fm", base, report.GameDate, out.Revision[:12], report.Focus)
	if info, err := os.Lstat(filepath.Join(edit.outputDir, plan.file)); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
		out.File = plan.file
		return out, nil
	}
	return runTrainingCopy(ctx, src, edit, plan)
}
