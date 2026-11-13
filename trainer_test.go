package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTrainerPreviewApplyAndReadBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Trainer integration uses a macOS Unix socket")
	}
	dir, err := os.MkdirTemp("/tmp", "fm-trainer-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "agent.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var writes atomic.Int32
	var drift atomic.Bool
	var unknown atomic.Bool
	go func() {
		week := [][]uint16{{300, 300, 300}, {500, 300, 300}, {302, 300, 300}, {300, 300, 300}, {402, 300, 300}, {300, 300, 300}, {300, 300, 300}}
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var req struct {
				Cmd  string
				Days [][]uint16
			}
			if json.NewDecoder(conn).Decode(&req) != nil {
				conn.Close()
				continue
			}
			var data any
			switch req.Cmd {
			case "ping":
				data = trainerStatus{PID: 42, Build: "tested", Known: !unknown.Load(), Platform: "macos"}
			case "club":
				data = map[string]any{"uid": 1869}
			case "training_weeks":
				rows := slices.Clone(week)
				if drift.Load() {
					rows[3] = []uint16{301, 300, 300}
				}
				data = trainerCalendar{Today: trainerDate{2026, 201}, Weeks: []trainerWeek{{Start: trainerDate{2026, 201}, Days: rows}}}
			case "set_week":
				count := 0
				for i, day := range req.Days {
					if !slices.Contains(day, uint16(500)) {
						week[i] = day
						count++
					}
				}
				writes.Add(1)
				data = map[string]int{"days": count}
			}
			_ = json.NewEncoder(conn).Encode(map[string]any{"ok": data != nil, "data": data})
			conn.Close()
		}
	}()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-trainer", Version: "1"}, nil)
	registerTrainer(server, trainerClient{socket: path, club: 1869})
	ctx := context.Background()
	left, right := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, left, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, right, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	args := func(date string) map[string]any {
		return map[string]any{"week_start": "2026-07-20", "changes": []trainerDayChange{{Date: date, Sessions: []string{"Recovery", "Rest", "Rest"}}}}
	}
	rejected := func(name string, params map[string]any) {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: params})
		if err != nil || !res.IsError {
			t.Fatalf("%s should fail: %+v, %v", name, res, err)
		}
	}
	rejected("preview_live_training", args("2026-07-21"))
	unknown.Store(true)
	rejected("preview_live_training", args("2026-07-22"))
	unknown.Store(false)
	preview := callTool[trainerPlan](t, session, "preview_live_training", args("2026-07-22"))
	drift.Store(true)
	rejected("apply_live_training", map[string]any{"token": preview.Token})
	drift.Store(false)
	if writes.Load() != 0 {
		t.Fatal("preview or stale approval attempted a write")
	}
	preview = callTool[trainerPlan](t, session, "preview_live_training", args("2026-07-22"))
	applied := callTool[map[string]any](t, session, "apply_live_training", map[string]any{"token": preview.Token})
	if applied["applied"] != true || writes.Load() != 1 {
		t.Fatalf("expected one verified write: %v", applied)
	}
	rejected("apply_live_training", map[string]any{"token": preview.Token})
}
