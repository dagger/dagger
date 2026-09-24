package core

// This file is deliberately self-contained so the exact workload can be copied
// to main. Run alone, twice in separate engine-dev test invocations: each creates
// random engine state and /run cache volumes (not merely a fresh SDK session).
// Engine/CLI builds are outside timings. No latency or native-path gates apply.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

const realBenchFixtureSHA = "06eca9957aa99cdf55855110846e8ee2546ef306"
const realBenchDate = "2026-01-01T00:00:00Z"

func realBenchLog(t *testing.T, value any) {
	t.Helper()
	b, err := json.Marshal(value)
	require.NoError(t, err)
	t.Logf("REALBENCH %s", b)
}

// Do not change the parent's environment: other integration tests are parallel.
func realBenchPrivateSession(t *testing.T) bool {
	t.Helper()
	if _, inherited := os.LookupEnv("DAGGER_SESSION_PORT"); !inherited {
		return false
	}
	require.NotEmpty(t, os.Getenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST"))
	require.NotEmpty(t, os.Getenv("_EXPERIMENTAL_DAGGER_CLI_BIN"))
	bin, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(t.Context(), bin, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.v", "-test.count=1", "-test.timeout=30m")
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "DAGGER_SESSION_PORT=") && !strings.HasPrefix(env, "DAGGER_SESSION_TOKEN=") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	// Stream samples so a failed/timed-out run still leaves reproducible logs.
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	require.NoError(t, cmd.Run())
	return true
}

type realBenchTrace struct {
	mu    sync.Mutex
	spans map[string]*tracepb.Span
}

func realBenchTraceServer(t *testing.T) (*realBenchTrace, *httptest.Server) {
	t.Helper()
	sink := &realBenchTrace{spans: map[string]*tracepb.Span{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var req coltracepb.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		sink.mu.Lock()
		defer sink.mu.Unlock()
		for _, resource := range req.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.EndTimeUnixNano > span.StartTimeUnixNano {
						sink.spans[string(span.TraceId)+string(span.SpanId)] = span
					}
				}
			}
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(server.Close)
	return sink, server
}

// Diagnostic only: no requirement that main execute PR-only native operations.
func (sink *realBenchTrace) report(t *testing.T) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	ordered := make([]*tracepb.Span, 0, len(sink.spans))
	for _, span := range sink.spans {
		ordered = append(ordered, span)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].StartTimeUnixNano < ordered[j].StartTimeUnixNano })
	counts := map[string]int{}
	for _, span := range ordered {
		ancestors := []string{}
		duplicate := false
		for p := sink.spans[string(span.TraceId)+string(span.ParentSpanId)]; p != nil; p = sink.spans[string(p.TraceId)+string(p.ParentSpanId)] {
			ancestors = append(ancestors, p.Name)
			if p.Name == span.Name {
				duplicate = true
			}
		}
		if duplicate {
			continue
		}
		fetch := strings.HasPrefix(span.Name, "git fetch") || strings.HasPrefix(span.Name, "fetching ")
		interesting := fetch
		switch span.Name {
		case "Workspace.withCommit", "Changeset.__mergeWithChangeset", "git commit", "git commit-tree", "git native commit transaction", "git native workspace merge", "materialize incremental git checkout", "materialize local git checkout", "GitRef.log":
			interesting = true
		}
		if !interesting {
			continue
		}
		counts[span.Name]++
		if fetch {
			counts["fetches"]++
			for _, a := range ancestors {
				if a == "Workspace.withCommit" {
					counts["fetches_under_commit"]++
					break
				}
			}
		}
		if span.Name == "git commit-tree" {
			for _, a := range ancestors {
				if a == "git native commit transaction" {
					counts["actual_native_history_writes"]++
					break
				}
				if a == "git native workspace merge" {
					counts["scratch_merge_commits"]++
					break
				}
			}
		}
		attrs := map[string]any{}
		for _, a := range span.Attributes {
			if strings.HasPrefix(a.Key, "dagger.git.") {
				attrs[a.Key] = a.Value
			}
		}
		realBenchLog(t, map[string]any{"kind": "span", "name": span.Name, "trace": hex.EncodeToString(span.TraceId), "span": hex.EncodeToString(span.SpanId), "ms": float64(span.EndTimeUnixNano-span.StartTimeUnixNano) / 1e6, "attrs": attrs})
	}
	realBenchLog(t, map[string]any{"kind": "trace_counts", "counts": counts, "completed_spans": len(sink.spans)})
}

