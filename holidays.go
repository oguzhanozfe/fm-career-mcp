package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type holidayInput struct {
	ClubUID uint32 `json:"club_uid" jsonschema:"Club identifier from find_clubs on this save"`
	Name    string `json:"name" jsonschema:"Name of the new save copy, without the .fm extension"`
}

type holidayPlayer struct {
	UID   uint32 `json:"uid"`
	Name  string `json:"name"`
	Start string `json:"starts"`
	End   string `json:"scheduled_return"`
}

type holidayPreview struct {
	File    string          `json:"file"`
	Players []holidayPlayer `json:"players"`
	Token   string          `json:"token,omitempty"`
}

type holidayPlan struct {
	input holidayInput
	hash  [32]byte
}

type holidayEdit struct {
	offset int
	player holidayPlayer
}

var (
	saveSignature = []byte{2, 1, 'f', 'm', 'f', '.', 8, 0, 0}
	holidaySuffix = []byte{5, 31, 255, 119, 2, 255}
	holidayPrefix = []byte{1, 0, 1, 0, 0, 255, 0}
	holidayMiddle = []byte{255, 0, 0}
)

func registerHolidays(server *mcp.Server, src sources, outputDir string) {
	var mu sync.Mutex
	var pending approval[holidayPlan]
	addTool(server, "preview_holiday_return", "Find supported active player holidays in an FM27 save. Preview a new save copy without changing the source.", true,
		func(ctx context.Context, in holidayInput) (holidayPreview, error) {
			mu.Lock()
			defer mu.Unlock()
			pending = approval[holidayPlan]{}
			out, plan, err := prepareHolidays(ctx, src, in, outputDir, nil)
			if err == nil && len(out.Players) > 0 {
				out.Token = pending.issue(plan)
			}
			return out, err
		})
	addTool(server, "apply_holiday_return", "Write the confirmed holiday changes to a new .fm copy. The original save remains available.", false,
		func(ctx context.Context, in tokenInput) (holidayPreview, error) {
			mu.Lock()
			defer mu.Unlock()
			plan, err := pending.take(in.Token)
			if err != nil {
				return holidayPreview{}, err
			}
			out, _, err := prepareHolidays(ctx, src, plan.input, outputDir, &plan.hash)
			return out, err
		})
}

func prepareHolidays(ctx context.Context, src sources, in holidayInput, outputDir string, expected *[32]byte) (holidayPreview, holidayPlan, error) {
	out := holidayPreview{Players: []holidayPlayer{}}
	plan := holidayPlan{input: in}
	if in.ClubUID == 0 {
		return out, plan, fmt.Errorf("club_uid must be positive")
	}
	name, err := fileName(in.Name, ".fm")
	if err != nil {
		return out, plan, err
	}
	out.File = name
	target := filepath.Join(outputDir, name)
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return out, plan, fmt.Errorf("output name already exists or cannot be checked")
	}
	info, err := os.Stat(src.save)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512<<20 {
		return out, plan, fmt.Errorf("expected a regular FM save up to 512 MiB")
	}
	raw, err := os.ReadFile(src.save)
	if err != nil {
		return out, plan, fmt.Errorf("cannot read the configured save")
	}
	plan.hash = sha256.Sum256(raw)
	if expected != nil && plan.hash != *expected {
		return out, plan, fmt.Errorf("save changed after preview; preview again")
	}
	work, err := os.MkdirTemp("", "fm-holiday-")
	if err != nil {
		return out, plan, err
	}
	defer os.RemoveAll(work)
	working := filepath.Join(work, "source.fm")
	if err := os.WriteFile(working, raw, 0600); err != nil {
		return out, plan, err
	}
	c, err := (sources{save: working, parser: src.parser}).load(ctx)
	if err != nil {
		return out, plan, err
	}
	data := c.players
	if v := data.Version; v.Major != 27 || v.DBFormat != 4000 || v.DBVersion != 9 || v.DBTag != "2700" ||
		string(data.Probe) != "null" {
		return out, plan, fmt.Errorf("this save layout is unsupported for holiday changes")
	}
	dec, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(768<<20), zstd.WithDecodeAllCapLimit(true), zstd.WithDecoderConcurrency(1))
	if err != nil {
		return out, plan, err
	}
	defer dec.Close()
	archive, err := readArchive(raw, dec)
	if err != nil {
		return out, plan, err
	}
	chunk, err := archive.readDB(dec)
	if err != nil {
		return out, plan, err
	}
	changes, err := holidayChanges(data, chunk, in.ClubUID)
	if err != nil {
		return out, plan, err
	}
	for _, change := range changes {
		out.Players = append(out.Players, change.player)
	}
	if expected == nil || len(changes) == 0 {
		return out, plan, nil
	}
	db := make([]byte, 0, len(chunk)-19*len(changes))
	last := 0
	for _, change := range changes {
		db = append(db, chunk[last:change.offset]...)
		db = append(db, 0)
		last = change.offset + 20
	}
	db = append(db, chunk[last:]...)
	newRaw, err := archive.replaceDB(db)
	if err != nil {
		return out, plan, err
	}
	if err := os.WriteFile(working, newRaw, 0600); err != nil {
		return out, plan, err
	}
	check, err := (sources{save: working, parser: src.parser}).load(ctx)
	if err != nil {
		return out, plan, fmt.Errorf("reopening generated save with the parser: %w", err)
	}
	remaining, err := holidayChanges(check.players, db, in.ClubUID)
	if err != nil || len(remaining) != 0 || !samePlayerData(data, check.players) {
		return out, plan, fmt.Errorf("generated save did not pass holiday and player verification")
	}
	if err := publishCopy(target, newRaw); err != nil {
		return out, plan, err
	}
	return out, plan, nil
}

