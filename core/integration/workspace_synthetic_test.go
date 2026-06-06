package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// These tests define the source-backed Workspace contract. A Workspace has a
// private source backend internally, but callers only see Workspace behavior:
// filesystem reads, git state, module/config behavior, and functional updates.

// TestSyntheticWorkspaceSourceIsPrivateInSchema asserts that the backend source
// is an implementation detail. The schema should expose constructors and
// behavior, not a public backend enum or source-discriminator field.
func (WorkspaceSuite) TestSyntheticWorkspaceSourceIsPrivateInSchema(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	res, err := testutil.QueryWithClient[syntheticWorkspaceSchemaResult](c, t, `{
		workspace: __type(name: "Workspace") {
			fields {
				name
			}
		}
		directory: __type(name: "Directory") {
			fields {
				name
			}
		}
		gitRef: __type(name: "GitRef") {
			fields {
				name
			}
		}
		schema: __schema {
			types {
				name
			}
		}
	}`, nil)
	require.NoError(t, err)

	requireGraphQLField(t, res.Directory.Fields, "asWorkspace")
	requireGraphQLField(t, res.GitRef.Fields, "asWorkspace")
	requireGraphQLField(t, res.Workspace.Fields, "withNewFile")
	requireGraphQLField(t, res.Workspace.Fields, "withNewDirectory")
	requireGraphQLField(t, res.Workspace.Fields, "withChanges")
	requireGraphQLField(t, res.Workspace.Fields, "changes")

	for _, field := range []string{"backend", "backendKind", "source", "sourceKind", "workspaceSource", "hostPath", "rootfs", "clientID", "clientId"} {
		requireNoGraphQLField(t, res.Workspace.Fields, field)
	}
	requireNoGraphQLType(t, res.Schema.Types, "WorkspaceSource")
	requireNoGraphQLType(t, res.Schema.Types, "WorkspaceBackend")
}

// TestDirectoryBackedSyntheticWorkspaceUsesSourceContent asserts the core
// caller contract for Directory.asWorkspace: the supplied Directory is the
// workspace source. Filesystem APIs resolve from cwd, absolute paths resolve
// from the source root, and filters run against source content rather than a
// host workspace.
func (WorkspaceSuite) TestDirectoryBackedSyntheticWorkspaceUsesSourceContent(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := syntheticWorkspaceSource(c).AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
		Cwd: "/app/nested",
	})

	cwd, err := ws.Cwd(ctx)
	require.NoError(t, err)
	require.Equal(t, "/app/nested", cwd)

	leaf, err := ws.File("leaf.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "leaf", leaf)

	root, err := ws.File("/README.md").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "root readme", root)

	found, err := ws.FindUp(ctx, "workspace.marker")
	require.NoError(t, err)
	require.Equal(t, "/workspace.marker", found)

	filtered, err := ws.Directory("/app", dagger.WorkspaceDirectoryOpts{Gitignore: true}).Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, filtered, "main.txt")
	requireEntry(t, filtered, "nested")
	requireNoEntry(t, filtered, "debug.log")

	unfiltered, err := ws.Directory("/app").Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, unfiltered, "debug.log")
}

