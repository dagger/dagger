package schema

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// gitFollowsOrigin serves a repository of many files over Git's smart HTTP,
// able to serve partial fetches, and returns its URL, its commit and the
// commit's files as `git ls-tree` lists them ("mode oid path").
func gitFollowsOrigin(t *testing.T) (url, sha string, files []string) {
	t.Helper()
	root := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+root,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	work := filepath.Join(root, "work")
	require.NoError(t, os.Mkdir(work, 0o755))
	run(work, "init", "-q", "-b", "main")
	// Enough paths that two walks of a map of them come out in one order
	// with no real chance.
	for i := range 64 {
		p := filepath.Join(work, fmt.Sprintf("d%d", i%8), fmt.Sprintf("f%02d.txt", i))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(fmt.Sprintf("file %d\n", i)), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(work, "dagger.toml"), []byte("\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "files")
	sha = run(work, "rev-parse", "HEAD")
	for line := range strings.SplitSeq(run(work, "ls-tree", "-r", "--full-tree", sha), "\n") {
		meta, p, _ := strings.Cut(line, "\t")
		fields := strings.Fields(meta)
		files = append(files, fields[0]+" "+fields[2]+" "+p)
	}
	bare := filepath.Join(root, "origin.git")
	run(root, "clone", "-q", "--bare", work, bare)
	run(bare, "config", "uploadpack.allowFilter", "true")
	run(bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	server := httptest.NewServer(gitUploadPackHandler(t, bare, root))
	t.Cleanup(server.Close)
	return server.URL + "/origin.git", sha, files
}

// gitUploadPackHandler serves the bare repository at /origin.git over Git's
// smart HTTP, fetches only, with git upload-pack itself.
func gitUploadPackHandler(t *testing.T, bare, home string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := io.Reader(r.Body)
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			body = gz
		}
		args := []string{"upload-pack", "--stateless-rpc"}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/origin.git/info/refs" && r.URL.Query().Get("service") == "git-upload-pack":
			args = append(args, "--advertise-refs")
			w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		case r.Method == http.MethodPost && r.URL.Path == "/origin.git/git-upload-pack":
			w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
		default:
			http.NotFound(w, r)
			return
		}
		cmd := exec.Command("git", append(args, bare)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+home, "GIT_PROTOCOL="+r.Header.Get("Git-Protocol"))
		cmd.Stdin = body
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		if err := cmd.Run(); err != nil {
			t.Logf("git upload-pack: %v: %s", err, stderr.String())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if r.Method == http.MethodGet && r.Header.Get("Git-Protocol") == "" {
			// Protocol v0 says which service answers before its refs.
			w.Write([]byte("001e# service=git-upload-pack\n0000"))
		}
		w.Write(out.Bytes())
	})
}

// snapshotFiles is every file of the directory's snapshot from its root, with
// its mode and its bytes or link target. What an engine stores and sends of
// a directory is its whole snapshot, while its content digest covers only
// its own path: a sparse tree's lists sit beside its files, outside it.
func (e *followsTestEngine) snapshotFiles(dir dagql.ObjectResult[*core.Directory]) map[string]string {
	e.t.Helper()
	e.digest(dir) // evaluates it
	ref, set := dir.Self().Snapshot.Peek()
	require.True(e.t, set)
	out := map[string]string{}
	require.NoError(e.t, core.MountRef(e.ctx, ref, func(root string, _ *mount.Mount) error {
		return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			switch {
			case d.Type()&fs.ModeSymlink != 0:
				target, err := os.Readlink(p)
				if err != nil {
					return err
				}
				out[rel] = info.Mode().String() + " -> " + target
			case d.IsDir():
				out[rel] = info.Mode().String()
			default:
				data, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				out[rel] = fmt.Sprintf("%s %x", info.Mode(), sha256.Sum256(data))
			}
			return nil
		})
	}))
	return out
}

// gitFollowsResults runs the git reads core declares to follow from their
// inputs on e, against the repository at url.
func gitFollowsResults(t *testing.T, e *followsTestEngine, url, sha string, files []string) map[string]string {
	t.Helper()
	var repo dagql.ObjectResult[*core.GitRepository]
	require.NoError(t, e.srv.Select(e.ctx, e.srv.Root(), &repo, dagql.Selector{Field: "git", Args: []dagql.NamedInput{followsArg("url", dagql.NewString(url))}}))
	var ref dagql.ObjectResult[*core.GitRef]
	require.NoError(t, e.srv.Select(e.ctx, repo, &ref, dagql.Selector{Field: "ref", Args: []dagql.NamedInput{followsArg("name", dagql.NewString(sha))}}))
	out := map[string]string{}
	for p, file := range e.snapshotFiles(e.dir(ref, "__sparseWorkspaceTree", followsArg("cwd", dagql.NewString(".")))) {
		out["GitRef.__sparseWorkspaceTree "+p] = file
	}
	sparse := e.dir(repo, "__sparseFiles", followsArg("files", dagql.ArrayInput[dagql.String](dagql.NewStringArray(files...))))
	for p, file := range e.snapshotFiles(sparse) {
		out["GitRepository.__sparseFiles "+p] = file
	}
	out["GitRef.tree"] = e.digest(e.dir(ref, "tree", followsArg("discardGitDir", dagql.Boolean(true))))
	return out
}

// The git reads that follow from their inputs give the same snapshot, every
// byte of it, on two engines a second apart: what each stores beside the
// files it serves too, which the directory's content digest doesn't cover.
func TestDeclaredGitReadsFollowFromTheirInputs(t *testing.T) {
	url, sha, files := gitFollowsOrigin(t)
	salt := transferTestSalt(t)
	first := gitFollowsResults(t, newFollowsTestEngine(t, salt, "follows-git-a"), url, sha, files)
	time.Sleep(1100 * time.Millisecond)
	second := gitFollowsResults(t, newFollowsTestEngine(t, salt, "follows-git-b"), url, sha, files)
	require.Contains(t, first, "GitRef.__sparseWorkspaceTree objects", "the sparse tree lists its objects")
	for name, value := range first {
		require.Equal(t, value, second[name], name)
	}
	require.Len(t, second, len(first))
}
