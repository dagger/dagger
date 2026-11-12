package daggercmd

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/internal/hotline"
)

type noNetwork struct{ t *testing.T }

func (n noNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	n.t.Error("Mock mode attempted a network request.")
	return nil, fmt.Errorf("network disabled")
}

func runMock(t *testing.T, want int, args ...string) []byte {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runHotlineTest(append([]string{"hotline", "--json"}, args...), strings.NewReader("From standard input"), &out, &errOut)
	if code != want {
		t.Fatalf("%v: exit %d; stdout=%s stderr=%s", args, code, &out, &errOut)
	}
	if want != 0 {
		return errOut.Bytes()
	}
	return out.Bytes()
}

func TestMockDemoWithoutNetwork(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	state := filepath.Join(root, "state", "messages.json")
	t.Setenv("DAGGER_HOTLINE_MOCK", state)
	t.Setenv("DAGGER_CLOUD_URL", "https://never-contact.invalid")
	t.Setenv("DAGGER_CLOUD_TOKEN", "do-not-use-this-token")
	transport := http.DefaultTransport
	http.DefaultTransport = noNetwork{t}
	t.Cleanup(func() { http.DefaultTransport = transport })
	if err := os.WriteFile("engine.log", []byte("Engine cannot start.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	create := []string{"new", "--body-file", "-", "--attach", "engine.log", "--request-id", "setup-1"}
	first := runMock(t, 0, create...)
	if again := runMock(t, 0, create...); !bytes.Equal(first, again) {
		t.Fatalf("retry changed IDs: %s / %s", first, again)
	}
	var sent hotline.Sent
	if err := json.Unmarshal(first, &sent); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(sent.ThreadID, 10)
	runMock(t, 0, "reply", id, "--mock-user", "team", "--body", "Is the runtime running?", "--request-id", "team-1")
	runMock(t, 0, "reply", id, "--mock-user", "team", "--body", "Is the runtime running?", "--request-id", "team-1")
	var conversation hotline.Conversation
	if err := json.Unmarshal(runMock(t, 0, "read", id), &conversation); err != nil {
		t.Fatal(err)
	}
	if len(conversation.Messages) != 2 || !conversation.Messages[1].Team || len(conversation.Messages[0].Attachments) != 1 {
		t.Fatalf("bad conversation: %+v", conversation)
	}
	if err := json.Unmarshal(runMock(t, 0, "read", id, "--after", strconv.FormatInt(sent.MessageID, 10)), &conversation); err != nil {
		t.Fatal(err)
	}
	if len(conversation.Messages) != 1 || !conversation.Messages[0].Team {
		t.Fatal("incremental read failed")
	}
	// Each Run reloads the file. The bundle must keep its original bytes.
	os.WriteFile("engine.log", []byte("Changed after sending."), 0600)
	runMock(t, 0, "attachments", strconv.FormatInt(sent.MessageID, 10), "--output", "bundle.zip")
	z, err := zip.OpenReader("bundle.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	f, err := z.Open("engine.log")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(data) != "Engine cannot start.\n" {
		t.Fatalf("bad bundle: %q %v", data, err)
	}
	runMock(t, 2, "attachments", strconv.FormatInt(sent.MessageID, 10), "--output", "bundle.zip")
	runMock(t, 1, create...) // changed file under the same key
	runMock(t, 1, "read", id, "--mock-user", "other")
	runMock(t, 1, "reply", id, "--mock-user", "other", "--body", "No access")
	runMock(t, 1, "attachments", strconv.FormatInt(sent.MessageID, 10), "--mock-user", "other", "--output", "denied.zip")
	data, err = os.ReadFile(state)
	if err != nil || !json.Valid(data) || bytes.Contains(data, []byte("do-not-use-this-token")) {
		t.Fatalf("bad saved state: %v", err)
	}
	info, err := os.Stat(state)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("mock file must be private")
	}
}

func TestMockConcurrentCommands(t *testing.T) {
	t.Setenv("DAGGER_HOTLINE_MOCK", filepath.Join(t.TempDir(), "messages.json"))
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() { runMock(t, 0, "new", "--body", "One question", "--request-id", "same") })
	}
	wg.Wait()
	var page hotline.ThreadPage
	json.Unmarshal(runMock(t, 0, "list"), &page)
	if len(page.Threads) != 1 {
		t.Fatalf("expected one thread, got %d", len(page.Threads))
	}
	for i := range 12 {
		wg.Go(func() { runMock(t, 0, "reply", "1", "--body", fmt.Sprint(i), "--request-id", fmt.Sprint(i)) })
	}
	wg.Wait()
	var c hotline.Conversation
	json.Unmarshal(runMock(t, 0, "read", "1"), &c)
	if len(c.Messages) != 13 {
		t.Fatalf("lost messages: %d", len(c.Messages))
	}
}

