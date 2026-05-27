package loopstate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	MaxStateBytes     = 16 * 1024
	MaxEventLineBytes = 32 * 1024
)

type State struct {
	Version              int    `json:"version"`
	LoopID               string `json:"loop_id,omitempty"`
	OwnerSession         string `json:"owner_session,omitempty"`
	Status               string `json:"status,omitempty"`
	ProtocolPath         string `json:"protocol_path,omitempty"`
	CreatedAt            string `json:"created_at,omitempty"`
	UpdatedAt            string `json:"updated_at,omitempty"`
	LastProtocolUpdateAt string `json:"last_protocol_update_at,omitempty"`
	ProtocolUpdates      int    `json:"protocol_updates,omitempty"`
	ProtocolFeeds        int    `json:"protocol_feeds,omitempty"`
	LastProtocolFeedAt   string `json:"last_protocol_feed_at,omitempty"`
	LastProtocolFeedMode string `json:"last_protocol_feed_mode,omitempty"`
	EventCount           int    `json:"event_count,omitempty"`
	LastEventType        string `json:"last_event_type,omitempty"`
	LastEventSummary     string `json:"last_event_summary,omitempty"`
	LastEventAt          string `json:"last_event_at,omitempty"`
}

type Event struct {
	Seq             int      `json:"seq"`
	Time            string   `json:"time"`
	Type            string   `json:"type"`
	Summary         string   `json:"summary,omitempty"`
	SectionsChanged []string `json:"sections_changed,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	Path            string   `json:"path,omitempty"`
	Mode            string   `json:"mode,omitempty"`
	FeedNumber      int      `json:"feed_number,omitempty"`
}

func ReadState(path string) (State, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, false, nil
		}
		return State{}, false, err
	}
	if info.IsDir() {
		return State{}, false, errors.New("loop state path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return State{}, false, errors.New("loop state path must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, false, nil
		}
		return State{}, false, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxStateBytes+1))
	if err != nil {
		return State{}, false, err
	}
	if len(raw) > MaxStateBytes {
		return State{}, false, fmt.Errorf("loop state file exceeds %d bytes", MaxStateBytes)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return State{}, false, nil
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		return State{}, false, err
	}
	return state, true, nil
}

func WriteState(path string, state State) error {
	if state.Version == 0 {
		state.Version = 1
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > MaxStateBytes {
		return fmt.Errorf("loop state file exceeds %d bytes", MaxStateBytes)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return errors.New("loop state path is a directory")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("loop state path must not be a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

func RecordProtocolFeed(protocolPath, mode string) (State, Event, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "digest"
	}
	loopDir := filepath.Dir(protocolPath)
	loopID := filepath.Base(loopDir)
	now := time.Now().UTC()
	statePath := filepath.Join(loopDir, StateFileName)
	state, found, err := ReadState(statePath)
	if err != nil {
		return State{}, Event{}, err
	}
	if !found {
		state = State{
			Version:      1,
			LoopID:       loopID,
			OwnerSession: loopID,
			Status:       "running",
			ProtocolPath: ProtocolRelPath(loopID),
			CreatedAt:    formatTime(now),
		}
	}
	if state.Version == 0 {
		state.Version = 1
	}
	if state.LoopID == "" {
		state.LoopID = loopID
	}
	if state.OwnerSession == "" {
		state.OwnerSession = loopID
	}
	if state.Status == "" {
		state.Status = "running"
	}
	if state.ProtocolPath == "" {
		state.ProtocolPath = ProtocolRelPath(loopID)
	}
	feedNumber := state.ProtocolFeeds + 1
	event, err := AppendEvent(filepath.Join(loopDir, EventsFileName), Event{
		Type:       "loop.protocol_feed",
		Summary:    "Fed LOOP.md " + mode,
		Reason:     "loop protocol feed policy",
		Path:       ProtocolRelPath(loopID),
		Mode:       mode,
		FeedNumber: feedNumber,
		Time:       formatTime(now),
	})
	if err != nil {
		return State{}, Event{}, err
	}
	state.ProtocolFeeds = feedNumber
	state.LastProtocolFeedAt = event.Time
	state.LastProtocolFeedMode = mode
	state.UpdatedAt = event.Time
	state.EventCount = event.Seq
	state.LastEventType = event.Type
	state.LastEventSummary = event.Summary
	state.LastEventAt = event.Time
	if err := WriteState(statePath, state); err != nil {
		return State{}, Event{}, err
	}
	return state, event, nil
}

func AppendEvent(path string, ev Event) (Event, error) {
	if strings.TrimSpace(ev.Type) == "" {
		return Event{}, errors.New("loop event type is required")
	}
	count, err := CountEvents(path)
	if err != nil {
		return Event{}, err
	}
	if ev.Seq == 0 {
		ev.Seq = count + 1
	}
	if ev.Time == "" {
		ev.Time = formatTime(time.Now())
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Event{}, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return Event{}, errors.New("loop events path is a directory")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return Event{}, errors.New("loop events path must not be a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Event{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return Event{}, err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(ev); err != nil {
		_ = f.Close()
		return Event{}, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return Event{}, err
	}
	if err := f.Close(); err != nil {
		return Event{}, err
	}
	syncDir(filepath.Dir(path))
	return ev, nil
}

func CountEvents(path string) (int, error) {
	events, count, _, err := readEvents(path, 0)
	_ = events
	return count, err
}

func ReadRecentEvents(path string, limit int) ([]Event, bool, error) {
	events, _, found, err := readEvents(path, limit)
	return events, found, err
}

func readEvents(path string, limit int) ([]Event, int, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, false, nil
		}
		return nil, 0, false, err
	}
	if info.IsDir() {
		return nil, 0, false, errors.New("loop events path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, 0, false, errors.New("loop events path must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, false, nil
		}
		return nil, 0, false, err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 64*1024)
	var out []Event
	count := 0
	for {
		line, err := readBoundedLine(reader, MaxEventLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, true, err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, 0, true, err
		}
		count++
		if limit <= 0 {
			continue
		}
		if len(out) < limit {
			out = append(out, ev)
		} else {
			copy(out, out[1:])
			out[len(out)-1] = ev
		}
	}
	return out, count, true, nil
}

func readBoundedLine(r *bufio.Reader, max int) ([]byte, error) {
	if max <= 0 {
		max = MaxEventLineBytes
	}
	var out []byte
	for {
		part, isPrefix, err := r.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) && len(out) > 0 {
				return out, nil
			}
			return nil, err
		}
		if len(out)+len(part) > max {
			return nil, fmt.Errorf("line exceeds %d bytes", max)
		}
		out = append(out, part...)
		if !isPrefix {
			return out, nil
		}
	}
}

func syncDir(path string) {
	if d, err := os.Open(path); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
