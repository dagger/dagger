package core

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (GitSuite) TestGitBundleRoundTripAndStockInterop(ctx context.Context, t *testctx.T) {
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, sink.clientOpts()...)
	gitDaemon, repoURL := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("base.txt", "base\n"))
	remote := core.NewQuery(c).Git(repoURL, core.GitOpts{ExperimentalServiceHost: gitDaemon})
	baseSHA, err := remote.Head().CommitSHA(ctx)
	require.NoError(t, err)

	localDir := core.NewQuery(c).Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithServiceBinding("bundle-origin", gitDaemon).
		WithEnvVariable("REPO_URL", repoURL).
		WithExec([]string{"sh", "-ec", `
			git clone "$REPO_URL" /repo
			cd /repo
			git config user.name Bundle
			git config user.email bundle@example.com
			printf 'local\n' > local.txt
			git add local.txt
			git commit -m local
		`}).
		Directory("/repo")
	local := localDir.AsGit()
	localID, err := local.ID(ctx)
	require.NoError(t, err)
	baseID, err := local.Ref(baseSHA).ID(ctx)
	require.NoError(t, err)
	headSHA, err := local.Head().CommitSHA(ctx)
	require.NoError(t, err)

	type bundleResult struct {
		Node struct {
			Bundle struct {
				ID               string
				Version          int
				ObjectFormat     string
				PrerequisiteSHAs []string
				Refs             []struct {
					Name string
					SHA  string
				}
				File struct {
					ID   string
					Size int
				}
				Validated struct{ Version int }
			}
		}
	}
	created, err := testutil.QueryWithClient[bundleResult](c, t, `query($repo: ID!, $base: ID!) {
		node(id: $repo) {
			... on GitRepository {
				bundle(refs: ["refs/heads/main"], base: $base) {
					id
					version
					objectFormat
					prerequisiteSHAs
					refs { name sha }
					file: asFile { id size }
					validated: validate { version }
				}
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"repo": localID,
		"base": baseID,
	}})
	require.NoError(t, err)
	require.Equal(t, 3, created.Node.Bundle.Version)
	require.Equal(t, 3, created.Node.Bundle.Validated.Version)
	require.Equal(t, "sha1", created.Node.Bundle.ObjectFormat)
	require.Equal(t, []string{baseSHA}, created.Node.Bundle.PrerequisiteSHAs)
	require.Equal(t, headSHA, created.Node.Bundle.Refs[0].SHA)
	require.Equal(t, "refs/heads/main", created.Node.Bundle.Refs[0].Name)
	require.Positive(t, created.Node.Bundle.File.Size)

	parsed, err := testutil.QueryWithClient[struct {
		Node struct {
			Bundle struct {
				Version          int
				ObjectFormat     string
				PrerequisiteSHAs []string
				Refs             []struct{ Name, SHA string }
			}
		}
	}](c, t, `query($file: ID!) {
		node(id: $file) {
			... on File {
				bundle: asGitBundle {
					version objectFormat prerequisiteSHAs refs { name sha }
				}
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{"file": created.Node.Bundle.File.ID}})
	require.NoError(t, err)
	require.Equal(t, created.Node.Bundle.Version, parsed.Node.Bundle.Version)
	require.Equal(t, created.Node.Bundle.ObjectFormat, parsed.Node.Bundle.ObjectFormat)
	require.Equal(t, created.Node.Bundle.PrerequisiteSHAs, parsed.Node.Bundle.PrerequisiteSHAs)
	require.Equal(t, created.Node.Bundle.Refs[0].Name, parsed.Node.Bundle.Refs[0].Name)
	require.Equal(t, created.Node.Bundle.Refs[0].SHA, parsed.Node.Bundle.Refs[0].SHA)

	remoteID, err := remote.ID(ctx)
	require.NoError(t, err)
	imported, err := testutil.QueryWithClient[struct {
		Node struct {
			WithBundle struct {
				Ref struct {
					CommitSHA string
					Tree      struct{ File struct{ Contents string } }
				}
			}
		}
	}](c, t, `query($repo: ID!, $bundle: ID!, $head: String!) {
		node(id: $repo) {
			... on GitRepository {
				withBundle(bundle: $bundle, prerequisiteRef: "refs/heads/main") {
					ref(name: $head) {
						commitSHA
						tree(discardGitDir: true) { file(path: "local.txt") { contents } }
					}
				}
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"repo":   remoteID,
		"bundle": created.Node.Bundle.ID,
		"head":   headSHA,
	}})
	require.NoError(t, err)
	require.Equal(t, headSHA, imported.Node.WithBundle.Ref.CommitSHA)
	require.Equal(t, "local\n", imported.Node.WithBundle.Ref.Tree.File.Contents)

	_, err = testutil.QueryWithClient[struct{ Node struct{ CommitSHA string } }](c, t, `query($repo: ID!, $bundle: ID!) {
		node(id: $repo) {
			... on GitRepository {
				withBundle(bundle: $bundle, prerequisiteRef: "refs/heads/main") {
					ref(name: "refs/dagger/bundle/prerequisites/0") { commitSHA }
				}
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"repo":   remoteID,
		"bundle": created.Node.Bundle.ID,
	}})
	require.Error(t, err)
	require.ErrorContains(t, err, `repository does not contain ref "refs/dagger/bundle/prerequisites/0"`)

	stock := core.NewQuery(c).Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithServiceBinding("bundle-origin", gitDaemon).
		WithEnvVariable("REPO_URL", repoURL)
	stockID, err := stock.ID(ctx)
	require.NoError(t, err)
	stockResult, err := testutil.QueryWithClient[struct {
		Node struct {
			Mounted struct{ Run struct{ Stdout string } }
		}
	}](c, t, `query($container: ID!, $file: ID!, $args: [String!]!) {
		node(id: $container) {
			... on Container {
				mounted: withMountedFile(path: "/bundle", source: $file) {
					run: withExec(args: $args) { stdout }
				}
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"container": stockID,
		"file":      created.Node.Bundle.File.ID,
		"args": []string{"sh", "-ec", `
			git clone "$REPO_URL" /stock >/dev/null
			git -C /stock fetch /bundle refs/heads/main:refs/dagger/imported >/dev/null
			git -C /stock show refs/dagger/imported:local.txt
		`},
	}})
	require.NoError(t, err)
	require.Equal(t, "local\n", stockResult.Node.Mounted.Run.Stdout)

	otherDaemon, otherURL := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("other.txt", "other\n"))
	other := core.NewQuery(c).Git(otherURL, core.GitOpts{ExperimentalServiceHost: otherDaemon})
	otherID, err := other.ID(ctx)
	require.NoError(t, err)
	_, err = testutil.QueryWithClient[struct{ ID string }](c, t, `query($repo: ID!, $bundle: ID!) {
		node(id: $repo) {
			... on GitRepository { withBundle(bundle: $bundle) { id } }
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"repo":   otherID,
		"bundle": created.Node.Bundle.ID,
	}})
	require.Error(t, err)
	require.ErrorContains(t, err, "prerequisite")
	require.ErrorContains(t, err, baseSHA)

	// The import owns the prerequisite's history by packing it directly from
	// the mirror, not by fetching it.
	require.NoError(t, c.Close())
	traces, _ := sink.capture()
	var methods []string
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.Name != "git bundle import" || span.EndTimeUnixNano <= span.StartTimeUnixNano {
						continue
					}
					for _, attr := range span.Attributes {
						if attr.Key == "dagger.git.bundle.prerequisites.method" {
							methods = append(methods, attr.Value.GetStringValue())
						}
					}
				}
			}
		}
	}
	require.Contains(t, methods, "pack")
	require.NotContains(t, methods, "fetch")
}