// TestGitRefBackedSyntheticWorkspaceUsesSelectedRef asserts the git-source
// contract: GitRef.asWorkspace keeps the selected ref as the source of truth.
// Filesystem reads come from that ref, gitignore filtering applies to that
// tree, and workspace.git reports clean git state without depending on a
// materialized .git directory.
func (WorkspaceSuite) TestGitRefBackedSyntheticWorkspaceUsesSelectedRef(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ref := syntheticWorkspaceGitRef(ctx, t, c)
	refID, err := ref.ID(ctx)
	require.NoError(t, err)

	res, err := testutil.QueryWithClient[gitRefWorkspaceResult](c, t, `query GitRefWorkspace($ref: GitRefID!) {
		ref: loadGitRefFromID(id: $ref) {
			commit
			asWorkspace(cwd: "/app") {
				cwd
				main: file(path: "main.txt") {
					contents
				}
				root: file(path: "/README.md") {
					contents
				}
				filtered: directory(path: ".", gitignore: true) {
					entries
				}
				unfiltered: directory(path: ".") {
					entries
				}
				git {
					head {
						commit
					}
					uncommitted {
						isEmpty
					}
				}
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"ref": refID,
	}})
	require.NoError(t, err)

	require.Equal(t, "/app", res.Ref.AsWorkspace.Cwd)
	require.Equal(t, "app main", res.Ref.AsWorkspace.Main.Contents)
	require.Equal(t, "root readme", res.Ref.AsWorkspace.Root.Contents)
	requireEntry(t, res.Ref.AsWorkspace.Filtered.Entries, "main.txt")
	requireNoEntry(t, res.Ref.AsWorkspace.Filtered.Entries, "debug.log")
	requireEntry(t, res.Ref.AsWorkspace.Unfiltered.Entries, "debug.log")
	require.Equal(t, strings.TrimSpace(res.Ref.Commit), strings.TrimSpace(res.Ref.AsWorkspace.Git.Head.Commit))
	require.True(t, res.Ref.AsWorkspace.Git.Uncommitted.IsEmpty)
}

// TestOverlayWorkspaceFunctionalWritesDoNotMutateBaseSource asserts the future
// functional-write contract. Writing to a Workspace returns an overlay
// Workspace; the base source remains readable and unchanged.
func (WorkspaceSuite) TestOverlayWorkspaceFunctionalWritesDoNotMutateBaseSource(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	res, err := testutil.QueryWithClient[overlayWorkspaceResult](c, t, `{
		directory {
			withNewFile(path: "app/base.txt", contents: "base") {
				asWorkspace(cwd: "/app") {
					before: file(path: "base.txt") {
						contents
					}
					beforeEntries: directory(path: ".") {
						entries
					}
					changed: withNewFile(path: "base.txt", contents: "changed") {
						after: file(path: "base.txt") {
							contents
						}
						added: withNewFile(path: "new.txt", contents: "new") {
							newFile: file(path: "new.txt") {
								contents
							}
							afterEntries: directory(path: ".") {
								entries
							}
						}
					}
					afterBase: file(path: "base.txt") {
						contents
					}
					afterBaseEntries: directory(path: ".") {
						entries
					}
				}
			}
		}
	}`, nil)
	require.NoError(t, err)

	ws := res.Directory.WithNewFile.AsWorkspace
	require.Equal(t, "base", ws.Before.Contents)
	require.Equal(t, "base", ws.AfterBase.Contents)
	require.Equal(t, "changed", ws.Changed.After.Contents)
	require.Equal(t, "new", ws.Changed.Added.NewFile.Contents)
	requireEntry(t, ws.BeforeEntries.Entries, "base.txt")
	requireNoEntry(t, ws.BeforeEntries.Entries, "new.txt")
	requireEntry(t, ws.Changed.Added.AfterEntries.Entries, "base.txt")
	requireEntry(t, ws.Changed.Added.AfterEntries.Entries, "new.txt")
	requireEntry(t, ws.AfterBaseEntries.Entries, "base.txt")
	requireNoEntry(t, ws.AfterBaseEntries.Entries, "new.txt")
}

// TestOverlayGitRefWorkspaceReportsOverlayAsUncommitted asserts how functional
// writes compose with git state: the overlay keeps the base ref's commit and
// reports the overlay as uncommitted workspace state.
func (WorkspaceSuite) TestOverlayGitRefWorkspaceReportsOverlayAsUncommitted(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ref := syntheticWorkspaceGitRef(ctx, t, c)
	refID, err := ref.ID(ctx)
	require.NoError(t, err)

	res, err := testutil.QueryWithClient[overlayGitRefWorkspaceResult](c, t, `query GitRefOverlayWorkspace($ref: GitRefID!) {
		ref: loadGitRefFromID(id: $ref) {
			commit
			asWorkspace(cwd: "/app") {
				clean: git {
					head {
						commit
					}
					uncommitted {
						isEmpty
					}
				}
				changed: withNewFile(path: "overlay.txt", contents: "overlay") {
					overlayFile: file(path: "overlay.txt") {
						contents
					}
					git {
						head {
							commit
						}
						uncommitted {
							isEmpty
						}
					}
				}
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"ref": refID,
	}})
	require.NoError(t, err)

	baseCommit := strings.TrimSpace(res.Ref.Commit)
	require.Equal(t, baseCommit, strings.TrimSpace(res.Ref.AsWorkspace.Clean.Head.Commit))
	require.True(t, res.Ref.AsWorkspace.Clean.Uncommitted.IsEmpty)
	require.Equal(t, "overlay", res.Ref.AsWorkspace.Changed.OverlayFile.Contents)
	require.Equal(t, baseCommit, strings.TrimSpace(res.Ref.AsWorkspace.Changed.Git.Head.Commit))
	require.False(t, res.Ref.AsWorkspace.Changed.Git.Uncommitted.IsEmpty)
}

// TestSyntheticWorkspaceManagementAPIsDoNotDependOnHostState asserts that
// value workspaces do not accidentally route non-filesystem workspace APIs
// through the caller's current session. Listing APIs return empty results
// because no module graph is loaded; local-only mutations reject.
func (WorkspaceSuite) TestSyntheticWorkspaceManagementAPIsDoNotDependOnHostState(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := syntheticWorkspaceSource(c).AsWorkspace()

	assertSyntheticWorkspaceListsAreEmpty(ctx, t, ws)

	_, err := ws.Install(ctx, "github.com/dagger/dagger/modules/wolfi")
	require.Error(t, err)
}

// TestSyntheticWorkspaceFindUpRejectsInvalidNames asserts that Workspace.findUp
// searches for one path element while walking parents. Accepting slash or dot
// segments would turn a name lookup into path traversal and make source-backed
// and host-backed workspaces disagree.
func (WorkspaceSuite) TestSyntheticWorkspaceFindUpRejectsInvalidNames(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := syntheticWorkspaceSource(c).AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
		Cwd: "/app/nested",
	})

	for _, name := range []string{"", ".", "..", "../workspace.marker", "nested/leaf.txt"} {
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			_, err := ws.FindUp(ctx, name)
			require.Error(t, err)
		})
	}
}

