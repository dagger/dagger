package engineutil

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// TestScrubStringsRedactsSecret: a registered session-secret value appearing in an
// argv token is replaced with *** (the same trie-based censor the stdout/stderr
// scrubbers use), so a secret baked into a command never reaches the profile.
func TestScrubStringsRedactsSecret(t *testing.T) {
	out, err := ScrubStrings(
		[]string{"TOKEN=s3cr3t-value"}, []string{"TOKEN"}, nil,
		[]string{"curl", "-H", "Authorization: s3cr3t-value", "https://example.com"},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"curl", "-H", "Authorization: ***", "https://example.com"}
	if !slices.Equal(out, want) {
		t.Fatalf("scrub = %v, want %v", out, want)
	}
}

// TestScrubStringsNilInputsPassthrough: with no registered secrets (all inputs
// nil/empty) the strings pass through unchanged and nothing panics.
func TestScrubStringsNilInputsPassthrough(t *testing.T) {
	in := []string{"go", "build", "./..."}
	out, err := ScrubStrings(nil, nil, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out, in) {
		t.Fatalf("passthrough = %v, want %v", out, in)
	}
	if got, _ := ScrubStrings(nil, nil, nil, nil); got != nil {
		t.Fatalf("empty input must return nil, got %v", got)
	}
}

// TestBoundProfArgvPreservesKeyTokens: the bounds cap token count / per-token bytes
// / total bytes, NEVER truncating argv[0..1] (the group key), and append a sentinel
// no prefix rule can match.
func TestBoundProfArgvPreservesKeyTokens(t *testing.T) {
	// token-count overflow: 200 tokens → keep a prefix + 1 sentinel.
	argv := make([]string, 200)
	for i := range argv {
		argv[i] = fmt.Sprintf("a%d", i)
	}
	out := boundProfArgv(argv)
	if len(out) > maxProfArgvTokens+1 {
		t.Fatalf("bounded token count = %d, want <= %d", len(out), maxProfArgvTokens+1)
	}
	if out[0] != "a0" || out[1] != "a1" {
		t.Fatalf("argv[0..1] must be preserved, got %q %q", out[0], out[1])
	}
	last := out[len(out)-1]
	if !strings.HasPrefix(last, "…(+") {
		t.Fatalf("dropped-token sentinel missing/teachable to a prefix rule: %q", last)
	}

	// per-token byte cap applies past the key tokens; argv[0..1] are exempt.
	big := strings.Repeat("x", 1000)
	capped := boundProfArgv([]string{big, big, big})
	if len(capped[0]) != 1000 || len(capped[1]) != 1000 {
		t.Fatalf("argv[0..1] must not be byte-truncated, got lens %d %d", len(capped[0]), len(capped[1]))
	}
	if len(capped[2]) != maxProfArgvTokenBytes {
		t.Fatalf("argv[2] must be byte-capped to %d, got %d", maxProfArgvTokenBytes, len(capped[2]))
	}

	// the key tokens survive even when they alone blow the total budget.
	huge := strings.Repeat("y", maxProfArgvTotalBytes)
	keyOnly := boundProfArgv([]string{huge, huge, "dropme"})
	if len(keyOnly) < 2 || keyOnly[0] != huge || keyOnly[1] != huge {
		t.Fatal("argv[0..1] must always be retained, even over budget")
	}

	// short argv is returned unchanged.
	if got := boundProfArgv([]string{"go", "build"}); !slices.Equal(got, []string{"go", "build"}) {
		t.Fatalf("short argv altered: %v", got)
	}
}

// TestExecProfArgvEndToEnd: the emit-site helper scrubs then bounds the captured
// ProfArgs from a state's secret set; a nil execMD or empty ProfArgs emits nothing.
func TestExecProfArgvEndToEnd(t *testing.T) {
	state := &execState{
		spec: &specs.Spec{Process: &specs.Process{Env: []string{"PW=hunter2"}}},
		execMD: &ExecutionMetadata{
			SecretEnvNames: []string{"PW"},
			ProfArgs:       []string{"mysql", "--password=hunter2", "db"},
		},
	}
	got := execProfArgv(state)
	want := []string{"mysql", "--password=***", "db"}
	if !slices.Equal(got, want) {
		t.Fatalf("execProfArgv = %v, want %v", got, want)
	}

	if got := execProfArgv(&execState{execMD: nil}); got != nil {
		t.Fatalf("nil execMD must emit no argv, got %v", got)
	}
	if got := execProfArgv(&execState{execMD: &ExecutionMetadata{}}); got != nil {
		t.Fatalf("empty ProfArgs must emit no argv, got %v", got)
	}
}