func (GitSuite) TestGitBundlePreservesAnnotatedTag(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	repoCtr := core.NewQuery(c).Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"sh", "-ec", `
			git init -q -b main /repo
			git -C /repo config user.name Bundle
			git -C /repo config user.email bundle@example.com
			echo content > /repo/file.txt
			git -C /repo add file.txt
			git -C /repo commit -q -m initial
			git -C /repo tag -a v1.0.0 -m release
		`})
	tagSHA, err := repoCtr.
		WithExec([]string{"git", "-C", "/repo", "rev-parse", "refs/tags/v1.0.0"}).
		Stdout(ctx)
	require.NoError(t, err)
	tagSHA = strings.TrimSpace(tagSHA)

	bundle := repoCtr.Directory("/repo").AsGit().Bundle([]string{"refs/tags/v1.0.0"})
	refs, err := bundle.Refs(ctx)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	bundleSHA, err := refs[0].SHA(ctx)
	require.NoError(t, err)
	require.Equal(t, tagSHA, bundleSHA)

	gitDaemon, repoURL := gitService(ctx, t, c, repoCtr.Directory("/repo"))
	remoteBundle := core.NewQuery(c).Git(repoURL, core.GitOpts{ExperimentalServiceHost: gitDaemon}).
		Bundle([]string{"refs/tags/v1.0.0"})
	remoteRefs, err := remoteBundle.Refs(ctx)
	require.NoError(t, err)
	require.Len(t, remoteRefs, 1)
	remoteBundleSHA, err := remoteRefs[0].SHA(ctx)
	require.NoError(t, err)
	require.Equal(t, tagSHA, remoteBundleSHA)

	objectType, err := core.NewQuery(c).Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithMountedFile("/repository.bundle", remoteBundle.AsFile()).
		WithExec([]string{"sh", "-ec", `
			git init -q --bare /verify
			git -C /verify fetch -q /repository.bundle refs/tags/v1.0.0:refs/tags/v1.0.0
			git -C /verify cat-file -t refs/tags/v1.0.0
		`}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "tag", strings.TrimSpace(objectType))
}