type realBenchPaths struct{ Modified, Added, Removed []string }

func realBenchStatus(ctx context.Context, t *testing.T, delta *dagger.Changeset, expected realBenchPaths) {
	t.Helper()
	modified, err := delta.ModifiedPaths(ctx)
	require.NoError(t, err)
	added, err := delta.AddedPaths(ctx)
	require.NoError(t, err)
	removed, err := delta.RemovedPaths(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, expected.Modified, modified)
	require.ElementsMatch(t, expected.Added, added)
	require.ElementsMatch(t, expected.Removed, removed)
}

func TestWorkspaceRealRepositoryPerformance(t *testing.T) {
	if realBenchPrivateSession(t) {
		return
	}
	ctx := t.Context()
	runner, cli := os.Getenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST"), os.Getenv("_EXPERIMENTAL_DAGGER_CLI_BIN")
	require.NotEmpty(t, runner, "explicit from-source engine required")
	require.NotEmpty(t, cli, "explicit from-source CLI required")
	require.Empty(t, os.Getenv("DAGGER_SESSION_PORT"))
	cliVersion, err := exec.CommandContext(ctx, cli, "version").CombinedOutput()
	require.NoError(t, err, "%s", cliVersion)
	realBenchLog(t, map[string]any{"kind": "target", "runner": runner, "cli": cli, "cli_version": strings.TrimSpace(string(cliVersion)), "fixture_sha": realBenchFixtureSHA})

	started := time.Now()
	checkout := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = checkout
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_DATE="+realBenchDate, "GIT_COMMITTER_DATE="+realBenchDate)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "benchmark")
	for _, setting := range [][2]string{{"user.name", "Real Benchmark"}, {"user.email", "realbench@example.com"}, {"commit.gpgsign", "false"}, {"core.hooksPath", "/dev/null"}, {"gc.auto", "0"}, {"pack.threads", "1"}} {
		git("config", setting[0], setting[1])
	}
	// Full ancestry of exactly the pinned commit, no shallow/partial clone, tags,
	// mutable branch tips or remotes. Capture must copy this local history.
	git("fetch", "--no-tags", "https://github.com/dagger/dagger.git", realBenchFixtureSHA)
	git("reset", "--hard", realBenchFixtureSHA)
	git("repack", "-adf", "--window=10", "--depth=50")
	git("prune-packed")
	require.Equal(t, "false", git("rev-parse", "--is-shallow-repository"))
	require.Empty(t, git("remote"))
	require.Equal(t, realBenchFixtureSHA, git("rev-parse", "HEAD"))
	packs, err := filepath.Glob(filepath.Join(checkout, ".git/objects/pack/*.pack"))
	require.NoError(t, err)
	packHashes := map[string]string{}
	for _, path := range packs {
		f, err := os.Open(path)
		require.NoError(t, err)
		h := sha256.New()
		_, err = io.Copy(h, f)
		require.NoError(t, err)
		require.NoError(t, f.Close())
		packHashes[filepath.Base(path)] = hex.EncodeToString(h.Sum(nil))
	}
	var sourceBytes, gitBytes int64
	require.NoError(t, filepath.WalkDir(checkout, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if strings.HasPrefix(path, filepath.Join(checkout, ".git")+string(filepath.Separator)) {
			gitBytes += info.Size()
		} else {
			sourceBytes += info.Size()
		}
		return nil
	}))
	du := exec.CommandContext(ctx, "du", "-sk", filepath.Join(checkout, ".git"), checkout)
	allocated, duErr := du.CombinedOutput()
	realBenchLog(t, map[string]any{"kind": "fixture", "setup_ms": float64(time.Since(started)) / float64(time.Millisecond), "git_version": git("--version"), "commits": git("rev-list", "--count", "HEAD"), "tracked_files": len(strings.Split(git("ls-files"), "\n")), "objects": git("count-objects", "-v"), "pack_sha256": packHashes, "source_file_bytes": sourceBytes, "git_file_bytes": gitBytes, "host_du_allocated_KiB": string(allocated), "du_error": fmt.Sprint(duErr), "engine_physical_allocation": "not measured; compressed object bytes are not allocation"})
	readHost := func(path string) string {
		b, err := os.ReadFile(filepath.Join(checkout, path))
		require.NoError(t, err)
		return string(b)
	}
	top, nested, pending := readHost("README.md"), readHost("core/workspace.go"), readHost("go.mod")

	sink, server := realBenchTraceServer(t)
	started = time.Now()
	c, err := dagger.Connect(ctx, dagger.WithWorkdir(checkout), dagger.WithLogOutput(io.Discard), dagger.WithEnvironmentVariable("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", server.URL+"/v1/traces"), dagger.WithEnvironmentVariable("OTEL_EXPORTER_OTLP_TRACES_LIVE", "1"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	version, err := c.Version(ctx)
	require.NoError(t, err)
	require.Contains(t, string(cliVersion), version, "engine must match the explicitly built CLI")
	realBenchLog(t, map[string]any{"kind": "connect", "ms": float64(time.Since(started)) / float64(time.Millisecond), "engine_version": version})
	started = time.Now()
	id, err := c.CurrentWorkspace().Snapshot().ID(ctx)
	require.NoError(t, err)
	ws := dagger.Ref[*dagger.Workspace](c, id)
	sha, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, realBenchFixtureSHA, sha)
	realBenchStatus(ctx, t, ws.Git().Uncommitted(), realBenchPaths{})
	realBenchLog(t, map[string]any{"kind": "capture", "ms": float64(time.Since(started)) / float64(time.Millisecond)})
	// Resolve consumer image outside the cycles; full tree reads remain inside.
	started = time.Now()
	consumer, err := c.Container().From("alpine:3.22.2").Sync(ctx)
	require.NoError(t, err)
	realBenchLog(t, map[string]any{"kind": "consumer_setup", "ms": float64(time.Since(started)) / float64(time.Millisecond)})

	const added = "core/realbench-added.txt"
	const renamed = "core/realbench-renamed.txt"
	expected := []realBenchPaths{{Modified: []string{"README.md"}}, {Modified: []string{"core/workspace.go"}}, {Added: []string{added}}, {Added: []string{renamed}, Removed: []string{added}}, {Modified: []string{"README.md"}}, {Removed: []string{renamed}}}
	edit := func(w *dagger.Workspace, i int) *dagger.Workspace {
		switch i {
		case 0:
			return w.WithNewFile("README.md", top+"\nReal benchmark edit one.\n")
		case 1:
			return w.WithNewFile("core/workspace.go", nested+"\n// Real benchmark nested edit.\n")
		case 2:
			return w.WithNewFile(added, "real benchmark addition\n")
		case 3:
			return w.WithFile(renamed, w.File(added)).WithoutFile(added)
		case 4:
			return w.WithNewFile("README.md", top+"\nReal benchmark edit five.\n")
		case 5:
			return w.WithoutFile(renamed)
		default:
			return w.WithNewFile("README.md", top+"\nReal benchmark next edit.\n")
		}
	}
	ws = edit(ws.WithNewFile("go.mod", pending+"\n// Keep this unselected edit.\n"), 0)
	for i := range expected {
		cycle := time.Now()
		sample := map[string]any{"kind": "cycle", "iteration": i + 1, "paths": expected[i]}
		measure := func(name string, fn func()) {
			start := time.Now()
			fn()
			sample[name+"_ms"] = float64(time.Since(start)) / float64(time.Millisecond)
		}
		var selected *dagger.Changeset
		before := ws.Git().Head()
		var beforeSHA string
		measure("pre_status", func() {
			want := expected[i]
			want.Modified = append(append([]string{}, want.Modified...), "go.mod")
			realBenchStatus(ctx, t, ws.Git().Uncommitted(), want)
			paths := append(append(append([]string{}, expected[i].Modified...), expected[i].Added...), expected[i].Removed...)
			selectedID, err := ws.Git().Uncommitted().Filter(dagger.ChangesetFilterOpts{Include: paths}).ID(ctx)
			require.NoError(t, err)
			selected = dagger.Ref[*dagger.Changeset](c, selectedID)
			beforeSHA, err = before.CommitSHA(ctx)
			require.NoError(t, err)
		})
		measure("diff_review", func() {
			patch, err := selected.AsPatch().Contents(ctx)
			require.NoError(t, err)
			require.NotEmpty(t, patch)
			require.NotContains(t, patch, "diff --git a/go.mod")
			sample["reviewed_patch"] = patch
		})
		var next *dagger.Workspace
		message := fmt.Sprintf("realbench: scoped commit %d", i+1)
		measure("withCommit", func() {
			id, err := ws.WithCommit(selected, message, realBenchDate, dagger.WorkspaceWithCommitOpts{AuthorName: "Real Benchmark", AuthorEmail: "realbench@example.com"}).ID(ctx)
			require.NoError(t, err)
			next = dagger.Ref[*dagger.Workspace](c, id)
		})
		measure("post_status", func() {
			realBenchStatus(ctx, t, next.Git().Uncommitted(), realBenchPaths{Modified: []string{"go.mod"}})
		})
		head := next.Git().Head()
		measure("history", func() {
			ahead, err := head.Log(ctx, dagger.GitRefLogOpts{Base: before, Limit: 101})
			require.NoError(t, err)
			require.Len(t, ahead, 1)
			msg, err := ahead[0].Message(ctx)
			require.NoError(t, err)
			require.Equal(t, message, strings.TrimSpace(msg))
			parents, err := ahead[0].ParentShas(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{beforeSHA}, parents)
			behind, err := before.Log(ctx, dagger.GitRefLogOpts{Base: head, Limit: 101})
			require.NoError(t, err)
			require.Empty(t, behind)
			recent, err := head.Log(ctx, dagger.GitRefLogOpts{Limit: 2})
			require.NoError(t, err)
			require.Len(t, recent, 2)
		})
		measure("consume", func() {
			tree := head.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
			realBenchStatus(ctx, t, tree.Changes(before.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})), expected[i])
			wantTop := top + "\nReal benchmark edit one.\n"
			if i >= 4 {
				wantTop = top + "\nReal benchmark edit five.\n"
			}
			got, err := tree.File("README.md").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, wantTop, got)
			wantNested := nested
			if i >= 1 {
				wantNested += "\n// Real benchmark nested edit.\n"
			}
			got, err = tree.File("core/workspace.go").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, wantNested, got)
			if i >= 2 && i < 5 {
				path := added
				if i >= 3 {
					path = renamed
				}
				got, err = tree.File(path).Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, "real benchmark addition\n", got)
			}
			contents, err := tree.File("go.mod").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, pending, contents)
			contents, err = next.File("go.mod").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, pending+"\n// Keep this unselected edit.\n", contents)
			out, err := consumer.WithMountedDirectory("/source", tree).WithMountedDirectory("/workspace", next.Directory("/", dagger.WorkspaceDirectoryOpts{Exclude: []string{".git"}})).WithExec([]string{"sh", "-ec", "for d in /source /workspace; do cd \"$d\"; find . -type f -exec sha256sum {} + | sort | sha256sum; done"}).Stdout(ctx)
			require.NoError(t, err)
			sample["consumed_source_and_workspace_sha256"] = out
		})
		measure("next_edit", func() {
			ws = edit(next, i+1)
			id, err := ws.ID(ctx)
			require.NoError(t, err)
			ws = dagger.Ref[*dagger.Workspace](c, id)
			// Force pending-file evaluation, not just construction of a recipe.
			paths, err := ws.Git().Uncommitted().DiffStats(ctx)
			require.NoError(t, err)
			require.NotEmpty(t, paths)
		})
		sample["cycle_ms"] = float64(time.Since(cycle)) / float64(time.Millisecond)
		realBenchLog(t, sample)
	}
	require.Equal(t, realBenchFixtureSHA, git("rev-parse", "HEAD"))
	require.Empty(t, git("status", "--porcelain", "--untracked-files=all"), "source checkout must remain untouched")
	require.Equal(t, top, readHost("README.md"))
	require.Equal(t, nested, readHost("core/workspace.go"))
	require.Equal(t, pending, readHost("go.mod"))
	require.NoError(t, c.Close())
	sink.report(t)
	realBenchLog(t, map[string]any{"kind": "validated", "commits": 6, "host_unchanged": true})
}
