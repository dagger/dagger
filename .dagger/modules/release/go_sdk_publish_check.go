package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/release/internal/dagger"
)

// Exercise the Go SDK publish of the committed history against a local git
// destination.
// +check
func (r *Release) GoSdkPublishCheck(
	ctx context.Context,

	// Repository whose HEAD is the candidate to publish. The check publishes a
	// disposable copy of its history, so commit IDs match the candidate's.
	// +defaultPath="/"
	source *dagger.GitRepository,

	// git-filter-repo callback that the Go SDK publish applies
	// +defaultPath="/.dagger/modules/go-client-dev/git-filter-callback.py"
	callback *dagger.File,
) (rerr error) {
	candidate, err := source.Head().Commit(ctx)
	if err != nil {
		return err
	}

	fixture := goSDKGitContainer().
		WithDirectory("/src", source.Head().Tree(dagger.GitRefTreeOpts{Depth: -1})).
		WithWorkdir("/src").
		WithEnvVariable("CANDIDATE", candidate).
		WithExec([]string{"sh", "-ec", `
fail() { echo "$*" >&2; exit 1; }
head=$(git rev-parse HEAD)
[ "$head" = "$CANDIDATE" ] || fail "history copy is at $head, not the candidate $CANDIDATE"
last=$(git log -1 --first-parent --format=%H HEAD -- sdk/go)
seed=$(git rev-parse --verify -q "$last^") || fail "retained history has no parent for $last, the last sdk/go change"
tag=$(tr -d '[:space:]' < internal/version/VERSION)
git checkout -q --detach
git branch -f main "$CANDIDATE"
git tag -f "$tag" "$CANDIDATE"
printf '%s %s' "$seed" "$tag" > /fixture
`})
	fixtureOut, err := fixture.File("/fixture").Contents(ctx)
	if err != nil {
		return err
	}
	seed, releaseTag, _ := strings.Cut(fixtureOut, " ")
	candidateHistory := fixture.Directory("/src")
	candidateRepo := candidateHistory.AsGit()
	seedRepo := goSDKGitContainer().
		WithDirectory("/src", candidateHistory).
		WithWorkdir("/src").
		WithExec([]string{"git", "branch", "-f", "main", seed}).
		WithExec([]string{"git", "tag", "-d", releaseTag}).
		Directory("/src").
		AsGit()

	// The publish exports sourceRepo.Ref(tag), not the commit the workflow
	// checked out, so a named ref that drifted from the candidate would
	// publish other content.
	for _, ref := range []struct {
		repo *dagger.GitRepository
		name string
		want string
	}{
		{candidateRepo, "main", candidate},
		{candidateRepo, releaseTag, candidate},
		{seedRepo, "main", seed},
	} {
		got, err := ref.repo.Ref(ref.name).Commit(ctx)
		if err != nil {
			return err
		}
		if got != ref.want {
			return fmt.Errorf("fixture ref %s resolves to %s, want %s", ref.name, got, ref.want)
		}
	}

	destSvc, err := gitSmartHTTPService("", goSDKGitContainer().
		WithExec([]string{"sh", "-ec", `
git init -q --bare --initial-branch=main /srv/dest.git
git -C /srv/dest.git config http.receivepack true
`}).
		Directory("/srv")).
		Start(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_, err := destSvc.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
		if rerr == nil && err != nil {
			rerr = fmt.Errorf("stop Go SDK destination: %w", err)
		}
	}()
	endpoint, err := destSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "http"})
	if err != nil {
		return err
	}
	destRemote := endpoint + "/dest.git"
	destGit := func(script string) (string, error) {
		out, err := goSDKGitContainer().
			WithMountedDirectory("/candidate", candidateHistory).
			WithEnvVariable("DEST_REMOTE", destRemote).
			WithEnvVariable("CANDIDATE", candidate).
			WithEnvVariable("RELEASE_TAG", releaseTag).
			WithEnvVariable("CACHE_BUSTER", randomID()).
			WithExec([]string{"sh", "-ec", "fail() { echo \"$*\" >&2; exit 1; }\n" + script}).
			Stdout(ctx)
		if err != nil {
			return "", fmt.Errorf("inspect Go SDK destination: %w", err)
		}
		return strings.TrimSpace(out), nil
	}

	goSDK := dag.GoClientDev(r.Workspace)
	if err := goSDK.Release(ctx, "main", dagger.GoClientDevReleaseOpts{
		SourceRepo: seedRepo,
		DestRemote: destRemote,
		Callback:   callback,
	}); err != nil {
		return fmt.Errorf("seed Go SDK destination from %s: %w", seed, err)
	}
	seedTip, err := destGit(`git ls-remote "$DEST_REMOTE" refs/heads/main | cut -f1`)
	if err != nil {
		return err
	}
	if seedTip == "" {
		return fmt.Errorf("seeding from %s did not create main in the Go SDK destination", seed)
	}

	if err := goSDK.ReleaseDryRun(ctx, dagger.GoClientDevReleaseDryRunOpts{
		SourceRepo: candidateRepo,
		SourceTag:  "main",
		DestRemote: destRemote,
		Callback:   callback,
	}); err != nil {
		return fmt.Errorf("dry-run Go SDK publish of main: %w", err)
	}
	for _, ref := range []string{"main", releaseTag} {
		if err := goSDK.Release(ctx, ref, dagger.GoClientDevReleaseOpts{
			SourceRepo: candidateRepo,
			DestRemote: destRemote,
			Callback:   callback,
		}); err != nil {
			return fmt.Errorf("publish Go SDK %s: %w", ref, err)
		}
	}

	published, err := destGit(`
git clone -q "$DEST_REMOTE" dest
cd dest
main=$(git rev-parse origin/main)
tagged=$(git rev-parse "refs/tags/$RELEASE_TAG^{commit}")
[ "$tagged" = "$main" ] || fail "tag $RELEASE_TAG is at $tagged, main is at $main"
[ "$main" != "` + seedTip + `" ] || fail "main did not advance from the seed"
git merge-base --is-ancestor "` + seedTip + `" "$main" || fail "main $main does not fast-forward the seed"
git checkout -q "$RELEASE_TAG"

[ "$(head -n1 go.mod)" = "module dagger.io/dagger" ] || fail "go.mod is not at the root of the Go SDK"
for f in client.go core/core.gen.go dag/dag.gen.go engineconn/version.gen.go; do
	[ -f "$f" ] || fail "Go SDK is missing $f"
done
[ ! -e sdk ] || fail "Go SDK still contains the monorepo layout"
grep -qxF "const CLIVersion = \"${RELEASE_TAG#v}\"" engineconn/version.gen.go || fail "engineconn/version.gen.go does not pin ${RELEASE_TAG#v}"

git -C /candidate ls-tree -r "$CANDIDATE:sdk/go" | grep -v '	go.mod$' > /tmp/want
git ls-tree -r HEAD | grep -v '	go.mod$' > /tmp/got
diff -u /tmp/want /tmp/got >&2 || fail "Go SDK tree differs from sdk/go at $CANDIDATE"

replace='github.com/dagger/dagger( [^ ]+)? =>'
git -C /candidate show "$CANDIDATE:sdk/go/go.mod" > /tmp/go.mod
if ! grep -qE "$replace" /tmp/go.mod; then
	cmp -s /tmp/go.mod go.mod || fail "go.mod differs from sdk/go/go.mod at $CANDIDATE"
fi
replaced=0
for c in $(git -C /candidate rev-list "$CANDIDATE" -- sdk/go/go.mod); do
	if git -C /candidate show "$c:sdk/go/go.mod" | grep -qE "$replace"; then
		replaced=$((replaced + 1))
	fi
done
[ "$replaced" -gt 0 ] || fail "no sdk/go/go.mod in the history replaces github.com/dagger/dagger, so the callback is not exercised"
for c in $(git rev-list origin/main -- go.mod); do
	if git show "$c:go.mod" | grep -qE "$replace"; then
		fail "published $c keeps the github.com/dagger/dagger replace"
	fi
done
echo "$main $replaced"
`)
	if err != nil {
		return err
	}
	publishedTip, replaced, _ := strings.Cut(published, " ")

	refsBefore, err := destGit(`git ls-remote "$DEST_REMOTE"`)
	if err != nil {
		return err
	}
	hostname, err := destSvc.Hostname(ctx)
	if err != nil {
		return err
	}
	// The Go SDK publish functions are cached: an identical call would
	// return without pushing, so address the same destination differently.
	rerunRemote := "http://" + hostname + "/dest.git"
	for _, ref := range []string{"main", releaseTag} {
		if err := goSDK.Release(ctx, ref, dagger.GoClientDevReleaseOpts{
			SourceRepo: candidateRepo,
			DestRemote: rerunRemote,
			Callback:   callback,
		}); err != nil {
			return fmt.Errorf("republish Go SDK %s: %w", ref, err)
		}
	}
	refsAfter, err := destGit(`git ls-remote "$DEST_REMOTE"`)
	if err != nil {
		return err
	}
	if refsAfter != refsBefore {
		return fmt.Errorf("republishing changed the Go SDK destination refs:\nbefore:\n%s\nafter:\n%s", refsBefore, refsAfter)
	}

	fmt.Printf("Go SDK publish of %s (%s): seed %s published as %s, candidate published as %s; callback dropped %s monorepo replaces\n",
		candidate, releaseTag, seed, seedTip, publishedTip, replaced)
	return nil
}

func goSDKGitContainer() *dagger.Container {
	return dag.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "git"})
}