// Bundling a remote annotated tag fetches the tag object from origin while
// the mirror is locked. That fetch must negotiate against the tag's history,
// already copied from the mirror, rather than clone it all again: the origin
// refuses any pack of the tag object that has no haves to exclude.
func (GitSuite) TestGitBundleRemoteAnnotatedTagFetchesOnlyTag(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	origin := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git", "git-daemon"}).
		With(gitUserConfig).
		WithWorkdir("/repos/repo").
		WithExec([]string{"sh", "-ec", `
			git init -q -b main
			for i in 1 2 3 4 5; do echo $i > file.txt; git add file.txt; git commit -q -m "commit$i"; done
			git tag -a v1.0.0 -m release HEAD~1
			git config --system uploadpack.packObjectsHook /hook.sh
			cat > /hook.sh <<EOF
#!/bin/sh
set -eu
in=\$(mktemp)
cat > "\$in"
# upload-pack feeds pack-objects the wants, then --not, then the haves.
if grep -qx $(git rev-parse refs/tags/v1.0.0) "\$in" &&
	! sed '1,/^--not\$/d' "\$in" | grep -q '^[0-9a-f]'; then
	echo "refusing to send the tag's full history" >&2
	exit 1
fi
exec "\$@" < "\$in"
EOF
			chmod +x /hook.sh
		`})
	tagSHA, err := origin.WithExec([]string{"git", "rev-parse", "refs/tags/v1.0.0"}).Stdout(ctx)
	require.NoError(t, err)
	tagSHA = strings.TrimSpace(tagSHA)
	svc := origin.WithExposedPort(9418).
		WithDefaultArgs([]string{"git", "daemon", "--export-all", "--base-path=/repos"}).
		AsService()
	host, err := svc.Hostname(ctx)
	require.NoError(t, err)
	_, err = svc.Start(ctx)
	require.NoError(t, err)
	defer svc.Stop(ctx)
	repoURL := "git://" + host + "/repo"

	client := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithServiceBinding("origin", svc)
	// The hook is live: a fetch of the tag with no history to negotiate fails.
	_, err = client.WithExec([]string{"sh", "-ec", `
		git init -q --bare /cold
		git -C /cold fetch -q git://origin/repo refs/tags/v1.0.0:refs/tags/v1.0.0
	`}).Sync(ctx)
	require.Error(t, err)

	repo := core.NewQuery(c).Git(repoURL, core.GitOpts{ExperimentalServiceHost: svc})
	for _, refs := range [][]string{
		{"refs/tags/v1.0.0"},
		{"refs/heads/main", "refs/tags/v1.0.0"},
	} {
		bundle := repo.Bundle(refs)
		bundleRefs, err := bundle.Refs(ctx)
		require.NoError(t, err, "bundle %v", refs)
		require.Len(t, bundleRefs, len(refs))
		tag := bundleRefs[len(refs)-1]
		name, err := tag.Name(ctx)
		require.NoError(t, err)
		require.Equal(t, "refs/tags/v1.0.0", name)
		sha, err := tag.Sha(ctx)
		require.NoError(t, err)
		require.Equal(t, tagSHA, sha)

		out, err := client.
			WithMountedFile("/repository.bundle", bundle.AsFile()).
			WithExec([]string{"sh", "-ec", `
				git init -q --bare /verify
				git -C /verify fetch -q /repository.bundle 'refs/*:refs/*'
				git -C /verify for-each-ref --format='%(refname)'
				git -C /verify cat-file -t refs/tags/v1.0.0
				git -C /verify rev-list --count refs/tags/v1.0.0
			`}).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		n := len(lines)
		require.Equal(t, refs, lines[:n-2], "bundle must carry only the requested refs")
		require.Equal(t, []string{"tag", "4"}, lines[n-2:])
	}
}