func syntheticWorkspaceSource(c *dagger.Client) *dagger.Directory {
	return c.Directory().
		WithNewFile(".gitignore", "*.log\nbuild/\n").
		WithNewFile("README.md", "root readme").
		WithNewFile("workspace.marker", "root marker").
		WithNewFile("root.log", "ignored").
		WithNewFile("build/root.bin", "ignored").
		WithNewFile("app/main.txt", "app main").
		WithNewFile("app/debug.log", "ignored").
		WithNewFile("app/nested/leaf.txt", "leaf")
}

func syntheticWorkspaceGitRef(ctx context.Context, t *testctx.T, c *dagger.Client) *dagger.GitRef {
	t.Helper()
	gitDaemon, repoURL := gitService(ctx, t, c, syntheticWorkspaceSource(c))
	return c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).Head()
}

func assertSyntheticWorkspaceListsAreEmpty(ctx context.Context, t *testctx.T, ws *dagger.Workspace) {
	t.Helper()

	checks, err := ws.Checks().List(ctx)
	require.NoError(t, err)
	require.Empty(t, checks)

	generators, err := ws.Generators().List(ctx)
	require.NoError(t, err)
	require.Empty(t, generators)

	services, err := ws.Services().List(ctx)
	require.NoError(t, err)
	require.Empty(t, services)

	modules, err := ws.ModuleList(ctx)
	require.NoError(t, err)
	require.Empty(t, modules)

	envs, err := ws.EnvList(ctx)
	require.NoError(t, err)
	require.Empty(t, envs)
}

func requireGraphQLField(t require.TestingT, fields []graphqlField, name string) {
	if hasGraphQLField(fields, name) {
		return
	}
	require.Failf(t, "missing GraphQL field", "expected field %q in %v", name, graphqlFieldNames(fields))
}

