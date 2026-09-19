package core

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRunGitEnvTelemetryPrivacy(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(context.Background())
	ctx, root := provider.Tracer("test").Start(context.Background(), "root")
	defer root.End()
	dir := t.TempDir()
	_, err := runGitEnv(ctx, dir, "init", "--quiet")
	require.NoError(t, err)
	_, err = runGitEnv(ctx, dir, "-c", "user.name=private-identity", "rev-parse", "--verify", "private-ref")
	require.Error(t, err)
	require.Contains(t, err.Error(), "private-ref", "caller retains the diagnostic")
	spans := recorder.Ended()
	require.Len(t, spans, 2)
	require.Equal(t, "git init", spans[0].Name())
	require.Equal(t, "git rev-parse", spans[1].Name())
	require.Equal(t, codes.Error, spans[1].Status().Code)
	for _, span := range spans {
		diagnostic := fmt.Sprint(span.Name(), span.Attributes(), span.Events(), span.Status())
		require.NotContains(t, diagnostic, "private-")
		require.NotContains(t, diagnostic, dir)
	}
}

func gitBundleTestRun(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := runGitEnv(context.Background(), dir, args...)
	require.NoError(t, err)
	return strings.TrimSpace(out)
}

func gitBundleTestSnapshot(t *testing.T, root string) map[string][sha256.Size]byte {
	t.Helper()
	files := map[string][sha256.Size]byte{}
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[strings.TrimPrefix(path, root)] = sha256.Sum256(data)
		return nil
	}))
	return files
}

func TestCreateLocalGitBundle(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		for _, incremental := range []bool{false, true} {
			for _, packed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/incremental=%t/packed=%t", format, incremental, packed), func(t *testing.T) {
					ctx := context.Background()
					source := t.TempDir()
					gitBundleTestRun(t, source, "init", "--quiet", "--initial-branch=master", "--object-format="+format)
					require.NoError(t, os.WriteFile(filepath.Join(source, "file"), []byte("base\n"), 0o600))
					gitBundleTestRun(t, source, "add", ".")
					gitBundleTestRun(t, source, "commit", "--quiet", "-m", "base")
					base := gitBundleTestRun(t, source, "rev-parse", "HEAD")
					dest := t.TempDir()
					gitBundleTestRun(t, dest, "init", "--bare", "--quiet", "--object-format="+format)
					if incremental {
						// Populate the destination before the new commit exists, so it
						// contains exactly the prerequisite closure, not extra objects.
						gitBundleTestRun(t, dest, "fetch", "--quiet", "--no-tags", "file://"+source, base+":refs/heads/base")
					}
					require.NoError(t, os.WriteFile(filepath.Join(source, "file"), []byte("target\n"), 0o600))
					gitBundleTestRun(t, source, "commit", "--quiet", "-am", "target")
					head := gitBundleTestRun(t, source, "rev-parse", "HEAD")
					gitBundleTestRun(t, source, "tag", "-a", "v1", "-m", "annotation")
					tag := gitBundleTestRun(t, source, "rev-parse", "refs/tags/v1")
					gitBundleTestRun(t, source, "checkout", "--quiet", "--detach")
					gitBundleTestRun(t, source, "update-ref", "refs/dagger/bundle/base", head)
					if packed {
						gitBundleTestRun(t, source, "gc", "--quiet")
					}
					before := gitBundleTestSnapshot(t, source)
					cli := gitutil.NewGitCLI(gitutil.WithDir(source), gitutil.WithExec(func(_ context.Context, cmd *exec.Cmd) error {
						require.NotContains(t, cmd.Args, "fetch", "local bundling must not fetch history")
						return cmd.Run()
					}))
					targets := []*gitBundleTarget{}
					for _, name := range []string{"HEAD", "refs/heads/master", "refs/tags/v1", "refs/dagger/bundle/base"} {
						sha := head
						if name == "refs/tags/v1" {
							sha = tag
						}
						targets = append(targets, &gitBundleTarget{
							exact: &gitutil.Ref{Name: name, SHA: sha}, checkout: &gitutil.Ref{SHA: head},
						})
					}
					root := t.TempDir()
					baseSHA := ""
					if incremental {
						baseSHA = base
					}
					require.NoError(t, createGitBundleFromSource(ctx, cli, &LocalGitRepository{}, root, targets, baseSHA))
					require.Equal(t, before, gitBundleTestSnapshot(t, source), "source must not change")
					require.NoError(t, os.RemoveAll(source)) // Simulate release of the source mount.
					entries, err := os.ReadDir(root)
					require.NoError(t, err)
					require.Len(t, entries, 1, "no alternate path or private repository may survive")
					bundlePath := filepath.Join(root, "repository.bundle")
					header, err := inspectGitBundleFile(bundlePath)
					require.NoError(t, err)
					require.Equal(t, format, header.ObjectFormat)
					if incremental {
						require.Equal(t, []string{base}, header.PrerequisiteSHAs)
						empty := t.TempDir()
						gitBundleTestRun(t, empty, "init", "--bare", "--quiet", "--object-format="+format)
						_, err := runGitEnv(ctx, empty, "bundle", "verify", bundlePath)
						require.ErrorContains(t, err, "Repository lacks these prerequisite commits")
					} else {
						require.Empty(t, header.PrerequisiteSHAs)
					}
					gitBundleTestRun(t, dest, "bundle", "verify", bundlePath)
					require.NoError(t, fetchGitBundleRefs(ctx, dest, bundlePath, header.Refs))
					gitBundleTestRun(t, dest, "fsck", "--full", "--strict")
					require.Equal(t, "target", gitBundleTestRun(t, dest, "show", head+":file"))
					require.Equal(t, tag, gitBundleTestRun(t, dest, "rev-parse", "refs/tags/v1"))
					require.Contains(t, gitBundleTestRun(t, dest, "cat-file", "-p", tag), "annotation")
					require.Len(t, header.Refs, len(targets))
				})
			}
		}
	}
}

