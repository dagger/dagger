package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestNativeWorkspaceMergeMatchesCheckout(t *testing.T) {
	// The engine runs with umask 000. Exercise both it and the usual host
	// umask: Git's normalization is not a hard-coded 0644/0755 policy.
	for _, mask := range []int{0022, 0} {
		t.Run(fmt.Sprintf("umask%03o", mask), func(t *testing.T) {
			old := syscall.Umask(mask)
			defer syscall.Umask(old)
			for _, scenario := range []string{"disjoint", "identical", "identical-metadata", "overlap", "rename-modify", "replacement", "attributes", "normalized-noop", "delete", "conflict", "modify-delete", "packed"} {
				t.Run(scenario, func(t *testing.T) {
					ctx := t.Context()
					source := t.TempDir()
					run := func(dir string, args ...string) string {
						t.Helper()
						out, err := runWorkspaceCommitGit(ctx, dir, nil, args...)
						require.NoError(t, err)
						return strings.TrimSpace(out)
					}
					write := func(dir, p, data string, mode os.FileMode) {
						t.Helper()
						name := filepath.Join(dir, p)
						require.NoError(t, os.MkdirAll(filepath.Dir(name), 0777))
						require.NoError(t, os.WriteFile(name, []byte(data), mode))
						require.NoError(t, os.Chmod(name, mode))
					}
					run(source, "init", "-b", "main")
					write(source, "unchanged", "keep filesystem metadata", 0600)
					write(source, "edit", "one\ntwo\nthree\nfour\nfive\nsix\nseven\n", 0644)
					write(source, "remove", "remove\n", 0644)
					write(source, "dir/file", "child\n", 0644)
					write(source, ".gitattributes", "*.txt text eol=crlf ident\n", 0644)
					write(source, "same.txt", "same\n", 0644)
					run(source, "add", ".")
					run(source, "commit", "-m", "base")
					parent := run(source, "rev-parse", "HEAD")
					if scenario == "packed" {
						run(source, "gc", "--prune=now")
					}
					// Both start with precisely the same filesystem, including a
					// non-Git mode on an untouched file. No shared mutable .git.
					oracle, native := filepath.Join(t.TempDir(), "oracle"), filepath.Join(t.TempDir(), "native")
					for _, dir := range []string{oracle, native} {
						run(source, "clone", "--no-hardlinks", source, dir)
						require.NoError(t, os.RemoveAll(filepath.Join(dir, ".git")))
						require.NoError(t, os.Chmod(filepath.Join(dir, "unchanged"), 0600))
					}
					paths := []*ChangesetPaths{{}, {}}
					apply := []func(string) error{
						func(work string) error {
							switch scenario {
							case "disjoint", "packed":
								paths[0].Added = []string{"ours", "newdir/ours", "link"}
								write(work, "ours", "ours", 0600)
								write(work, "newdir/ours", "new directory", 0600)
								require.NoError(t, os.Symlink("ours", filepath.Join(work, "link")))
							case "identical", "identical-metadata":
								paths[0].Added = []string{"new"}
								write(work, "new", "same", 0600)
							case "overlap", "conflict", "modify-delete":
								paths[0].Modified = []string{"edit"}
								write(work, "edit", "ONE\ntwo\nthree\nfour\nfive\nsix\nseven\n", 0600)
							case "rename-modify":
								paths[0].AllRemoved = []string{"edit"}
								paths[0].Added = []string{"renamed"}
								require.NoError(t, os.RemoveAll(filepath.Join(work, "edit")))
								write(work, "renamed", "one\ntwo\nthree\nfour\nfive\nsix\nseven\n", 0600)
							case "replacement":
								paths[0].AllRemoved = []string{"dir/file", "remove"}
								paths[0].Added = []string{"dir", "remove/child"}
								require.NoError(t, os.RemoveAll(filepath.Join(work, "dir")))
								require.NoError(t, os.RemoveAll(filepath.Join(work, "remove")))
								write(work, "dir", "file now", 0600)
								write(work, "remove/child", "directory now", 0700)
							case "attributes":
								paths[0].Added = []string{"same-new.txt"}
								write(work, "same-new.txt", "$Id: expanded $\nhello\n", 0600)
							case "normalized-noop":
								paths[0].Modified = []string{"same.txt"}
								write(work, "same.txt", "same\n", 0600)
							case "delete":
								paths[0].AllRemoved = []string{"dir/file"}
								require.NoError(t, os.RemoveAll(filepath.Join(work, "dir")))
							}
							return nil
						},
						func(work string) error {
							switch scenario {
							case "identical", "identical-metadata":
								paths[1].Added = []string{"new"}
								write(work, "new", "same", 0640)
								if scenario == "identical-metadata" {
									if os.Geteuid() == 0 {
										require.NoError(t, os.Chown(filepath.Join(work, "new"), 123, 456))
									}
									require.NoError(t, os.Chmod(filepath.Join(work, "new"), 0600|os.ModeSetuid|os.ModeSetgid))
									require.NoError(t, unix.Setxattr(filepath.Join(work, "new"), "user.native-test", []byte("retained"), 0))
								}
							case "overlap", "rename-modify":
								paths[1].Modified = []string{"edit"}
								write(work, "edit", "one\ntwo\nthree\nfour\nfive\nsix\nSEVEN\n", 0700)
							case "conflict":
								paths[1].Modified = []string{"edit"}
								write(work, "edit", "conflict\n", 0644)
							case "modify-delete":
								paths[1].AllRemoved = []string{"edit"}
								require.NoError(t, os.RemoveAll(filepath.Join(work, "edit")))
							case "attributes":
								paths[1].Added = []string{"same-new.txt", "incoming.txt"}
								write(work, "same-new.txt", "$Id: different expansion $\r\nhello\r\n", 0640)
								write(work, "incoming.txt", "$Id: expanded $\nnew\n", 0600)
							default:
								paths[1].Added = []string{"theirs"}
								write(work, "theirs", "theirs", 0700)
							}
							return nil
						},
					}
					// This is the actual legacy sequence, including whole-baseline
					// staging and checkout skips; not a tree-only equality oracle.
					require.NoError(t, initGitRepo(ctx, oracle))
					run(oracle, "checkout", "-b", "ours")
					require.NoError(t, apply[0](oracle))
					run(oracle, "add", "-A")
					run(oracle, "commit", "--allow-empty", "-m", "ours")
					run(oracle, "checkout", "-b", "theirs", "HEAD~1")
					require.NoError(t, apply[1](oracle))
					run(oracle, "add", "-A")
					run(oracle, "commit", "--allow-empty", "-m", "theirs")
					run(oracle, "checkout", "ours")
					oracleErr := runGit(ctx, oracle, "merge", "--no-edit", "--no-commit", "theirs")
					err := nativeWorkspaceMerge(ctx, filepath.Join(source, ".git/objects"), parent, native, paths, apply)
					if scenario == "conflict" || scenario == "modify-delete" {
						require.Error(t, oracleErr)
						require.Error(t, err)
						require.NotErrorIs(t, err, errNativeCommitUnsupported)
						require.Contains(t, err.Error(), "CONFLICT")
						return
					}
					require.NoError(t, oracleErr)
					require.NoError(t, err)
					require.NoError(t, os.RemoveAll(filepath.Join(oracle, ".git")))
					require.Equal(t, nativeWorkspaceFilesystem(t, oracle), nativeWorkspaceFilesystem(t, native))
					require.Equal(t, parent, run(source, "rev-parse", "HEAD"))
				})
			}
		})
	}
}

