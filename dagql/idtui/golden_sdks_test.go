package idtui_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// sdkFixture is a module of viztest/sdks, written with one of the external,
// entrypoint-style SDKs (github.com/dagger/<lang>-sdk) rather than the
// engine's built-in runtimes. Every fixture has the same functions, so the
// goldens of one case line up across SDKs; see viztest/sdks/go/main.go.
//
// These goldens record what users see TODAY, warts and all: they are meant to
// change as error bubbling and log propagation through module entrypoints are
// fixed.
type sdkFixture struct {
	lang string
	// a source file of the module, and code that, appended to it, breaks the
	// module's build or load
	source, breakage string
	// the SDK runs no process of the function's own: it can't exit with a
	// code, and logs only to stdout
	noProcess bool
	// BAD: the function's own stdout breaks the call, so log-success fails
	logBreaksCall bool
	// BAD: the SDK has no function cache policy, so log-success is cached
	// and its logs are never seen again
	noCachePolicy bool
	// functions whose expanded golden is flaky, and why: their dispatcher
	// writes to stdout and stderr at once, and the function's row shows the
	// two streams in whichever order they arrive
	racy map[string]string
}

const (
	racyReply      = "the dispatcher's reply on stdout races its stack trace on stderr"
	racyQueryError = "the dispatcher writes an API error's stdout and stderr at once"
)

// What every entrypoint SDK gets wrong today:
//
//   - BAD: the dispatcher exec's stdout and stderr, the function's own logs
//     with stack traces and protocol lines (::dagger-function-error::, a
//     reply's JSON) among them, render on the function's row, which a
//     collapsed report doesn't expand. The exec an error's origin names
//     renders with "exit code: N" and no logs.
//   - BAD: a caught and wrapped exec error loses the wrapping message in the
//     collapsed report: the origin is the inner withExec.
//   - BAD: an entrypoint's error reads "call module entrypoint: ...", and
//     across a module boundary a dependency's failed exec arrives as
//     "call module entrypoint: exit code: 1".
//   - BAD: an exec error's message is a bare "exit code: N".
var sdkFixtures = []sdkFixture{
	// Errors use the ::dagger-function-error:: marker. A build failure renders
	// the compiler's error, through dagger/go's own build.
	{lang: "go", source: "main.go", breakage: "\nvar _ = undefinedValue\n"},
	// BAD: no error marker yet (dagger/python-sdk#93): every failure, a
	// syntax error included, is the dispatcher's "exit code: 2", without its
	// message or logs.
	{lang: "python", source: "src/viztest_python/__init__.py", breakage: "\nthis is not python\n"},
	// BAD: the dispatcher replies on stdout, which the function's console.log
	// shares: the reply no longer decodes, so log-success fails, and a
	// function that logs and throws loses its message to "exit code: 2". A
	// syntax error is the dispatcher's "exit code: 1", without the error.
	{lang: "typescript", source: "src/index.ts", breakage: "\nthis is not typescript\n", logBreaksCall: true, racy: map[string]string{
		"log-success":       racyReply,
		"fail-native":       racyReply,
		"fail-exec":         racyReply,
		"fail-exec-wrapped": racyReply,
		"call-failing-dep":  racyReply,
	}},
	// BAD: no error marker: the entrypoint raises the dispatcher's whole
	// stderr, so the error message is the function's own stderr lines and a
	// stack trace, and an exit with a code reads "php function stderr". The
	// client sends every default argument, so a withExec renders with all of
	// them.
	{lang: "php", source: "src/ViztestPhp.php", breakage: "\nthis is not php;\n", noCachePolicy: true, racy: map[string]string{
		"fail-exec": racyQueryError,
	}},
	// Errors use the ::dagger-function-error:: marker. BAD: a build failure
	// is the trace 150bc33a shape: maven's withExec, "exit code: 1", and none
	// of the compiler's output.
	{lang: "java", source: "src/main/java/io/dagger/modules/viztestjava/ViztestJava.java", breakage: "\nthis is not java\n"},
	// Not an entrypoint: the dang-sdk module runs on the engine's built-in
	// Dang runtime, so its errors and logs are the reference. BAD: the
	// collapsed report renders a native error twice.
	{lang: "dang", source: "main.dang", breakage: "\nthis is not dang\n", noProcess: true},
}