func TestCreateRemoteGitBundlePreservesAnnotatedTag(t *testing.T) {
	ctx := context.Background()
	origin := t.TempDir()
	gitBundleTestRun(t, origin, "init", "--bare", "--quiet")
	tree := gitBundleTestRun(t, origin, "mktree")
	head := gitBundleTestRun(t, origin, "commit-tree", tree, "-m", "head")
	gitBundleTestRun(t, origin, "update-ref", "refs/heads/main", head)
	gitBundleTestRun(t, origin, "tag", "-a", "v1", "-m", "annotation", head)
	tag := gitBundleTestRun(t, origin, "rev-parse", "refs/tags/v1")
	mirror := t.TempDir()
	gitBundleTestRun(t, mirror, "init", "--bare", "--quiet")
	gitBundleTestRun(t, mirror, "fetch", "--quiet", "--no-tags", "file://"+origin, head+":refs/heads/main")
	_, err := runGitEnv(ctx, mirror, "cat-file", "-e", tag)
	require.Error(t, err, "canonical mirror must not already have the annotation")
	url := &gitutil.GitURL{Scheme: "file", Path: origin}
	root := t.TempDir()
	targets := []*gitBundleTarget{{
		exact: &gitutil.Ref{Name: "refs/tags/v1", SHA: tag}, checkout: &gitutil.Ref{SHA: head},
	}}
	require.NoError(t, createGitBundleFromSource(ctx, gitutil.NewGitCLI(gitutil.WithDir(mirror)), &RemoteGitRepository{URL: url}, root, targets, ""))
	require.NoError(t, os.RemoveAll(origin))
	require.NoError(t, os.RemoveAll(mirror))
	dest := t.TempDir()
	gitBundleTestRun(t, dest, "init", "--bare", "--quiet")
	bundlePath := filepath.Join(root, "repository.bundle")
	gitBundleTestRun(t, dest, "bundle", "verify", bundlePath)
	gitBundleTestRun(t, dest, "fetch", "--quiet", bundlePath, "refs/tags/v1:refs/tags/v1")
	require.Equal(t, tag, gitBundleTestRun(t, dest, "rev-parse", "refs/tags/v1"))
	require.Contains(t, gitBundleTestRun(t, dest, "cat-file", "-p", tag), "annotation")
	gitBundleTestRun(t, dest, "fsck", "--full", "--strict")
}