func (GitSuite) TestGitBundleImportAfterPrerequisiteRefAdvances(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	gitDaemon, repoURL := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("base.txt", "base\n"))

	// Capture a bundle rooted at the remote's initial main, without resolving
	// that remote through core Git. This keeps the later import's remote lookup
	// honest: its first view of main is the advanced tip.
	localCtr := core.NewQuery(c).Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithServiceBinding("bundle-origin", gitDaemon).
		WithEnvVariable("REPO_URL", repoURL).
		WithExec([]string{"sh", "-ec", `
			git clone "$REPO_URL" /repo
			cd /repo
			git config user.name Bundle
			git config user.email bundle@example.com
			printf 'captured\n' > captured.txt
			git add captured.txt
			git commit -qm captured
			git rev-parse HEAD~1
		`})
	baseSHA, err := localCtr.Stdout(ctx)
	require.NoError(t, err)
	baseSHA = strings.TrimSpace(baseSHA)
	local := localCtr.Directory("/repo").AsGit()
	localID, err := local.ID(ctx)
	require.NoError(t, err)
	baseID, err := local.Ref(baseSHA).ID(ctx)
	require.NoError(t, err)
	capturedSHA, err := local.Head().CommitSHA(ctx)
	require.NoError(t, err)

	bundle, err := testutil.QueryWithClient[struct {
		Node struct{ Bundle struct{ ID string } }
	}](c, t, `query($repo: ID!, $base: ID!) {
		node(id: $repo) {
			... on GitRepository {
				bundle(refs: ["refs/heads/main"], base: $base) { id }
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"repo": localID,
		"base": baseID,
	}})
	require.NoError(t, err)

	// Advance the same ref after capture. A prerequisite ref is a fetch hint,
	// not the captured identity: restore must still use the bundle's exact base
	// SHA and captured head rather than substituting this new workspace state.
	_, err = core.NewQuery(c).Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithServiceBinding("bundle-origin", gitDaemon).
		WithEnvVariable("REPO_URL", repoURL).
		WithExec([]string{"sh", "-ec", `
			git clone "$REPO_URL" /repo
			cd /repo
			git config user.name Advance
			git config user.email advance@example.com
			printf 'current\n' > current.txt
			git add current.txt
			git commit -m current
			git push origin HEAD:main
		`}).
		Sync(ctx)
	require.NoError(t, err)

	remote := core.NewQuery(c).Git(repoURL, core.GitOpts{ExperimentalServiceHost: gitDaemon})
	remoteID, err := remote.ID(ctx)
	require.NoError(t, err)
	imported, err := testutil.QueryWithClient[struct {
		Node struct {
			WithBundle struct {
				Ref struct {
					CommitSHA string
					Tree      struct{ Entries []string }
				}
			}
		}
	}](c, t, `query($repo: ID!, $bundle: ID!, $head: String!) {
		node(id: $repo) {
			... on GitRepository {
				withBundle(bundle: $bundle, prerequisiteRef: "refs/heads/main") {
					ref(name: $head) {
						commitSHA
						tree(discardGitDir: true) { entries }
					}
				}
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"repo":   remoteID,
		"bundle": bundle.Node.Bundle.ID,
		"head":   capturedSHA,
	}})
	require.NoError(t, err)
	require.Equal(t, capturedSHA, imported.Node.WithBundle.Ref.CommitSHA)
	require.Contains(t, imported.Node.WithBundle.Ref.Tree.Entries, "captured.txt")
	require.NotContains(t, imported.Node.WithBundle.Ref.Tree.Entries, "current.txt")
}

