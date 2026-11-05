package daggercmd

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

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
	create := []string{"setup", "--outcome", "blocked", "--body-file", "-", "--attach", "engine.log", "--request-id", "setup-1"}
	first := runMock(t, 0, create...)
	if again := runMock(t, 0, create...); !bytes.Equal(first, again) {
		t.Fatalf("retry changed IDs: %s / %s", first, again)
	}
	var sent hotline.Sent
	if err := json.Unmarshal(first, &sent); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(sent.ThreadID, 10)
	for _, kind := range []string{"feedback", "question", "bug", "suggestion"} {
		runMock(t, 0, kind, "--body", "Demo message")
	}
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
	runMock(t, 3, "billing", "--body", "Invoice")
	var bill hotline.Sent
	json.Unmarshal(runMock(t, 0, "billing", "--mock-user", "admin", "--body", "Invoice"), &bill)
	runMock(t, 1, "read", strconv.FormatInt(bill.ThreadID, 10))
	runMock(t, 0, "read", strconv.FormatInt(bill.ThreadID, 10), "--mock-user", "team")
	var threads []hotline.Thread
	json.Unmarshal(runMock(t, 0, "list"), &threads)
	if len(threads) != 5 {
		t.Fatalf("expected five visible threads, got %d", len(threads))
	}
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
		wg.Go(func() { runMock(t, 0, "question", "--body", "One question", "--request-id", "same") })
	}
	wg.Wait()
	var threads []hotline.Thread
	json.Unmarshal(runMock(t, 0, "list"), &threads)
	if len(threads) != 1 {
		t.Fatalf("expected one thread, got %d", len(threads))
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
	runMock(t, 2, "question", "--body", "Do not overwrite")
	data, _ := os.ReadFile(state)
	if string(data) != "broken" {
		t.Fatal("damaged data was overwritten")
	}
}