func TestCreateLocalGitBundleQuotesAlternatePath(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source\nwith\r\"quotes\\and spaces")
	require.NoError(t, os.Mkdir(source, 0o700))
	gitBundleTestRun(t, source, "init", "--bare", "--quiet")
	tree := gitBundleTestRun(t, source, "mktree")
	head := gitBundleTestRun(t, source, "commit-tree", tree, "-m", "head")
	targets := []*gitBundleTarget{{exact: &gitutil.Ref{Name: "refs/heads/main", SHA: head}}}
	root := t.TempDir()
	require.NoError(t, createGitBundleFromSource(context.Background(), gitutil.NewGitCLI(gitutil.WithDir(source)), &LocalGitRepository{}, root, targets, ""))
}

func TestCreateLocalGitBundleRejectsMissingObjects(t *testing.T) {
	source := t.TempDir()
	gitBundleTestRun(t, source, "init", "--quiet")
	require.NoError(t, os.WriteFile(filepath.Join(source, "file"), []byte("content\n"), 0o600))
	gitBundleTestRun(t, source, "add", ".")
	tree := gitBundleTestRun(t, source, "write-tree")
	head := gitBundleTestRun(t, source, "commit-tree", tree, "-m", "head")
	// The selected commit exists, but its tree does not. Borrowing its objects
	// must not let us emit a bundle with an incomplete closure.
	require.NoError(t, os.Remove(filepath.Join(source, ".git", "objects", tree[:2], tree[2:])))
	targets := []*gitBundleTarget{{exact: &gitutil.Ref{Name: "refs/heads/main", SHA: head}}}
	root := t.TempDir()
	err := createGitBundleFromSource(context.Background(), gitutil.NewGitCLI(gitutil.WithDir(source)), &LocalGitRepository{}, root, targets, "")
	require.Error(t, err)
	require.NoDirExists(t, filepath.Join(root, ".git-bundle-source"))
}

func TestCreateLocalGitBundleRejectsUnreachableBase(t *testing.T) {
	source := t.TempDir()
	gitBundleTestRun(t, source, "init", "--bare", "--quiet")
	tree := gitBundleTestRun(t, source, "mktree")
	head := gitBundleTestRun(t, source, "commit-tree", tree, "-m", "head")
	base := gitBundleTestRun(t, source, "commit-tree", tree, "-m", "unrelated")
	targets := []*gitBundleTarget{{exact: &gitutil.Ref{Name: "refs/heads/main", SHA: head}}}
	root := t.TempDir()
	err := createGitBundleFromSource(context.Background(), gitutil.NewGitCLI(gitutil.WithDir(source)), &LocalGitRepository{}, root, targets, base)
	require.ErrorContains(t, err, "is not reachable from the bundled refs")
	require.NoDirExists(t, filepath.Join(root, ".git-bundle-source"))
}

func TestResolveGitBundleTargetPreservesAnnotatedTag(t *testing.T) {
	tagSHA := "1111111111111111111111111111111111111111"
	commitSHA := "2222222222222222222222222222222222222222"
	remote := &gitutil.Remote{Refs: []*gitutil.Ref{
		{Name: "refs/heads/v1.0.0", SHA: commitSHA},
		{Name: "refs/tags/v1.0.0", SHA: tagSHA},
		{Name: "refs/tags/v1.0.0^{}", SHA: commitSHA},
	}}

	branch, err := resolveGitBundleTarget(remote, "v1.0.0")
	require.NoError(t, err)
	require.Equal(t, "refs/heads/v1.0.0", branch.exact.Name)
	require.Equal(t, commitSHA, branch.exact.SHA)
	require.Same(t, branch.checkout, branch.exact)

	tag, err := resolveGitBundleTarget(remote, "refs/tags/v1.0.0")
	require.NoError(t, err)
	require.Equal(t, "refs/tags/v1.0.0", tag.exact.Name)
	require.Equal(t, tagSHA, tag.exact.SHA)
	require.Equal(t, commitSHA, tag.checkout.SHA)
}