// logSuccessOutput checks a log-success run. Fuzzy, because the function's
// result is rendered as a line of its row's logs, and races them there: it
// arrives before or after the exec's logs, and lacks a newline of its own,
// so it can run into the next line ("go function resultgo function stdout").
func (fx sdkFixture) logSuccessOutput(t *testctx.T, out string) {
	result := fx.lang + " function result"
	require.Contains(t, out, "Expected stdout:\n\n"+result+"\n")
	_, row, ok := strings.Cut(out, "logSuccess: String! ")
	require.True(t, ok, "the report must show the function's row")
	row, _, _ = strings.Cut(row, "\n\n")
	if fx.noCachePolicy {
		require.True(t, strings.HasPrefix(row, "X.Xs CACHED\n"), "the cached function's row: %s", row)
		require.NotContains(t, row, fx.lang+" function stdout")
		return
	}
	require.True(t, strings.HasPrefix(row, "X.Xs\n"), "the function's row: %s", row)
	require.Contains(t, row, result)
	require.Contains(t, row, fx.lang+" function stdout")
	if !fx.noProcess {
		require.Contains(t, row, fx.lang+" function stderr")
	}
}

// sdkCase is one function every SDK fixture implements.
type sdkCase struct {
	function string
	fail     bool
}

var sdkCases = []sdkCase{
	// Logs to stdout and stderr, and succeeds: where do a function's own logs
	// land? On its row, with its result rendered among them.
	{function: "log-success"},
	// Logs to stdout and stderr, then fails with an error of its own. BAD:
	// collapsed, the error's origin is "call module entrypoint", and the logs
	// on the function's row are not shown.
	{function: "fail-native", fail: true},
	// Logs to stdout and stderr, then exits its process with code 3 instead
	// of returning an error. BAD: the origin is the dispatcher exec, with
	// "exit code: 3" and none of the logs.
	{function: "fail-exit-code", fail: true},
	// Runs a withExec that logs to stdout and stderr and exits 1, and lets the
	// error propagate unhandled. Collapsed, the inner withExec is the origin,
	// with its logs: the best case, except where the SDK loses it (python).
	{function: "fail-exec", fail: true},
	// The same exec, its error caught and wrapped in a message of the
	// function's own, the common real-world pattern. BAD: collapsed, the
	// wrapping message is nowhere.
	{function: "fail-exec-wrapped", fail: true},
	// Calls viztest/sdks/dep (a go-sdk module) whose withExec fails: how the
	// error, its origin and its logs survive a module boundary. Same-SDK for
	// go, cross-SDK for the rest. BAD: expanded, the caller's message is
	// "call module entrypoint: exit code: 1".
	{function: "call-failing-dep", fail: true},
}