func requireNoGraphQLField(t require.TestingT, fields []graphqlField, name string) {
	if !hasGraphQLField(fields, name) {
		return
	}
	require.Failf(t, "unexpected GraphQL field", "did not expect field %q in %v", name, graphqlFieldNames(fields))
}

func requireNoGraphQLType(t require.TestingT, types []graphqlType, name string) {
	for _, typ := range types {
		if typ.Name == name {
			require.Failf(t, "unexpected GraphQL type", "did not expect type %q in schema", name)
		}
	}
}

func hasGraphQLField(fields []graphqlField, name string) bool {
	for _, field := range fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

func graphqlFieldNames(fields []graphqlField) []string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Name)
	}
	return names
}

func requireEntry(t require.TestingT, entries []string, name string) {
	if hasWorkspaceEntry(entries, name) {
		return
	}
	require.Failf(t, "missing workspace entry", "expected %q in %v", name, entries)
}

func requireNoEntry(t require.TestingT, entries []string, name string) {
	if !hasWorkspaceEntry(entries, name) {
		return
	}
	require.Failf(t, "unexpected workspace entry", "did not expect %q in %v", name, entries)
}

func hasWorkspaceEntry(entries []string, name string) bool {
	for _, entry := range entries {
		if entry == name || entry == name+"/" {
			return true
		}
	}
	return false
}

type syntheticWorkspaceSchemaResult struct {
	Workspace graphqlType `json:"workspace"`
	Directory graphqlType `json:"directory"`
	GitRef    graphqlType `json:"gitRef"`
	Schema    struct {
		Types []graphqlType `json:"types"`
	} `json:"schema"`
}

type graphqlType struct {
	Name   string         `json:"name"`
	Fields []graphqlField `json:"fields"`
}

type graphqlField struct {
	Name string `json:"name"`
}

type gitRefWorkspaceResult struct {
	Ref struct {
		Commit      string `json:"commit"`
		AsWorkspace struct {
			Cwd        string                `json:"cwd"`
			Main       workspaceFileContents `json:"main"`
			Root       workspaceFileContents `json:"root"`
			Filtered   directoryEntries      `json:"filtered"`
			Unfiltered directoryEntries      `json:"unfiltered"`
			Git        workspaceGit          `json:"git"`
		} `json:"asWorkspace"`
	} `json:"ref"`
}

type overlayWorkspaceResult struct {
	Directory struct {
		WithNewFile struct {
			AsWorkspace struct {
				Before           workspaceFileContents `json:"before"`
				BeforeEntries    directoryEntries      `json:"beforeEntries"`
				Changed          overlayWorkspace      `json:"changed"`
				AfterBase        workspaceFileContents `json:"afterBase"`
				AfterBaseEntries directoryEntries      `json:"afterBaseEntries"`
			} `json:"asWorkspace"`
		} `json:"withNewFile"`
	} `json:"directory"`
}

type overlayWorkspace struct {
	After workspaceFileContents `json:"after"`
	Added struct {
		NewFile      workspaceFileContents `json:"newFile"`
		AfterEntries directoryEntries      `json:"afterEntries"`
	} `json:"added"`
}

type overlayGitRefWorkspaceResult struct {
	Ref struct {
		Commit      string `json:"commit"`
		AsWorkspace struct {
			Clean   workspaceGit `json:"clean"`
			Changed struct {
				OverlayFile workspaceFileContents `json:"overlayFile"`
				Git         workspaceGit          `json:"git"`
			} `json:"changed"`
		} `json:"asWorkspace"`
	} `json:"ref"`
}

type workspaceGit struct {
	Head struct {
		Commit string `json:"commit"`
	} `json:"head"`
	Uncommitted struct {
		IsEmpty bool `json:"isEmpty"`
	} `json:"uncommitted"`
}

type workspaceFileContents struct {
	Contents string `json:"contents"`
}

type directoryEntries struct {
	Entries []string `json:"entries"`
}
