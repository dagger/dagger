package daggercmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/idtui"
)

func runHotlineTest(args []string, in io.Reader, out, errOut io.Writer) int {
	cmd := newHotlineCmd()
	cmd.GroupID = ""
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args[1:])
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	err := cmd.Execute()
	if err == nil {
		return 0
	}
	var exit idtui.ExitError
	if errors.As(err, &exit) {
		return exit.Code()
	}
	return 1
}

func TestHotlineInputAndDiscovery(t *testing.T) {
	t.Setenv("DAGGER_HOTLINE_MOCK", "")
	var out, errOut bytes.Buffer
	if code := runHotlineTest([]string{"hotline", "--help"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatal(code)
	}
	for _, name := range []string{"setup", "feedback", "question", "bug", "billing", "suggestion", "read", "reply", "attachments"} {
		if !strings.Contains(out.String(), name) {
			t.Fatalf("missing %s", name)
		}
	}
	for _, args := range [][]string{
		{"hotline", "--json", "question"},
		{"hotline", "--json", "question", "--bogus"},
		{"hotline", "--json", "reply", "bad", "--body", "test"},
		{"hotline", "--json", "list", "--mock-user", "team"},
	} {
		out.Reset()
		errOut.Reset()
		if code := runHotlineTest(args, strings.NewReader(""), &out, &errOut); code != 2 || out.Len() != 0 || !json.Valid(errOut.Bytes()) {
			t.Fatalf("%v: %d %s %s", args, code, &out, &errOut)
		}
	}
}
