package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type editorConfig struct {
	tactics, template, outputDir, live string
	pid, club                          uint
	holidays                           bool
	allowUnknown                       bool
}

func (e editorConfig) validate(src sources) error {
	if (e.tactics == "") != (e.template == "") {
		return fmt.Errorf("configure both -tactics-cli and -tactic-template")
	}
	if e.holidays && src.save == "" {
		return fmt.Errorf("-holiday-copies requires -save")
	}
	if e.holidays || e.tactics != "" {
		if info, err := os.Stat(e.outputDir); err != nil || !info.IsDir() {
			return fmt.Errorf("configure an existing -output-dir")
		}
	}
	if e.live != "" && (e.pid == 0 || e.club == 0 || e.pid > 0xffffffff || e.club > 0xffffffff) {
		return fmt.Errorf("configure -live-pid and -live-club for the active FM27 session")
	}
	return nil
}

type fileResult struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

type tacticFileInput struct {
	Name      string `json:"name" jsonschema:"New tactic name; the server adds the .fmf extension"`
	Mentality string `json:"mentality,omitempty" jsonschema:"Optional mentality from very-defensive to very-attacking"`
	Tackling  string `json:"tackling,omitempty" jsonschema:"Optional get-stuck-in, stay-on-feet, or none"`
}

func fileName(name, ext string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 || strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("use a file name of 1 to 100 bytes")
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(" -_()", r) {
			return "", fmt.Errorf("use letters, numbers, spaces, hyphens, underscores or parentheses")
		}
	}
	firstWord := strings.ToUpper(name)
	switch firstWord {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return "", fmt.Errorf("choose a name that is not reserved by Windows")
	}
	return name + ext, nil
}

func (e editorConfig) exportTactic(ctx context.Context, in tacticFileInput) (fileResult, error) {
	name, err := fileName(in.Name, ".fmf")
	if err != nil {
		return fileResult{}, err
	}
	target, err := filepath.Abs(filepath.Join(e.outputDir, name))
	if err != nil {
		return fileResult{}, err
	}
	args := []string{"create", "--template", e.template, "--output", target, "--name", strings.TrimSpace(in.Name), "--json"}
	switch in.Mentality {
	case "":
	case "very-defensive", "defensive", "cautious", "balanced", "positive", "attacking", "very-attacking":
		args = append(args, "--mentality", in.Mentality)
	default:
		return fileResult{}, fmt.Errorf("invalid mentality")
	}
	switch in.Tackling {
	case "":
	case "get-stuck-in", "stay-on-feet", "none":
		args = append(args, "--tackling", in.Tackling)
	default:
		return fileResult{}, fmt.Errorf("invalid tackling instruction")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := exec.CommandContext(ctx, e.tactics, args...).Run(); err != nil {
		return fileResult{}, fmt.Errorf("tactic export failed (%w); check the template schema and choose a new file name", err)
	}
	if info, err := os.Stat(target); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return fileResult{}, fmt.Errorf("tactic writer did not produce a non-empty FMF")
	}
	return fileResult{name, "Import this FMF from the configured output directory. Formation and roles come from the template."}, nil
}

type approval[T any] struct {
	token   string
	expires time.Time
	value   T
}

func (a *approval[T]) issue(v T) string {
	a.token, a.expires, a.value = rand.Text(), time.Now().Add(5*time.Minute), v
	return a.token
}

func (a *approval[T]) take(token string) (T, error) {
	if token == "" || token != a.token || time.Now().After(a.expires) {
		var zero T
		return zero, fmt.Errorf("preview again to get a current confirmation token")
	}
	value := a.value
	*a = approval[T]{}
	return value, nil
}

type tokenInput struct {
	Token string `json:"token" jsonschema:"Confirmation token from the preview after reviewing the proposed change"`
}

type healthTarget struct {
	UID      uint32 `json:"uid"`
	Name     string `json:"name"`
	Injuries int    `json:"injuries"`
}

type healthOutput struct {
	Club    string         `json:"club"`
	Targets []healthTarget `json:"targets"`
	Token   string         `json:"token,omitempty"`
	Message string         `json:"message,omitempty"`
}

type liveHealth struct {
	Club    string          `json:"club"`
	Targets []healthTarget  `json:"targets"`
	State   json.RawMessage `json:"state"`
}

func (e editorConfig) health(ctx context.Context, mode string, expected []byte) (liveHealth, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	args := []string{mode, strconv.FormatUint(uint64(e.pid), 10), strconv.FormatUint(uint64(e.club), 10)}
	if e.allowUnknown {
		args = append(args, "--allow-unknown-build")
	}
	cmd := exec.CommandContext(ctx, e.live, args...)
	cmd.Stdin = bytes.NewReader(expected)
	cmd.Stderr = os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return liveHealth{}, nil, fmt.Errorf("live editor failed (%w); see local diagnostics and inspect the game before saving", err)
	}
	var out liveHealth
	if err := json.Unmarshal(data, &out); err != nil || out.Club == "" || out.Targets == nil {
		return out, nil, fmt.Errorf("invalid live adapter response")
	}
	return out, data, nil
}

func registerEditors(server *mcp.Server, src sources, edit editorConfig) {
	if edit.tactics != "" {
		addTool(server, "export_tactic", "Write a new local FMF variant of the configured template. Changes the name, mentality and tackling only.", false, edit.exportTactic)
	}
	if edit.live != "" {
		var mu sync.Mutex
		var pending approval[[]byte]
		addTool(server, "preview_heal", "Read active injuries of owned players at the configured club in the running FM27 session.", true,
			func(ctx context.Context, _ struct{}) (healthOutput, error) {
				mu.Lock()
				defer mu.Unlock()
				pending = approval[[]byte]{}
				out, data, err := edit.health(ctx, "preview", nil)
				if err != nil {
					return healthOutput{}, err
				}
				result := healthOutput{Club: out.Club, Targets: out.Targets}
				if len(result.Targets) > 0 {
					result.Token = pending.issue(data)
				}
				return result, nil
			})
		addTool(server, "apply_heal", "Heal the injuries from a confirmed preview in the active game. Save the game in FM afterward.", false,
			func(ctx context.Context, in tokenInput) (healthOutput, error) {
				mu.Lock()
				defer mu.Unlock()
				data, err := pending.take(in.Token)
				if err != nil {
					return healthOutput{}, err
				}
				out, _, err := edit.health(ctx, "apply", data)
				if err != nil {
					return healthOutput{}, err
				}
				return healthOutput{Club: out.Club, Targets: out.Targets, Message: "Injuries cleared in the running game. Save in FM to keep the change."}, nil
			})
	}
	if edit.holidays {
		registerHolidays(server, src, edit.outputDir)
	}
}