func TestResolveGitBundleTargetPreservesDetachedHead(t *testing.T) {
	sha := strings.Repeat("1", 40)
	remote := &gitutil.Remote{Refs: []*gitutil.Ref{{Name: "HEAD", SHA: sha}}}

	target, err := resolveGitBundleTarget(remote, "HEAD")
	require.NoError(t, err)
	require.Equal(t, "HEAD", target.exact.Name)
	require.Equal(t, sha, target.exact.SHA)
	require.Empty(t, target.checkout.Name)
	require.Equal(t, sha, target.checkout.SHA)

	// An arbitrary commit SHA must remain unnamed and ineligible for bundling.
	commit, err := resolveGitBundleTarget(remote, sha)
	require.NoError(t, err)
	require.Empty(t, commit.exact.Name)
}

func TestParseGitBundleHeader(t *testing.T) {
	t.Run("version 2", func(t *testing.T) {
		sha := strings.Repeat("1", sha1.Size*2)
		bundle, offset, err := parseGitBundleHeader(strings.NewReader(
			"# v2 git bundle\n" + sha + " refs/heads/main\n\nPACK"))
		require.NoError(t, err)
		require.Equal(t, 2, bundle.Version)
		require.Equal(t, "sha1", bundle.ObjectFormat)
		require.Equal(t, []*GitBundleRef{{Name: "refs/heads/main", SHA: sha}}, bundle.Refs)
		require.Empty(t, bundle.PrerequisiteSHAs)
		require.Equal(t, int64(len("# v2 git bundle\n"+sha+" refs/heads/main\n\n")), offset)
	})

	t.Run("version 3 sha256 with prerequisite", func(t *testing.T) {
		base := strings.Repeat("a", sha256.Size*2)
		head := strings.Repeat("b", sha256.Size*2)
		bundle, _, err := parseGitBundleHeader(strings.NewReader(
			"# v3 git bundle\n@object-format=sha256\n-" + base + " base commit\n" + head + " refs/heads/main\n\nPACK"))
		require.NoError(t, err)
		require.Equal(t, 3, bundle.Version)
		require.Equal(t, "sha256", bundle.ObjectFormat)
		require.Equal(t, []string{base}, bundle.PrerequisiteSHAs)
		require.Equal(t, []*GitBundleRef{{Name: "refs/heads/main", SHA: head}}, bundle.Refs)
	})

	t.Run("unsupported capability", func(t *testing.T) {
		sha := strings.Repeat("1", sha1.Size*2)
		_, _, err := parseGitBundleHeader(strings.NewReader(
			"# v3 git bundle\n@filter=blob:none\n" + sha + " refs/heads/main\n\nPACK"))
		require.ErrorContains(t, err, `unsupported git bundle capability "filter"`)
	})

	sha := strings.Repeat("1", sha1.Size*2)
	for name, input := range map[string]string{
		"bad signature":        "not a bundle\n",
		"truncated header":     "# v3 git bundle\n" + sha + " refs/heads/main\n",
		"bad object format":    "# v3 git bundle\n@object-format=md5\n" + sha + " refs/heads/main\n\n",
		"bad object id":        "# v3 git bundle\n123 refs/heads/main\n\n",
		"no refs":              "# v3 git bundle\n-" + sha + " base\n\n",
		"duplicate ref":        "# v3 git bundle\n" + sha + " refs/heads/main\n" + sha + " refs/heads/main\n\n",
		"late capability":      "# v3 git bundle\n" + sha + " refs/heads/main\n@object-format=sha1\n\n",
		"v2 with capability":   "# v2 git bundle\n@object-format=sha1\n" + sha + " refs/heads/main\n\n",
		"missing ref name":     "# v3 git bundle\n" + sha + "\n\n",
		"duplicate capability": "# v3 git bundle\n@object-format=sha1\n@object-format=sha1\n" + sha + " refs/heads/main\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := parseGitBundleHeader(strings.NewReader(input))
			require.Error(t, err)
		})
	}
}