func TestMockRejectsDamagedState(t *testing.T) {
	state := filepath.Join(t.TempDir(), "messages.json")
	t.Setenv("DAGGER_HOTLINE_MOCK", state)
	os.WriteFile(state, []byte("broken"), 0600)
	runMock(t, 2, "new", "--body", "Do not overwrite")
	data, _ := os.ReadFile(state)
	if string(data) != "broken" {
		t.Fatal("damaged data was overwritten")
	}
}

func TestHotlineUnreadLifecycle(t *testing.T) {
	t.Setenv("DAGGER_HOTLINE_MOCK", filepath.Join(t.TempDir(), "messages.json"))
	runMock(t, 0, "new", "--body", "Help")
	runMock(t, 0, "reply", "1", "--mock-user", "team", "--body", "First reply")
	runMock(t, 0, "reply", "1", "--mock-user", "team", "--body", "Second reply")
	inbox := func(account string) hotline.ThreadPage {
		t.Helper()
		var page hotline.ThreadPage
		if err := json.Unmarshal(runMock(t, 0, "inbox", "--mock-user", account), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	if p := inbox("member"); len(p.Threads) != 1 || p.Threads[0].UnreadCount != 2 {
		t.Fatalf("inbox: %+v", p)
	}
	runMock(t, 0, "read", "1", "--unread", "--peek")
	if p := inbox("member"); p.Threads[0].UnreadCount != 2 {
		t.Fatal("peek marked messages read")
	}
	var c hotline.Conversation
	json.Unmarshal(runMock(t, 0, "read", "1", "--unread", "--limit", "1"), &c)
	if len(c.Messages) != 1 || c.NextCursor != 2 {
		t.Fatalf("page: %+v", c)
	}
	if p := inbox("member"); p.Threads[0].UnreadCount != 1 {
		t.Fatalf("read cleared wrong messages: %+v", p)
	}
	var errOut bytes.Buffer
	if code := runHotlineTest([]string{"hotline", "read", "1"}, strings.NewReader(""), brokenOutput{}, &errOut); code == 0 {
		t.Fatal("output error was ignored")
	}
	if p := inbox("member"); p.Threads[0].UnreadCount != 1 {
		t.Fatal("failed output marked messages read")
	}
	runMock(t, 0, "read", "1", "--unread")
	if p := inbox("member"); len(p.Threads) != 0 {
		t.Fatalf("read did not clear inbox: %+v", p)
	}
	if p := inbox("admin"); len(p.Threads) != 1 || p.Threads[0].UnreadCount != 3 {
		t.Fatalf("accounts share cursor: %+v", p)
	}
}

type brokenOutput struct{}

func (brokenOutput) Write([]byte) (int, error) { return 0, fmt.Errorf("output closed") }

func TestHotlineWatch(t *testing.T) {
	file := filepath.Join(t.TempDir(), "messages.json")
	t.Setenv("DAGGER_HOTLINE_MOCK", file)
	runMock(t, 0, "new", "--body", "Help")
	runMock(t, 0, "reply", "1", "--mock-user", "team", "--body", "Reply")
	var out bytes.Buffer
	a := &hotlineCLI{mockFile: file, json: true, out: &out}
	cmd := newHotlineCmd()
	cmd.SetContext(t.Context())
	seen := map[int64]int64{}
	if err := a.watchPoll(cmd, seen, false); err != nil {
		t.Fatal(err)
	}
	var thread hotline.Thread
	if err := json.Unmarshal(out.Bytes(), &thread); err != nil || thread.ID != 1 || thread.UnreadCount != 1 {
		t.Fatalf("notification: %s %v", &out, err)
	}
	out.Reset()
	runMock(t, 0, "reply", "1", "--body", "My reply")
	if err := a.watchPoll(cmd, seen, false); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatal("own reply caused a duplicate notification")
	}
	runMock(t, 0, "reply", "1", "--mock-user", "team", "--body", "Another reply")
	if err := a.watchPoll(cmd, seen, false); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &thread); err != nil || thread.UnreadCount != 2 {
		t.Fatalf("watch marked messages read: %s %v", &out, err)
	}
}

func TestHotlineEditorDraftRecovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Editor fixture requires a Unix shell.")
	}
	root := t.TempDir()
	t.Setenv("DAGGER_HOTLINE_MOCK", filepath.Join(root, "messages.json"))
	editor := filepath.Join(root, "test editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf 'Draft from editor' > \"$1\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", "'"+editor+"'")
	runMock(t, 0, "new", "--body", "Help")
	result := runMock(t, 1, "reply", "1", "--mock-user", "other", "--editor")
	var failed struct {
		Error struct {
			Draft     string `json:"draft"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(result, &failed); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(failed.Error.Draft)
	draft, err := os.ReadFile(failed.Error.Draft)
	if err != nil || string(draft) != "Draft from editor" || failed.Error.RequestID == "" {
		t.Fatalf("missing recovery data: %s %v", result, err)
	}
	info, err := os.Stat(failed.Error.Draft)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("draft is not private")
	}
	runMock(t, 0, "reply", "1", "--body-file", failed.Error.Draft, "--request-id", failed.Error.RequestID)
	var out, errOut bytes.Buffer
	cmd := newHotlineCmd()
	cmd.GroupID = ""
	cmd.SetArgs([]string{"reply", "1", "--editor"})
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("editor send: %v %s", err, &errOut)
	}
}

func TestHotlineWatchStops(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a := &hotlineCLI{mockFile: filepath.Join(t.TempDir(), "messages.json"), out: io.Discard, errOut: io.Discard}
	cmd := a.watchCommand()
	cmd.SetContext(ctx)
	done := make(chan error, 1)
	go func() { done <- cmd.RunE(cmd, nil) }()
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("watch did not stop on cancellation")
	}
}

func TestHotlineTeamQueue(t *testing.T) {
	file := filepath.Join(t.TempDir(), "messages.json")
	t.Setenv("DAGGER_HOTLINE_MOCK", file)
	runMock(t, 0, "new", "--body", "First customer")
	runMock(t, 0, "new", "--mock-user", "other", "--body", "Second customer")
	runMock(t, 3, "queue")
	runMock(t, 2, "queue", "--mock-user", "team", "--org", "demo")
	var page hotline.ThreadPage
	if err := json.Unmarshal(runMock(t, 0, "queue", "--mock-user", "team", "--limit", "1"), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Threads) != 1 || page.Threads[0].ID != 2 || page.Threads[0].OrgName != "other" || page.NextCursor == 0 {
		t.Fatalf("queue: %+v", page)
	}
	if err := json.Unmarshal(runMock(t, 0, "queue", "--mock-user", "team", "--after", fmt.Sprint(page.NextCursor)), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Threads) != 1 || page.Threads[0].ID != 1 {
		t.Fatalf("queue page: %+v", page)
	}
	runMock(t, 0, "read", "2", "--mock-user", "team")
	var out bytes.Buffer
	a := &hotlineCLI{mockFile: file, demo: "team", json: true, out: &out}
	cmd := newHotlineCmd()
	cmd.SetContext(t.Context())
	seen := map[int64]int64{}
	if err := a.watchPoll(cmd, seen, true); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[2] != 2 {
		t.Fatal("queue watch omitted a read thread")
	}
	out.Reset()
	runMock(t, 0, "reply", "2", "--mock-user", "team", "--body", "Team reply", "--request-id", "team-reply")
	runMock(t, 0, "reply", "2", "--mock-user", "team", "--body", "Team reply", "--request-id", "team-reply")
	if err := a.watchPoll(cmd, seen, true); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("team reply caused a notification: %s", &out)
	}
	if err := json.Unmarshal(runMock(t, 0, "queue", "--mock-user", "team"), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Threads) != 1 || page.Threads[0].ID != 1 {
		t.Fatalf("answered thread remains: %+v", page)
	}
	runMock(t, 0, "reply", "2", "--mock-user", "other", "--body", "Follow-up")
	if err := a.watchPoll(cmd, seen, true); err != nil {
		t.Fatal(err)
	}
	var thread hotline.Thread
	if err := json.Unmarshal(out.Bytes(), &thread); err != nil || thread.ID != 2 {
		t.Fatalf("missing follow-up: %s %v", &out, err)
	}
	runMock(t, 1, "read", "2")
}