// The verified singleton has a fixed prefix, a two-byte duration, a start
// date and a fixed suffix. The limit keeps the scan inside this player's data.
func holidayChanges(data *playerData, db []byte, clubUID uint32) ([]holidayEdit, error) {
	today, err := time.Parse(time.DateOnly, data.Version.Date)
	if err != nil {
		return nil, fmt.Errorf("in-game date could not be established")
	}
	players := slices.Clone(data.Players)
	slices.SortFunc(players, func(a, b player) int { return a.Offset - b.Offset })
	var out []holidayEdit
	found := false
	for i, p := range players {
		if p.Club == nil || p.Club.UID != clubUID || p.onLoan() {
			continue
		}
		found = true
		if p.UID == 0 || p.Offset < 15 || p.Offset > len(db)-4 ||
			binary.LittleEndian.Uint32(db[p.Offset-15:]) != p.UID ||
			binary.LittleEndian.Uint32(db[p.Offset-11:]) != p.UID ||
			int(binary.LittleEndian.Uint16(db[p.Offset:])) != p.CA {
			return nil, fmt.Errorf("player anchor did not validate")
		}
		from, to := p.Offset+205, min(p.Offset+415, len(db))
		if i+1 < len(players) {
			to = min(to, players[i+1].Offset-15)
		}
		match := -1
		for pos := from; pos+len(holidaySuffix) <= to; {
			n := bytes.Index(db[pos:to], holidaySuffix)
			if n < 0 {
				break
			}
			at := pos + n
			pos = at + len(holidaySuffix)
			day := int(binary.LittleEndian.Uint16(db[at-4:]) & 511)
			year := int(binary.LittleEndian.Uint16(db[at-2:]))
			days := int(binary.LittleEndian.Uint16(db[at-9:]))
			if day == 0 || day > 366 || year < today.Year()-1 || year > today.Year() || days < 1 || days > 180 {
				continue
			}
			start := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, day-1)
			end := start.AddDate(0, 0, days)
			if start.Year() != year || start.After(today) || !end.After(today) {
				continue
			}
			if match >= 0 || !bytes.Equal(db[at-16:at-9], holidayPrefix) ||
				!bytes.Equal(db[at-7:at-4], holidayMiddle) {
				return nil, fmt.Errorf("a possible active holiday uses an unsupported record; no copy written")
			}
			match = at - 14
			out = append(out, holidayEdit{match, holidayPlayer{p.UID, p.Name, start.Format(time.DateOnly), end.Format(time.DateOnly)}})
		}
	}
	if !found {
		return nil, fmt.Errorf("no owned players found for this club identifier")
	}
	return out, nil
}

func samePlayerData(before, after *playerData) bool {
	if before.Version != after.Version || len(before.Players) != len(after.Players) {
		return false
	}
	for i := range before.Players {
		a, b := before.Players[i], after.Players[i]
		a.Offset, b.Offset = 0, 0
		if !reflect.DeepEqual(a, b) {
			return false
		}
	}
	return true
}

func publishCopy(target string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(target), ".fm-career-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Link(f.Name(), target)
}

type archiveEntry struct {
	field            int
	offset, size     uint64
	uncompressedSize uint64
}

type saveArchive struct {
	raw, directory []byte
	directoryAt    int
	database       archiveEntry
	entries        []archiveEntry
}