func nativeWorkspaceFilesystem(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == root {
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stat := info.Sys().(*syscall.Stat_t)
		var data string
		if entry.Type()&os.ModeSymlink != 0 {
			data, err = os.Readlink(name)
		} else if !entry.IsDir() {
			var contents []byte
			contents, err = os.ReadFile(name)
			data = string(contents)
		}
		if err != nil {
			return err
		}
		result[rel] = fmt.Sprintf("%v uid=%d gid=%d %s", info.Mode(), stat.Uid, stat.Gid, data)
		value := make([]byte, 256)
		n, err := unix.Lgetxattr(name, "user.native-test", value)
		if err != nil && !errors.Is(err, unix.ENODATA) && !errors.Is(err, unix.ENOTSUP) {
			return err
		}
		if n > 0 {
			result[rel] += " xattr=" + string(value[:n])
		}
		return nil
	}))
	return result
}

func TestNativeWorkspaceMergeFallbacksAndErrors(t *testing.T) {
	ctx := t.Context()
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"commit", "--allow-empty", "-m", "base"}} {
		_, err := runWorkspaceCommitGit(ctx, repo, nil, args...)
		require.NoError(t, err)
	}
	parent, err := runWorkspaceCommitGit(ctx, repo, nil, "rev-parse", "HEAD")
	require.NoError(t, err)
	parent = strings.TrimSpace(parent)
	noop := func(string) error { return nil }
	for _, paths := range []*ChangesetPaths{{Added: []string{"empty/"}}, {Modified: []string{".gitattributes"}}, {AllRemoved: []string{"nested/.gitignore"}}, {Added: []string{".gitmodules"}}} {
		err := nativeWorkspaceMerge(ctx, filepath.Join(repo, ".git/objects"), parent, t.TempDir(), []*ChangesetPaths{paths, {}}, []func(string) error{noop, noop})
		require.ErrorIs(t, err, errNativeCommitUnsupported)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	err = nativeWorkspaceMerge(cancelled, filepath.Join(repo, ".git/objects"), parent, t.TempDir(), []*ChangesetPaths{{}, {}}, []func(string) error{noop, noop})
	require.ErrorIs(t, err, context.Canceled)
	err = nativeWorkspaceMerge(ctx, filepath.Join(repo, ".git/objects"), strings.Repeat("f", 40), t.TempDir(), []*ChangesetPaths{{}, {}}, []func(string) error{noop, noop})
	require.Error(t, err)
	require.False(t, errors.Is(err, errNativeCommitUnsupported))
}