func (GitSuite) TestGitBundleMalformedAndResourceFailures(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	malformedID, err := core.NewQuery(c).Directory().WithNewFile("bad.bundle", "not a bundle\n").File("bad.bundle").ID(ctx)
	require.NoError(t, err)
	_, err = testutil.QueryWithClient[struct{ Version int }](c, t, `query($file: ID!) {
		node(id: $file) { ... on File { asGitBundle { version } } }
	}`, &testutil.QueryOptions{Variables: map[string]any{"file": malformedID}})
	require.Error(t, err)
	require.ErrorContains(t, err, "signature")

	large := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"truncate", "-s", fmt.Sprint((128 << 20) + 1), "/large.bundle"}).
		File("/large.bundle")
	largeID, err := large.ID(ctx)
	require.NoError(t, err)
	_, err = testutil.QueryWithClient[struct{ Version int }](c, t, `query($file: ID!) {
		node(id: $file) { ... on File { asGitBundle { version } } }
	}`, &testutil.QueryOptions{Variables: map[string]any{"file": largeID}})
	require.Error(t, err)
	require.ErrorContains(t, err, "size")
	require.ErrorContains(t, err, fmt.Sprint(128<<20))

	gitDaemon, repoURL := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("base.txt", "base\n"))
	repo := core.NewQuery(c).Git(repoURL, core.GitOpts{ExperimentalServiceHost: gitDaemon})
	repoID, err := repo.ID(ctx)
	require.NoError(t, err)

	sha256File := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"sh", "-ec", `
			git init --object-format=sha256 /sha256
			cd /sha256
			git config user.name Bundle
			git config user.email bundle@example.com
			printf 'sha256\n' > file.txt
			git add file.txt
			git commit -m sha256
			git bundle create --version=3 /sha256.bundle refs/heads/master
		`}).
		File("/sha256.bundle")
	sha256FileID, err := sha256File.ID(ctx)
	require.NoError(t, err)
	sha256Bundle, err := testutil.QueryWithClient[struct {
		Node struct {
			Bundle struct {
				ID           string
				ObjectFormat string
				Validated    struct{ Version int }
			}
		}
	}](c, t, `query($file: ID!) {
		node(id: $file) {
			... on File {
				bundle: asGitBundle { id objectFormat validated: validate { version } }
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{"file": sha256FileID}})
	require.NoError(t, err)
	require.Equal(t, "sha256", sha256Bundle.Node.Bundle.ObjectFormat)
	require.Equal(t, 3, sha256Bundle.Node.Bundle.Validated.Version)
	_, err = testutil.QueryWithClient[struct{ Node struct{ ID string } }](c, t, `query($repo: ID!, $bundle: ID!) {
		node(id: $repo) {
			... on GitRepository { imported: withBundle(bundle: $bundle) { id } }
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"repo":   repoID,
		"bundle": sha256Bundle.Node.Bundle.ID,
	}})
	require.Error(t, err)
	require.ErrorContains(t, err, "object format")
	require.ErrorContains(t, err, "sha256")

	bundle, err := testutil.QueryWithClient[struct {
		Node struct {
			Bundle struct{ File struct{ ID string } }
		}
	}](c, t, `query($repo: ID!) {
		node(id: $repo) {
			... on GitRepository { bundle(refs: ["refs/heads/main"]) { file: asFile { id } } }
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{"repo": repoID}})
	require.NoError(t, err)

	truncateBase := core.NewQuery(c).Container().From(alpineImage)
	truncateBaseID, err := truncateBase.ID(ctx)
	require.NoError(t, err)
	truncated, err := testutil.QueryWithClient[struct {
		Node struct {
			Mounted struct {
				Run struct{ File struct{ ID string } }
			}
		}
	}](c, t, `query($container: ID!, $bundle: ID!) {
		node(id: $container) {
			... on Container {
				mounted: withMountedFile(path: "/bundle", source: $bundle) {
					run: withExec(args: ["sh", "-ec", "size=$(stat -c %s /bundle); head -c $((size - 1)) /bundle > /truncated.bundle"]) {
						file(path: "/truncated.bundle") { id }
					}
				}
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"container": truncateBaseID,
		"bundle":    bundle.Node.Bundle.File.ID,
	}})
	require.NoError(t, err)

	_, err = testutil.QueryWithClient[struct{ Version int }](c, t, `query($file: ID!) {
		node(id: $file) {
			... on File { asGitBundle { validate { version } } }
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{"file": truncated.Node.Mounted.Run.File.ID}})
	require.Error(t, err)
	require.True(t,
		strings.Contains(err.Error(), "truncated") || strings.Contains(err.Error(), "checksum"),
		err.Error())
}