func TestParseGitBundleHeaderRefLimit(t *testing.T) {
	sha := strings.Repeat("1", sha1.Size*2)
	var header strings.Builder
	header.WriteString("# v3 git bundle\n")
	for i := 0; i <= MaxGitBundleRefs; i++ {
		fmt.Fprintf(&header, "%s refs/heads/ref-%d\n", sha, i)
	}
	header.WriteString("\n")
	_, _, err := parseGitBundleHeader(strings.NewReader(header.String()))
	require.ErrorContains(t, err, "more than")
	require.ErrorContains(t, err, fmt.Sprint(MaxGitBundleRefs))
}

func TestInspectGitBundleFileSafeguards(t *testing.T) {
	sha := strings.Repeat("1", sha1.Size*2)
	header := []byte("# v3 git bundle\n" + sha + " refs/heads/main\n\n")
	valid := testGitBundleBytes(header, 0, nil, "sha1")

	t.Run("valid envelope", func(t *testing.T) {
		path := writeTestGitBundle(t, valid)
		parsed, err := inspectGitBundleFile(path)
		require.NoError(t, err)
		require.Equal(t, 3, parsed.Version)
	})

	t.Run("truncated", func(t *testing.T) {
		path := writeTestGitBundle(t, valid[:len(valid)-1])
		_, err := inspectGitBundleFile(path)
		require.ErrorContains(t, err, "truncated")
	})

	t.Run("corrupt", func(t *testing.T) {
		corrupt := append([]byte(nil), valid...)
		corrupt[len(header)+12] ^= 0xff
		path := writeTestGitBundle(t, corrupt)
		_, err := inspectGitBundleFile(path)
		require.ErrorContains(t, err, "checksum")
	})

	t.Run("object count", func(t *testing.T) {
		tooMany := testGitBundleBytes(header, MaxGitBundleObjects+1, nil, "sha1")
		path := writeTestGitBundle(t, tooMany)
		_, err := inspectGitBundleFile(path)
		require.ErrorContains(t, err, "object count")
		require.ErrorContains(t, err, fmt.Sprint(MaxGitBundleObjects))
	})

	t.Run("file size", func(t *testing.T) {
		path := t.TempDir() + "/large.bundle"
		file, err := os.Create(path)
		require.NoError(t, err)
		require.NoError(t, file.Truncate(MaxGitBundleBytes+1))
		require.NoError(t, file.Close())
		_, err = inspectGitBundleFile(path)
		require.ErrorContains(t, err, "size")
		require.ErrorContains(t, err, fmt.Sprint(MaxGitBundleBytes))
	})
}

func testGitBundleBytes(header []byte, objectCount uint32, objectData []byte, objectFormat string) []byte {
	pack := make([]byte, 12, 12+len(objectData)+sha256.Size)
	copy(pack, "PACK")
	binary.BigEndian.PutUint32(pack[4:8], 2)
	binary.BigEndian.PutUint32(pack[8:12], objectCount)
	pack = append(pack, objectData...)
	if objectFormat == "sha256" {
		sum := sha256.Sum256(pack)
		pack = append(pack, sum[:]...)
	} else {
		sum := sha1.Sum(pack)
		pack = append(pack, sum[:]...)
	}
	return append(append([]byte(nil), header...), pack...)
}

func writeTestGitBundle(t *testing.T, contents []byte) string {
	t.Helper()
	path := t.TempDir() + "/test.bundle"
	require.NoError(t, os.WriteFile(path, contents, 0o600))
	return path
}