func TestNativeWorkspaceMergeBaseEvidence(t *testing.T) {
	for _, scenario := range []string{"ignored-addition", "ignored-tracked", "noncanonical-blob", "literal-path"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			repo := t.TempDir()
			run := func(args ...string) string {
				t.Helper()
				out, err := runWorkspaceCommitGit(ctx, repo, nil, args...)
				require.NoError(t, err)
				return strings.TrimSpace(out)
			}
			run("init", "-b", "main")
			require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored\n"), 0644))
			require.NoError(t, os.WriteFile(filepath.Join(repo, "old.txt"), []byte("old\r\n"), 0644))
			run("add", ".")
			run("commit", "-m", "base")
			paths := &ChangesetPaths{}
			name := "ignored"
			switch scenario {
			case "ignored-addition":
				paths.Added = []string{name}
			case "ignored-tracked":
				require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte("tracked despite ignores"), 0644))
				run("add", "-f", name)
				run("commit", "-m", "tracked ignored file")
				paths.Modified = []string{name}
			case "noncanonical-blob":
				// Retain the old CRLF blob, then introduce clean conversion.
				require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("*.txt text eol=lf\n"), 0644))
				run("add", ".gitattributes")
				run("commit", "-m", "attributes without renormalizing")
				name = "old.txt"
				paths.Modified = []string{name}
			case "literal-path":
				name = ":[literal]\nname"
				paths.Added = []string{name}
			}
			parent := run("rev-parse", "HEAD")
			base := filepath.Join(t.TempDir(), "base")
			run("clone", "--no-hardlinks", repo, base)
			require.NoError(t, os.RemoveAll(filepath.Join(base, ".git")))
			apply := func(work string) error {
				return os.WriteFile(filepath.Join(work, name), []byte("new\n"), 0644)
			}
			err := nativeWorkspaceMerge(ctx, filepath.Join(repo, ".git/objects"), parent, base, []*ChangesetPaths{paths, {}}, []func(string) error{apply, func(string) error { return nil }})
			if scenario == "literal-path" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, errNativeCommitUnsupported)
			}
		})
	}
}

func TestNativeWorkspaceDeltaMetadataFallback(t *testing.T) {
	base, delta := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(delta, "unreported"), []byte("same bytes"), 0600))
	err := validateNativeWorkspaceDelta(t.Context(), base, delta, &ChangesetPaths{})
	require.ErrorIs(t, err, errNativeCommitUnsupported)
	require.NoError(t, os.Remove(filepath.Join(delta, "unreported")))
	require.NoError(t, os.Chmod(delta, 0700))
	require.NoError(t, os.Chmod(base, 0755))
	err = validateNativeWorkspaceDelta(t.Context(), base, delta, &ChangesetPaths{})
	require.ErrorIs(t, err, errNativeCommitUnsupported)
}