// sdkExamples are the golden examples of every SDK fixture: each failing case
// as the suite renders it (expanded, -v) and as a user sees it by default
// (collapsed, quiet), plus a module whose build or load fails before any
// function runs, like trace 150bc33a, where a go-sdk module's `go build`
// failed and the user saw only "call module entrypoint: ... exit code: 1",
// with no logs on the failed exec.
func sdkExamples() []Example {
	var examples []Example
	for _, fx := range sdkFixtures {
		module := "./viztest/sdks/" + fx.lang
		for _, c := range sdkCases {
			if fx.noProcess && c.function == "fail-exit-code" {
				continue
			}
			ex := Example{
				Name:         "sdks-" + fx.lang + "-" + c.function,
				Golden:       "sdks/" + fx.lang + "/" + c.function,
				Module:       module,
				Function:     c.function,
				Fail:         c.fail,
				Flaky:        fx.racy[c.function],
				NormalizeSDK: true,
			}
			if c.function == "log-success" {
				if fx.logBreaksCall {
					c.fail, ex.Fail = true, true
				} else {
					ex.FuzzyTest = fx.logSuccessOutput
				}
			}
			examples = append(examples, ex)
			if c.fail {
				examples = append(examples, Example{
					Name:         "sdks-" + fx.lang + "-" + c.function + "-default",
					Golden:       "sdks/" + fx.lang + "/" + c.function + "-default",
					Module:       module,
					Function:     c.function,
					Fail:         true,
					NoExpand:     true,
					Quiet:        true,
					NormalizeSDK: true,
				})
			}
		}

		// Only as a user sees it by default: expanded, the failed build
		// renders the SDK's whole build pipeline, thousands of lines of
		// another module's internals.
		broken := "./viztest/sdks/broken-" + fx.lang
		examples = append(examples, Example{
			Name:         "sdks-" + fx.lang + "-broken-default",
			Golden:       "sdks/" + fx.lang + "/broken-default",
			Module:       broken,
			Function:     "log-success",
			Fail:         true,
			NoExpand:     true,
			Quiet:        true,
			NormalizeSDK: true,
			Setup:        brokenSDKFixture(module, broken, fx.source, fx.breakage),
		})
	}
	return examples
}

// dangSourceDir matches the directory the engine copies a Dang module's
// source to, named for the module and a digest of its source.
var dangSourceDir = regexp.MustCompile(`(/tmp/dagger-dang/[\w.-]+)-[0-9a-f]{32}/`)

// normalizeSDKOutput replaces every mention of the engine's native platform,
// and of its architecture as GOARCH, so goldens that render an SDK's build for
// the default platform are architecture-independent, and the digests in Dang
// source paths, so they don't change with every edit of a fixture. With
// expanded, it also elides what the module entrypoint evaluates; see
// elideEntrypointInternals.
func normalizeSDKOutput(out, nativePlatform string, expanded bool) string {
	if expanded {
		out = elideEntrypointInternals(out)
	}
	out = dangSourceDir.ReplaceAllString(out, "$1-XXXXXXXX/")
	if nativePlatform == "" {
		return out
	}
	replacements := []string{fmt.Sprintf("%q", nativePlatform), `"linux/ARCH"`}
	if _, arch, ok := strings.Cut(nativePlatform, "/"); ok {
		arch, _, _ = strings.Cut(arch, "/")
		replacements = append(replacements, "GOARCH="+arch, "GOARCH=ARCH")
	}
	return strings.NewReplacer(replacements...).Replace(out)
}

// entrypointInternalsElided replaces what elideEntrypointInternals leaves
// out.
const entrypointInternalsElided = "┆ (the entrypoint's own evaluation, elided)"

// elideEntrypointInternals leaves out the children of every nested "call
// module entrypoint" span of an expanded report: the SDK's build and
// dispatcher exec, evaluated concurrently by its Dang entrypoint, so their
// order and grouping vary from run to run. The span's own error and logs stay.
// What a user sees by default is pinned by the collapsed goldens, which these
// spans never reach.
func elideEntrypointInternals(out string) string {
	lines := strings.SplitAfter(out, "\n")
	var kept []string
	for i := 0; i < len(lines); i++ {
		kept = append(kept, lines[i])
		col, ok := entrypointCallColumn(lines[i])
		if !ok {
			continue
		}
		var (
			elided  bool
			inError bool
			pending []string // lines of only tree guides, kept if the subtree ends
			guide   string   // the prefix of the subtree's lines
		)
		for i+1 < len(lines) {
			line := []rune(strings.TrimSuffix(lines[i+1], "\n"))
			if len(line) > col && !onlyGuides(line[:col]) {
				break // the next sibling or an ancestor's
			}
			i++
			if len(line) <= col {
				pending = append(pending, lines[i])
				continue
			}
			switch {
			case line[col] == '!':
				inError = true
			case inError && line[col] == ' ' && len(line) > col+2 && line[col+1] == ' ' && line[col+2] != ' ':
				// the error's next line
			case line[col] == '┃':
				inError = false
			default:
				inError = false
				if !elided {
					guide = string(line[:col])
				}
				elided = true
				pending = nil
				continue
			}
			kept = append(kept, pending...)
			pending = nil
			kept = append(kept, lines[i])
		}
		if elided {
			kept = append(kept, guide+entrypointInternalsElided+"\n")
		}
		kept = append(kept, pending...)
	}
	return strings.Join(kept, "")
}

