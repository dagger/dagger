package core

import (
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

func (EngineSuite) TestDiskPersistenceAcrossRestart(ctx context.Context, t *testctx.T) {
	const persistenceTestGCThresholdBytes = "1000000000000000"

	engineWithPersistenceTestGC := func(ctx context.Context, t *testctx.T) func(*dagger.Container) *dagger.Container {
		t.Helper()
		return engineWithConfig(
			ctx,
			t,
			engineConfigWithEnabled(true),
			engineConfigWithGC(
				persistenceTestGCThresholdBytes,
				"0",
				persistenceTestGCThresholdBytes,
				"0",
			),
		)
	}

	startEngine := func(
		client *dagger.Client,
		ctx context.Context,
		t *testctx.T,
		stateKey string,
		opts ...func(*dagger.Container) *dagger.Container,
	) (*dagger.Service, *dagger.Service, *dagger.Client) {
		t.Helper()

		engineCtr := devEngineContainerWithStateKey(client, stateKey, opts...)
		upstreamSvc := devEngineContainerAsService(engineCtr)
		engineSvc, err := client.Host().Tunnel(upstreamSvc).Start(ctx)
		require.NoError(t, err)

		endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)

		engineClient, err := dagger.Connect(
			ctx,
			dagger.WithRunnerHost(endpoint),
			dagger.WithLogOutput(testutil.NewTWriter(t)),
		)
		require.NoError(t, err)
		return upstreamSvc, engineSvc, engineClient
	}

	stopEngine := func(
		ctx context.Context,
		t *testctx.T,
		upstreamSvc *dagger.Service,
		engineSvc *dagger.Service,
		engineClient *dagger.Client,
	) {
		t.Helper()
		if engineClient != nil {
			require.NoError(t, engineClient.Close())
		}
		if upstreamSvc != nil {
			_, err := upstreamSvc.Stop(ctx)
			require.NoError(t, err)
		}
		if engineSvc != nil {
			_, err := engineSvc.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
			require.NoError(t, err)
		}
	}

	t.Run("local cache survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-local-cache-state-" + identity.NewID()

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		_, err := engineClientA.
			Container().
			From(alpineImage).
			WithExec([]string{"sh", "-ec", "echo phase7-local-cache > /tmp/phase7.txt"}).
			Sync(ctx)
		require.NoError(t, err)

		entryCountA, err := engineClientA.Engine().LocalCache().EntrySet().EntryCount(ctx)
		require.NoError(t, err)
		require.Greater(t, entryCountA, 0)

		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		entryCount, err := engineClientB.Engine().LocalCache().EntrySet().EntryCount(ctx)
		require.NoError(t, err)
		require.Greater(t, entryCount, 0)
	})

	t.Run("function cache control survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-function-cache-state-" + identity.NewID()
		moduleSrc := `package main

import "crypto/rand"

type Test struct{}

func (m *Test) TestAlwaysCache() string {
	return rand.Text()
}
`

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		modA := modInit(t, engineClientA, "go", moduleSrc)
		outA, err := modA.
			WithEnvVariable("CACHE_BUST", identity.NewID()).
			With(daggerCall("test-always-cache")).
			Stdout(ctx)
		require.NoError(t, err)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		modB := modInit(t, engineClientB, "go", moduleSrc)
		outB, err := modB.
			WithEnvVariable("CACHE_BUST", identity.NewID()).
			With(daggerCall("test-always-cache")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, outA, outB, "always-cached function result should survive engine restart")
	})

	t.Run("contextual function cache survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-contextual-function-cache-state-" + identity.NewID()
		moduleSrc := `package main

import (
	"context"
	"crypto/rand"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) ContextDir(
	ctx context.Context,
	// +defaultPath="."
	dir *dagger.Directory,
) (string, error) {
	contents, err := dir.File("dagger.json").Contents(ctx)
	if err != nil {
		return "", err
	}
	return rand.Text() + "|" + contents, nil
}

func (m *Test) ContextFile(
	ctx context.Context,
	// +defaultPath="dagger.json"
	file *dagger.File,
) (string, error) {
	contents, err := file.Contents(ctx)
	if err != nil {
		return "", err
	}
	return rand.Text() + "|" + contents, nil
}

func (m *Test) ContextGitRepository(
	ctx context.Context,
	// +defaultPath="."
	repo *dagger.GitRepository,
) (string, error) {
	commit, err := repo.Head().Commit(ctx)
	if err != nil {
		return "", err
	}
	return rand.Text() + "|" + commit, nil
}

func (m *Test) ContextGitRef(
	ctx context.Context,
	// +defaultPath="."
	ref *dagger.GitRef,
) (string, error) {
	commit, err := ref.Commit(ctx)
	if err != nil {
		return "", err
	}
	return rand.Text() + "|" + commit, nil
}
`

		getMod := func(client *dagger.Client) *dagger.Container {
			return modInit(t, client, "go", moduleSrc).
				WithExec([]string{"git", "add", "."}).
				WithExec([]string{"git", "commit", "-m", "make HEAD exist"})
		}

		runCalls := func(ctx context.Context, t *testctx.T, engineClient *dagger.Client) map[string]string {
			t.Helper()

			mod := getMod(engineClient)
			outputs := map[string]string{}
			for _, fn := range []string{
				"context-dir",
				"context-file",
				"context-git-repository",
				"context-git-ref",
			} {
				out, err := mod.With(daggerCall(fn)).Stdout(ctx)
				require.NoError(t, err)
				outputs[fn] = out
			}
			return outputs
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		outA := runCalls(ctx, t, engineClientA)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		outB := runCalls(ctx, t, engineClientB)
		require.Equal(t, outA, outB, "contextual function results should survive engine restart")
	})

	t.Run("container withExec output on host mount survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-container-host-mount-state-" + identity.NewID()

		hostDirA := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(hostDirA, "input.txt"), []byte("same-content\n"), 0o600))
		hostDirB := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(hostDirB, "input.txt"), []byte("same-content\n"), 0o600))

		runChain := func(ctx context.Context, t *testctx.T, engineClient *dagger.Client, hostPath string) string {
			t.Helper()
			workDir := engineClient.
				Container().
				From(alpineImage).
				WithMountedDirectory("/src", engineClient.Host().Directory(hostPath)).
				WithExec([]string{"sh", "-ec", "mkdir -p /work && cp /src/input.txt /work/input.txt && head -c 32 /dev/urandom | sha256sum | cut -d' ' -f1 > /work/random.txt"}).
				Directory("/work")

			entries, err := workDir.Entries(ctx)
			require.NoError(t, err)
			require.Contains(t, entries, "input.txt")
			require.Contains(t, entries, "random.txt")

			randomContents, err := workDir.File("random.txt").Contents(ctx)
			require.NoError(t, err)
			return strings.TrimSpace(randomContents)
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		randomA := runChain(ctx, t, engineClientA, hostDirA)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		randomB := runChain(ctx, t, engineClientB, hostDirB)
		require.Equal(t, randomA, randomB, "withExec output should survive engine restart for equivalent host-mounted input")
	})

	t.Run("container withExec output on host mounted file survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-container-host-file-state-" + identity.NewID()

		hostDirA := t.TempDir()
		hostFileA := filepath.Join(hostDirA, "input.txt")
		require.NoError(t, os.WriteFile(hostFileA, []byte("same-content\n"), 0o600))
		hostDirB := t.TempDir()
		hostFileB := filepath.Join(hostDirB, "input.txt")
		require.NoError(t, os.WriteFile(hostFileB, []byte("same-content\n"), 0o600))

		runChain := func(ctx context.Context, t *testctx.T, engineClient *dagger.Client, hostPath string) string {
			t.Helper()
			workDir := engineClient.
				Container().
				From(alpineImage).
				WithMountedFile("/src/input.txt", engineClient.Host().File(hostPath)).
				WithExec([]string{"sh", "-ec", "mkdir -p /work && cp /src/input.txt /work/input.txt && head -c 32 /dev/urandom | sha256sum | cut -d' ' -f1 > /work/random.txt"}).
				Directory("/work")

			entries, err := workDir.Entries(ctx)
			require.NoError(t, err)
			require.Contains(t, entries, "input.txt")
			require.Contains(t, entries, "random.txt")

			randomContents, err := workDir.File("random.txt").Contents(ctx)
			require.NoError(t, err)
			return strings.TrimSpace(randomContents)
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		randomA := runChain(ctx, t, engineClientA, hostFileA)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		randomB := runChain(ctx, t, engineClientB, hostFileB)
		require.Equal(t, randomA, randomB, "withExec output should survive engine restart for equivalent host-mounted file input")
	})

	t.Run("git repository and ref survive restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-git-restart-state-" + identity.NewID()
		repoDir := t.TempDir()

		runGit := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = repoDir
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
		}

		runGit("init")
		runGit("config", "user.email", "dagger@example.com")
		runGit("config", "user.name", "Dagger Tests")
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("git persistence\n"), 0o600))
		runGit("add", "README.md")
		runGit("commit", "-m", "initial commit")

		type gitRunOutput struct {
			commit  string
			random  string
			readme  string
			layered string
			summary string
		}

		runChain := func(ctx context.Context, t *testctx.T, engineClient *dagger.Client, layerExtra bool) gitRunOutput {
			t.Helper()

			repo := engineClient.Host().Directory(repoDir).AsGit()
			ref := repo.Head()
			commitFromRef, err := ref.Commit(ctx)
			require.NoError(t, err)

			ctr := engineClient.
				Container().
				From(alpineImage).
				WithExec([]string{"apk", "add", "git"}).
				WithMountedDirectory("/repo", ref.Tree()).
				WithExec([]string{"sh", "-ec", `
set -eu
mkdir -p /work
git -C /repo rev-parse HEAD > /work/commit.txt
cat /repo/README.md > /work/readme.txt
head -c 32 /dev/urandom | sha256sum | cut -d' ' -f1 > /work/random.txt
`})

			if layerExtra {
				ctr = ctr.WithExec([]string{"sh", "-ec", `
set -eu
printf 'layered\n' > /work/layered.txt
{
  printf 'commit='
  tr -d '\n' < /work/commit.txt
  printf '\nrandom='
  tr -d '\n' < /work/random.txt
  printf '\n'
} > /work/summary.txt
`})
			}

			workDir := ctr.Directory("/work")

			commitFromWorktree, err := workDir.File("commit.txt").Contents(ctx)
			require.NoError(t, err)
			commitFromWorktree = strings.TrimSpace(commitFromWorktree)
			require.Equal(t, commitFromRef, commitFromWorktree)

			randomContents, err := workDir.File("random.txt").Contents(ctx)
			require.NoError(t, err)

			readmeContents, err := workDir.File("readme.txt").Contents(ctx)
			require.NoError(t, err)

			out := gitRunOutput{
				commit: commitFromWorktree,
				random: strings.TrimSpace(randomContents),
				readme: readmeContents,
			}
			if layerExtra {
				layeredContents, err := workDir.File("layered.txt").Contents(ctx)
				require.NoError(t, err)
				summaryContents, err := workDir.File("summary.txt").Contents(ctx)
				require.NoError(t, err)
				out.layered = strings.TrimSpace(layeredContents)
				out.summary = summaryContents
			}
			return out
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		outA := runChain(ctx, t, engineClientA, false)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		outB := runChain(ctx, t, engineClientB, true)
		require.Equal(t, outA.commit, outB.commit, "git ref commit should survive engine restart")
		require.Equal(t, outA.random, outB.random, "git-backed withExec result should survive engine restart")
		require.Equal(t, outA.readme, outB.readme, "mounted git tree contents should survive engine restart")
		require.Equal(t, "layered", outB.layered, "new withExec should still apply on top of the cached git-backed state")
		require.Contains(t, outB.summary, "commit="+outB.commit)
		require.Contains(t, outB.summary, "random="+outB.random)
	})

	t.Run("engine-dev container build survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-engine-dev-build-state-" + identity.NewID()
		repoDir := c.Host().Directory("/app")
		writeRandomScript := "set -eu\nhead -c 32 /dev/urandom | sha256sum | cut -d' ' -f1 > /tmp/random\n"
		writeSummaryScript := "set -eu\ntest -x /usr/local/bin/dagger-engine\nprintf '%s|layered\\n' \"$(cat /tmp/random)\" > /tmp/summary\n"

		type startedDevEngine struct {
			service  *dagger.Service
			endpoint string
		}

		startDevEngine := func(ctx context.Context, t *testctx.T, bootID string) *startedDevEngine {
			t.Helper()

			engineCtr := devEngineContainerWithStateKey(
				c,
				stateKey,
				engineWithPersistenceTestGC(ctx, t),
				func(ctr *dagger.Container) *dagger.Container {
					return ctr.WithEnvVariable("_DAGGER_EGRAPH_BOOT_ID", bootID)
				},
			)
			service := devEngineContainerAsService(engineCtr)

			endpoint, err := service.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
			require.NoError(t, err)

			return &startedDevEngine{
				service:  service,
				endpoint: endpoint,
			}
		}

		stopDevEngine := func(ctx context.Context, t *testctx.T, engine *startedDevEngine) {
			t.Helper()
			if engine == nil {
				return
			}
			if engine.service != nil {
				_, err := engine.service.Stop(ctx)
				require.NoError(t, err)
			}
		}

		runCLI := func(ctx context.Context, t *testctx.T, engine *startedDevEngine, sourceMutation string, args ...string) (string, error) {
			t.Helper()

			daggerCli := daggerCliFile(t, c)
			execArgs := append([]string{"/bin/dagger"}, args...)
			ctr := c.Container().From(alpineImage).
				WithServiceBinding("dev-engine", engine.service).
				WithMountedFile("/bin/dagger", daggerCli).
				WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
				WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", engine.endpoint).
				WithDirectory("/app", repoDir).
				WithWorkdir("/app").
				WithEnvVariable("CACHE_BUST", rand.Text())

			if sourceMutation != "" {
				ctr = ctr.WithEnvVariable("SOURCE_MUTATION", sourceMutation).WithExec([]string{
					"sh",
					"-ec",
					"printf '%s' \"$SOURCE_MUTATION\" >> /app/dagql/cache.go",
				})
			}

			ctr = ctr.WithExec(execArgs)

			stdout, err := ctr.Stdout(ctx)
			return strings.TrimSpace(stdout), err
		}

		runEngineDevRandom := func(ctx context.Context, t *testctx.T, engine *startedDevEngine, sourceMutation string) (string, error) {
			t.Helper()
			return runCLI(
				ctx,
				t,
				engine,
				sourceMutation,
				"call", "engine-dev", "container",
				"with-exec", "--args", "true",
				"with-new-file", "--path", "/tmp/write-random.sh", "--contents", writeRandomScript,
				"with-exec", "--args", "sh,/tmp/write-random.sh",
				"file", "--path", "/tmp/random",
				"contents",
			)
		}

		engineABootID := "phase7-engine-dev-build-engine-a"
		engineBBootID := "phase7-engine-dev-build-engine-b"
		engineCBootID := "phase7-engine-dev-build-engine-c"

		engineA := startDevEngine(ctx, t, engineABootID)
		t.Cleanup(func() { stopDevEngine(ctx, t, engineA) })

		randomA, err := runEngineDevRandom(ctx, t, engineA, "")
		require.NoError(t, err)
		require.NotEmpty(t, randomA)

		randomASecondSession, err := runEngineDevRandom(ctx, t, engineA, "")
		require.NoError(t, err)
		require.Equal(t, randomA, randomASecondSession, "engine-dev container build result should survive a new session on the same engine before restart")

		stopDevEngine(ctx, t, engineA)
		engineA = nil

		engineB := startDevEngine(ctx, t, engineBBootID)
		t.Cleanup(func() { stopDevEngine(ctx, t, engineB) })

		randomB, err := runEngineDevRandom(ctx, t, engineB, "")
		require.NoError(t, err)
		require.Equal(t, randomA, randomB, "engine-dev container build result should survive engine restart")

		layered, err := runCLI(
			ctx,
			t,
			engineB,
			"",
			"call", "engine-dev", "container",
			"with-exec", "--args", "true",
			"with-new-file", "--path", "/tmp/write-random.sh", "--contents", writeRandomScript,
			"with-exec", "--args", "sh,/tmp/write-random.sh",
			"with-new-file", "--path", "/tmp/write-summary.sh", "--contents", writeSummaryScript,
			"with-exec", "--args", "sh,/tmp/write-summary.sh",
			"file", "--path", "/tmp/summary",
			"contents",
		)
		require.NoError(t, err)
		require.Equal(t, randomB+"|layered", layered, "new withExec should still apply on top of the cached engine-dev container build")

		cacheGoMutation := "\n// TestDiskPersistenceAcrossRestart mutation\n"
		randomBChanged, err := runEngineDevRandom(ctx, t, engineB, cacheGoMutation)
		require.NoError(t, err)
		require.NotEqual(t, randomB, randomBChanged, "engine-dev container build result should miss after the repo source changes")

		stopDevEngine(ctx, t, engineB)
		engineB = nil

		engineC := startDevEngine(ctx, t, engineCBootID)
		t.Cleanup(func() { stopDevEngine(ctx, t, engineC) })

		randomCChanged, err := runEngineDevRandom(ctx, t, engineC, cacheGoMutation)
		require.NoError(t, err)
		require.Equal(t, randomBChanged, randomCChanged, "engine-dev container build result should survive engine restart with the repo source change in place")

		randomCRestored, err := runEngineDevRandom(ctx, t, engineC, "")
		require.NoError(t, err)
		require.Equal(t, randomB, randomCRestored, "engine-dev container build result should survive engine restart without the repo source change")
	})

	t.Run("cache volume survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-cache-volume-state-" + identity.NewID()
		cacheKey := "phase7-cache-volume-data-" + identity.NewID()
		cacheValue := identity.NewID()

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		cacheA := engineClientA.CacheVolume(cacheKey)
		outA, err := engineClientA.
			Container().
			From(alpineImage).
			WithEnvVariable("CACHE_VALUE", cacheValue).
			WithMountedCache("/mnt/cache", cacheA).
			WithExec([]string{"sh", "-ec", "echo \"$CACHE_VALUE\" >> /mnt/cache/sub-file; cat /mnt/cache/sub-file"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, cacheValue+"\n", outA)

		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		cacheB := engineClientB.CacheVolume(cacheKey)
		outB, err := engineClientB.
			Container().
			From(alpineImage).
			WithMountedCache("/mnt/cache", cacheB).
			WithExec([]string{"sh", "-ec", "cat /mnt/cache/sub-file"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, cacheValue+"\n", outB)
	})
}
