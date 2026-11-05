package hotline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dagger/dagger/internal/replacefile"
	"github.com/gofrs/flock"
)

// MockRequest handles the Hotline wire protocol locally. It opens no socket.
// One file holds the demo state. A file lock covers the whole request.
func MockRequest(req *http.Request, filename, account string) (*http.Response, error) {
	p, err := DemoAccount(account)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return nil, fmt.Errorf("Cannot create the mock directory: %w", err)
	}
	lock := flock.New(filename + ".lock")
	defer lock.Close()
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()
	locked, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil || !locked {
		return nil, fmt.Errorf("Cannot lock the mock file. Try again.")
	}
	var state mockState
	data, err := os.ReadFile(filename)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("Cannot read the mock file: %w", err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			return nil, fmt.Errorf("Invalid mock file. Use a new file path; the current file was not changed.")
		}
	}
	w := httptest.NewRecorder()
	mockHandler(&state, p).ServeHTTP(w, req)
	if req.Method == http.MethodPost && w.Code == http.StatusOK {
		if err := state.save(filename); err != nil {
			return nil, fmt.Errorf("Cannot save the mock file: %w", err)
		}
	}
	return w.Result(), nil
}

// DemoAccount is used only by explicit local test modes.
func DemoAccount(account string) (Principal, error) {
	switch account {
	case "", "member":
		return Principal{OrgID: "demo", ActorID: "demo-member"}, nil
	case "admin":
		return Principal{OrgID: "demo", ActorID: "demo-admin", Billing: true}, nil
	case "team":
		return Principal{OrgID: "demo", ActorID: "demo-team", Team: true}, nil
	case "other":
		return Principal{OrgID: "other", ActorID: "demo-other"}, nil
	default:
		return Principal{}, problem(401, "unauthenticated", "Choose demo account member, admin, team, or other.")
	}
}

type mockThread struct {
	Thread
	OrgID string `json:"org_id"`
}

type mockMessage struct {
	Message
	RequestID string `json:"request_id"`
	Files     []File `json:"files,omitempty"`
}

type mockState struct {
	Threads  []mockThread  `json:"threads"`
	Messages []mockMessage `json:"messages"`
}

func (s *mockState) save(filename string) error {
	f, err := os.CreateTemp(filepath.Dir(filename), ".messages-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := json.NewEncoder(f).Encode(s); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return replacefile.Rename(f.Name(), filename)
}

func (s *mockState) thread(p Principal, id int64) (Thread, error) {
	for _, t := range s.Threads {
		if t.ID == id && t.OrgID == p.OrgID && (t.Kind != "billing" || canReadBilling(p)) {
			return t.Thread, nil
		}
	}
	return Thread{}, problem(404, "not_found", "Thread not found.")
}

func (s *mockState) Send(_ context.Context, p Principal, threadID int64, key string, in Input) (Sent, error) {
	if err := in.Validate(threadID == 0); err != nil {
		return Sent{}, err
	}
	if strings.TrimSpace(key) == "" || len(key) > 200 || strings.ContainsRune(key, 0) {
		return Sent{}, problem(400, "invalid_input", "Supply a request ID of 1–200 bytes without null characters.")
	}
	if threadID == 0 && in.Kind == "billing" && !canReadBilling(p) {
		return Sent{}, problem(403, "forbidden", "Billing requires an organization admin account.")
	}
	if threadID != 0 {
		if _, err := s.thread(p, threadID); err != nil {
			return Sent{}, err
		}
	}
	in.Attachments = slices.Clone(in.Attachments)
	slices.SortFunc(in.Attachments, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	first := map[int64]bool{}
	for _, m := range s.Messages {
		initial := !first[m.ThreadID]
		first[m.ThreadID] = true
		if m.AuthorID != p.ActorID || m.RequestID != key {
			continue
		}
		// Resolve the organization separately so keys on hidden billing threads still conflict.
		var t mockThread
		for _, candidate := range s.Threads {
			if candidate.ID == m.ThreadID {
				t = candidate
				break
			}
		}
		if t.OrgID != p.OrgID {
			continue
		}
		same := m.Body == in.Body && slices.EqualFunc(m.Files, in.Attachments, func(a, b File) bool { return a.Path == b.Path && bytes.Equal(a.Data, b.Data) })
		if threadID == 0 {
			same = same && initial && t.Kind == in.Kind && t.Outcome == in.Outcome && t.Workspace == in.Workspace
		} else {
			same = same && !initial && threadID == m.ThreadID
		}
		if t.Kind == "billing" && !canReadBilling(p) {
			same = false
		}
		if !same {
			return Sent{}, problem(409, "request_conflict", "This request ID was used with different input. Use a new request ID.")
		}
		return Sent{ThreadID: m.ThreadID, MessageID: m.ID, RequestID: key}, nil
	}
	now := time.Now().UTC()
	if threadID == 0 {
		threadID = int64(len(s.Threads) + 1)
		s.Threads = append(s.Threads, mockThread{Thread: Thread{ID: threadID, Kind: in.Kind, Outcome: in.Outcome, Workspace: in.Workspace, CreatedAt: now}, OrgID: p.OrgID})
	}
	m := Message{ID: int64(len(s.Messages) + 1), ThreadID: threadID, AuthorID: p.ActorID, Team: p.Team, Body: in.Body, CreatedAt: now}
	s.Messages = append(s.Messages, mockMessage{Message: m, RequestID: key, Files: in.Attachments})
	return Sent{ThreadID: threadID, MessageID: m.ID, RequestID: key}, nil
}

func (s *mockState) List(_ context.Context, p Principal) ([]Thread, error) {
	threads := []Thread{}
	for i := len(s.Threads) - 1; i >= 0 && len(threads) < 50; i-- {
		if t, err := s.thread(p, s.Threads[i].ID); err == nil {
			threads = append(threads, t)
		}
	}
	return threads, nil
}

func (s *mockState) Read(_ context.Context, p Principal, id, after int64) (Conversation, error) {
	c := Conversation{Messages: []Message{}}
	if after < 0 {
		return c, problem(400, "invalid_input", "Use a nonnegative message ID for --after.")
	}
	var err error
	c.Thread, err = s.thread(p, id)
	if err != nil {
		return c, err
	}
	for _, stored := range s.Messages {
		if stored.ThreadID != id || stored.ID <= after {
			continue
		}
		m := stored.Message
		for _, f := range stored.Files {
			m.Attachments = append(m.Attachments, Attachment{Path: f.Path, Size: len(f.Data)})
		}
		c.Messages = append(c.Messages, m)
		if len(c.Messages) == 100 {
			break
		}
	}
	return c, nil
}

func (s *mockState) Bundle(_ context.Context, p Principal, id int64) ([]byte, error) {
	for _, m := range s.Messages {
		if m.ID == id {
			if _, err := s.thread(p, m.ThreadID); err != nil {
				return nil, err
			}
			return bundleFiles(m.Files)
		}
	}
	return nil, problem(404, "not_found", "Attachment bundle not found.")
}

func canReadBilling(p Principal) bool { return p.Billing || p.Team }