// entrypointCallColumn returns the column of a nested "call module
// entrypoint" span's children, if the line is one.
func entrypointCallColumn(line string) (int, bool) {
	before, _, ok := strings.Cut(line, "call module entrypoint X.")
	if !ok {
		return 0, false
	}
	prefix := []rune(before)
	// "├╴✘ " or "╰╴✔ " ends the prefix of a nested span
	if len(prefix) < 4 || prefix[len(prefix)-1] != ' ' || prefix[len(prefix)-3] != '╴' {
		return 0, false
	}
	if !onlyGuides(prefix[:len(prefix)-4]) {
		return 0, false
	}
	return len(prefix) - 2, true
}

func TestElideEntrypointInternals(t *testing.T) {
	require.Equal(t, `$ viztestGo: ViztestGo! X.Xs CACHED
✘ .failExec: String! X.Xs ERROR
┃ exit code: 1
├╴✘ call module entrypoint X.Xs ERROR
│ ! call module entrypoint: first line
│   second line
│ ┆ (the entrypoint's own evaluation, elided)
│
├╴✘ withExec sh -c 'exit 1' X.Xs ERROR
│ ! exit code: 1
╰╴✘ .callDep: String! X.Xs ERROR
  ╰╴✘ call module entrypoint X.Xs ERROR
    ┆ (the entrypoint's own evaluation, elided)

✘ call module entrypoint X.Xs ERROR
! collapsed, so left alone
`, elideEntrypointInternals(`$ viztestGo: ViztestGo! X.Xs CACHED
✘ .failExec: String! X.Xs ERROR
┃ exit code: 1
├╴✘ call module entrypoint X.Xs ERROR
│ ! call module entrypoint: first line
│   second line
│ ├╴✔ defaultPlatform: Platform! X.Xs
│ │ ┃ "linux/ARCH"
│ │
│ ╰╴✘ .contents: String! X.Xs ERROR
│
├╴✘ withExec sh -c 'exit 1' X.Xs ERROR
│ ! exit code: 1
╰╴✘ .callDep: String! X.Xs ERROR
  ╰╴✘ call module entrypoint X.Xs ERROR
    ├╴$ go: Go! X.Xs CACHED
    ╰╴✘ withExec dispatch X.Xs ERROR
      ! exit code: 2

✘ call module entrypoint X.Xs ERROR
! collapsed, so left alone
`))
}

func onlyGuides(rs []rune) bool {
	for _, r := range rs {
		if r != ' ' && r != '│' {
			return false
		}
	}
	return true
}

// brokenSDKFixture copies an SDK fixture to dst, with breakage appended to
// one of its source files. The fixtures' generated code is large, so the
// broken copies are made by the test rather than committed.
func brokenSDKFixture(src, dst, source, breakage string) func(*testctx.T) {
	return func(t *testctx.T) {
		exec.Command("rm", "-rf", dst).Run()
		t.Cleanup(func() { exec.Command("rm", "-rf", dst).Run() })
		if out, err := exec.Command("cp", "-R", src, dst).CombinedOutput(); err != nil {
			t.Fatalf("copy %s to %s: %v: %s", src, dst, err, out)
		}
		f, err := os.OpenFile(filepath.Join(dst, source), os.O_APPEND|os.O_WRONLY, 0)
		require.NoError(t, err)
		_, err = f.WriteString(breakage)
		require.NoError(t, err)
		require.NoError(t, f.Close())
	}
}