func readArchive(raw []byte, dec *zstd.Decoder) (saveArchive, error) {
	if len(raw) < 35 || !bytes.HasPrefix(raw, saveSignature) ||
		binary.LittleEndian.Uint64(raw[17:]) != 17 || raw[25] != 3 {
		return saveArchive{}, fmt.Errorf("unsupported FM archive header")
	}
	body := binary.LittleEndian.Uint64(raw[9:])
	if body < 17 || body > uint64(len(raw)-18) {
		return saveArchive{}, fmt.Errorf("invalid FM archive directory")
	}
	at := int(body) + 9
	if !bytes.Equal(raw[at:at+9], saveSignature) {
		return saveArchive{}, fmt.Errorf("invalid FM archive directory signature")
	}
	dir, err := dec.DecodeAll(raw[at+9:], make([]byte, 0, 16<<20))
	if err != nil {
		return saveArchive{}, err
	}
	a := saveArchive{raw: raw, directory: dir, directoryAt: at}
	r := bytes.NewReader(dir)
	if err := a.readDirectory(r, 0); err != nil {
		return saveArchive{}, err
	}
	if a.database.size == 0 || r.Len() != 0 {
		return saveArchive{}, fmt.Errorf("incomplete FM archive directory")
	}
	for _, e := range a.entries {
		db := a.database
		if e.field != db.field && e.offset < db.offset+db.size && db.offset < e.offset+e.size {
			return saveArchive{}, fmt.Errorf("overlapping game database and another archive entry")
		}
	}
	return a, nil
}

func (a *saveArchive) readDirectory(r *bytes.Reader, depth int) error {
	if depth > 32 {
		return fmt.Errorf("archive directory is too deep")
	}
	read32 := func() (uint32, error) {
		var n uint32
		err := binary.Read(r, binary.LittleEndian, &n)
		return n, err
	}
	readString := func() (string, error) {
		n, err := read32()
		if err != nil || n > 1<<20 || uint64(n) > uint64(r.Len()) {
			return "", fmt.Errorf("invalid archive directory string")
		}
		at := len(a.directory) - r.Len()
		_, err = r.Seek(int64(n), 1)
		return string(a.directory[at : at+int(n)]), err
	}
	if _, err := readString(); err != nil {
		return err
	}
	n, err := read32()
	if err != nil || uint64(n)*48 > uint64(r.Len()) {
		return fmt.Errorf("invalid archive directory entry count")
	}
	for range n {
		name, err := readString()
		if err != nil {
			return err
		}
		ext, err := readString()
		if err != nil || r.Len() < 40 {
			return fmt.Errorf("invalid archive directory entry")
		}
		at := len(a.directory) - r.Len()
		b := a.directory[at:]
		e := archiveEntry{at, binary.LittleEndian.Uint64(b), binary.LittleEndian.Uint64(b[8:]), binary.LittleEndian.Uint64(b[16:])}
		available := uint64(a.directoryAt - 26)
		if e.offset > available || e.size > available-e.offset {
			return fmt.Errorf("archive entry outside data section")
		}
		a.entries = append(a.entries, e)
		if name+ext == "game_db.dat" {
			if a.database.size != 0 || e.size == 0 {
				return fmt.Errorf("invalid or duplicate game database")
			}
			a.database = e
		}
		if _, err := r.Seek(40, 1); err != nil {
			return err
		}
	}
	subdirs, err := read32()
	if err != nil || uint64(subdirs)*12 > uint64(r.Len()) {
		return fmt.Errorf("invalid archive subdirectory count")
	}
	for range subdirs {
		if err := a.readDirectory(r, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (a saveArchive) readDB(dec *zstd.Decoder) ([]byte, error) {
	e := a.database
	if e.uncompressedSize > 512<<20 {
		return nil, fmt.Errorf("game database too large for holiday editing")
	}
	db, err := dec.DecodeAll(a.raw[26+e.offset:26+e.offset+e.size], make([]byte, 0, e.uncompressedSize))
	if err != nil || uint64(len(db)) != e.uncompressedSize {
		return nil, fmt.Errorf("game database could not be decompressed")
	}
	return db, nil
}

func (a saveArchive) replaceDB(db []byte) ([]byte, error) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	defer encoder.Close()
	frame := encoder.EncodeAll(db, nil)
	shift := int64(len(frame)) - int64(a.database.size)
	dir := bytes.Clone(a.directory)
	for _, e := range a.entries {
		if e.field == a.database.field {
			binary.LittleEndian.PutUint64(dir[e.field+8:], uint64(len(frame)))
			binary.LittleEndian.PutUint64(dir[e.field+16:], uint64(len(db)))
		} else if e.offset > a.database.offset {
			binary.LittleEndian.PutUint64(dir[e.field:], uint64(int64(e.offset)+shift))
		}
	}
	start, end := 26+a.database.offset, 26+a.database.offset+a.database.size
	out := make([]byte, 0, len(a.raw))
	out = append(out, a.raw[:start]...)
	out = append(out, frame...)
	out = append(out, a.raw[end:a.directoryAt+9]...)
	out = encoder.EncodeAll(dir, out)
	binary.LittleEndian.PutUint64(out[9:], uint64(int64(a.directoryAt)+shift-9))
	return out, nil
}
