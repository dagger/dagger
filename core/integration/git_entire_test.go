package core

// These tests cover entire:// remotes end to end: a nested dev engine runs its
// bundled git-remote-entire against a fake Entire cluster that serves real
// smart HTTP over TLS, cluster discovery, and bearer-token auth.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/internal/testutil"
)

type GitEntireSuite struct{}

func TestGitEntire(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(GitEntireSuite{})
}

const (
	// The cluster and the core that issues its tokens must share a
	// registrable domain: git-remote-entire refuses any other issuer.
	fakeEntireClusterHost = "cluster.entire.test"
	fakeEntireCoreURL     = "https://core.entire.test"

	fakeEntireRepo          = "et/acme/api"
	fakeEntireSubmoduleRepo = "et/acme/with-submodule"
	fakeEntireReadme        = "hello from entire\n"
)

// entireE2EArtifact is what the test observed, written to the test's artifact
// directory (go test -artifacts) so a run can be inspected and compared.
type entireE2EArtifact struct {
	Repository           string   `json:"repository"`
	MainSHA              string   `json:"mainSHA"`
	Readme               string   `json:"readme"`
	WrongTokenError      string   `json:"wrongTokenError"`
	ConflictingAuthError string   `json:"conflictingAuthError"`
	PushDisposition      string   `json:"pushDisposition"`
	PushedSHA            string   `json:"pushedSHA"`
	RefetchedPushedSHA   string   `json:"refetchedPushedSHA"`
	HelperModuleCommit   string   `json:"helperModuleCommit"`
	HelperHTMLRepoURL    string   `json:"helperHTMLRepoURL"`
	HelperReadme         string   `json:"helperReadme"`
	NoCredentialError    string   `json:"noCredentialError"`
	SubmoduleError       string   `json:"submoduleError"`
	ClusterAccessLog     []string `json:"clusterAccessLog"`
}

func (GitEntireSuite) TestEntireRemote(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	token := fakeEntireToken(fakeEntireCoreURL)
	cluster, repos := fakeEntireCluster(c, token)

	certs := newGeneratedCerts(c, "entire-test-ca")
	serverCert, serverKey := certs.newServerCerts(fakeEntireClusterHost)
	cluster = cluster.
		WithMountedFile("/etc/ssl/certs/entire.crt", serverCert).
		WithMountedFile("/etc/ssl/private/entire.key", serverKey)
	clusterSvc := cluster.AsService(core.ContainerAsServiceOpts{UseEntrypoint: true})

	devEngine := devEngineContainer(c, func(ctr *core.Container) *core.Container {
		return ctr.
			WithMountedFile("/usr/local/share/ca-certificates/entire-test-ca.crt", certs.caRootCert).
			WithServiceBinding(fakeEntireClusterHost, clusterSvc)
	})
	engineSvc, err := core.NewQuery(c).Host().Tunnel(devEngineContainerAsService(devEngine)).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = engineSvc.Stop(ctx) })
	endpoint, err := engineSvc.Endpoint(ctx, core.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)
	connectEngine := func() *dagger.Client {
		client, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
		require.NoError(t, err)
		t.Cleanup(func() { client.Close() })
		return client
	}

	repoURL := "entire://" + fakeEntireClusterHost + "/" + fakeEntireRepo
	artifact := entireE2EArtifact{Repository: repoURL}
	mainSHA, err := repos.File("main.sha").Contents(ctx)
	require.NoError(t, err)
	artifact.MainSHA = strings.TrimSpace(mainSHA)

	c2 := connectEngine()
	q := core.NewQuery(c2)
	tokenSecret := q.SetSecret("entire-token", token)

	// Reading with an explicit token.
	repo := q.Git(repoURL, core.GitOpts{HTTPAuthToken: tokenSecret})
	main := repo.Branch("main")
	sha, err := main.CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, artifact.MainSHA, sha)
	artifact.Readme, err = main.Tree().File("README").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, fakeEntireReadme, artifact.Readme)

	// A token the cluster rejects is an authentication failure, and its
	// value never reaches the error.
	_, err = q.Git(repoURL, core.GitOpts{HTTPAuthToken: q.SetSecret("entire-wrong", token+"x")}).Branch("main").CommitSHA(ctx)
	require.ErrorContains(t, err, "git authentication failed")
	require.NotContains(t, err.Error(), token)
	artifact.WrongTokenError = err.Error()

	// Entire has a single bearer token: no username, no prebuilt header.
	_, err = q.Git(repoURL, core.GitOpts{HTTPAuthToken: tokenSecret, HTTPAuthUsername: "someone"}).Branch("main").CommitSHA(ctx)
	require.ErrorContains(t, err, "httpAuthToken alone")
	artifact.ConflictingAuthError = err.Error()

	// Pushing with the same token.
	pushed := main.AsWorkspace().WithNewFile("pushed", "pushed").With(func(ws *core.Workspace) *core.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "push through entire", workspaceCommitDate)
	}).Git().Head()
	artifact.PushedSHA, err = pushed.CommitSHA(ctx)
	require.NoError(t, err)
	receipt, err := pushGitRef(ctx, c2, pushed, repo, "pushed", nil)
	require.NoError(t, err)
	require.Equal(t, "CREATED", receipt.Disposition)
	artifact.PushDisposition = receipt.Disposition
	// A new session lists the remote again rather than reusing this one's.
	q3 := core.NewQuery(connectEngine())
	artifact.RefetchedPushedSHA, err = q3.Git(repoURL, core.GitOpts{HTTPAuthToken: q3.SetSecret("entire-token", token)}).
		Branch("pushed").CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, artifact.PushedSHA, artifact.RefetchedPushedSHA)

	// Without an explicit token, the client's git credential helper supplies
	// it, for both module sources and plain repositories.
	workDir := t.TempDir()
	helperQuery := fmt.Sprintf(`{
  moduleSource(refString: %q) { commit htmlRepoURL }
  git(url: %q) { branch(name: "main") { tree { file(path: "README") { contents } } } }
}`, repoURL+"/mod@main", repoURL)
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "query.graphql"), []byte(helperQuery), 0o600))
	helperConfig := filepath.Join(workDir, "helper.gitconfig")
	require.NoError(t, os.WriteFile(helperConfig, []byte(makeGitCredentials("entire://*.entire.test", "x-access-token", token)), 0o600))
	emptyConfig := filepath.Join(workDir, "empty.gitconfig")
	require.NoError(t, os.WriteFile(emptyConfig, nil, 0o600))
	runHostQuery := func(gitConfig string) ([]byte, error) {
		cmd := hostDaggerCommandRaw(ctx, t, workDir, "query", "--no-load-module", "--doc", "query.graphql")
		env := make([]string, 0, len(cmd.Env))
		for _, e := range cmd.Env {
			if !strings.Contains(strings.ToLower(strings.SplitN(e, "=", 2)[0]), "git") {
				env = append(env, e)
			}
		}
		cmd.Env = append(env,
			"_EXPERIMENTAL_DAGGER_RUNNER_HOST="+endpoint,
			"GIT_CONFIG_GLOBAL="+gitConfig,
			"GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_TERMINAL_PROMPT=0",
		)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			err = fmt.Errorf("%s: %w", stderr.String(), err)
		}
		return out, err
	}
	out, err := runHostQuery(helperConfig)
	require.NoError(t, err)
	var helperResult struct {
		ModuleSource struct {
			Commit      string
			HTMLRepoURL string `json:"htmlRepoURL"`
		}
		Git struct {
			Branch struct {
				Tree struct {
					File struct{ Contents string }
				}
			}
		}
	}
	require.NoError(t, json.Unmarshal(out, &helperResult), string(out))
	artifact.HelperModuleCommit = helperResult.ModuleSource.Commit
	artifact.HelperHTMLRepoURL = helperResult.ModuleSource.HTMLRepoURL
	artifact.HelperReadme = helperResult.Git.Branch.Tree.File.Contents
	require.Equal(t, artifact.MainSHA, artifact.HelperModuleCommit)
	require.Equal(t, "https://entire.io/"+fakeEntireRepo, artifact.HelperHTMLRepoURL)
	require.Equal(t, fakeEntireReadme, artifact.HelperReadme)

	// Without a helper there is no credential: the engine holds no Entire
	// login of its own to fall back on.
	_, err = runHostQuery(emptyConfig)
	require.ErrorContains(t, err, "git authentication failed")
	artifact.NoCredentialError = ansiEscapes.ReplaceAllString(err.Error(), "")

	// entire:// submodules are refused: the helper would hand them the
	// superproject's token. The submodule names a repository this token
	// reads, so only the protocol policy can make the checkout fail.
	_, err = q.Git("entire://"+fakeEntireClusterHost+"/"+fakeEntireSubmoduleRepo, core.GitOpts{HTTPAuthToken: tokenSecret}).
		Branch("main").Tree().Entries(ctx)
	require.ErrorContains(t, err, "failed to update submodules")
	artifact.SubmoduleError = err.Error()

	// Every git request carried the bearer, which the cluster checked, and
	// fetches spoke protocol v2.
	accessLog, err := core.NewQuery(c).Container().From(alpineImage).
		WithServiceBinding("cluster", clusterSvc).
		WithEnvVariable("CACHEBUST", rand.Text()).
		WithExec([]string{"wget", "-qO-", "http://cluster/_log"}).
		Stdout(ctx)
	require.NoError(t, err)
	for line := range strings.SplitSeq(strings.TrimSpace(accessLog), "\n") {
		artifact.ClusterAccessLog = append(artifact.ClusterAccessLog, line)
	}
	// Discovery is public: the helper never sends the token there.
	require.Contains(t, accessLog, "GET /.well-known/entire-cluster.json git_protocol=- auth=none status=200")
	require.Contains(t, accessLog, "POST /"+fakeEntireRepo+"/git-upload-pack git_protocol=version=2 auth=ok status=200")
	require.Regexp(t, regexp.MustCompile(`(?m)^POST /`+regexp.QuoteMeta(fakeEntireRepo)+`/git-receive-pack git_protocol=\S+ auth=ok status=200$`), accessLog)
	require.Contains(t, accessLog, "auth=denied status=401")
	require.NotContains(t, accessLog, token)

	data, err := json.MarshalIndent(artifact, "", "  ")
	require.NoError(t, err)
	artifactPath := filepath.Join(t.ArtifactDir(), "entire-e2e.json")
	require.NoError(t, os.WriteFile(artifactPath, data, 0o644))
	t.Logf("wrote %s:\n%s", artifactPath, data)
}

var ansiEscapes = regexp.MustCompile("\x1b\\[[0-9;]*m")

// fakeEntireToken returns a token shaped like an Entire login JWT for the core
// at coreURL. git-remote-entire reads its audience to check the cluster trusts
// that core, but never verifies the signature: the cluster does.
func fakeEntireToken(coreURL string) string {
	enc := base64.RawURLEncoding.EncodeToString
	header := enc([]byte(`{"alg":"EdDSA","typ":"JWT"}`))
	claims := enc(fmt.Appendf(nil, `{"aud":%q,"iss":%q,"sub":"dagger-test","exp":4102444800}`, coreURL, coreURL))
	return header + "." + claims + "." + enc([]byte("unsigned"))
}

// fakeEntireCluster returns an nginx container serving the cluster's
// discovery document and its repositories over smart HTTP, admitting only
// requests that carry token as a bearer. Plain HTTP on port 80 serves the
// access log at /_log. It expects its TLS certificate and key to be mounted at
// /etc/ssl/certs/entire.crt and /etc/ssl/private/entire.key. The returned
// directory holds main.sha, the main branch commit of fakeEntireRepo.
func fakeEntireCluster(c *dagger.Client, token string) (*core.Container, *core.Directory) {
	repos := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithEnvVariable("GIT_AUTHOR_NAME", "Test User").
		WithEnvVariable("GIT_AUTHOR_EMAIL", "test@localhost").
		WithEnvVariable("GIT_COMMITTER_NAME", "Test User").
		WithEnvVariable("GIT_COMMITTER_EMAIL", "test@localhost").
		WithNewFile("/src/README", fakeEntireReadme).
		WithNewFile("/src/mod/dagger.json", `{"name": "probe"}`).
		WithExec([]string{"sh", "-euxc", fmt.Sprintf(`
mkdir -p /out
git -C /src init -q -b main
git -C /src add -A
git -C /src commit -qm init
git -C /src rev-parse HEAD > /out/main.sha
git clone -q --bare /src /out/srv/%[1]s
git -C /out/srv/%[1]s config http.receivepack true

git init -q -b main /sub
git -C /sub -c protocol.file.allow=always submodule add -q /out/srv/%[1]s api
git -C /sub config -f .gitmodules submodule.api.url entire://%[2]s/%[1]s
git -C /sub commit -qam "entire submodule"
git clone -q --bare /sub /out/srv/%[3]s
`, fakeEntireRepo, fakeEntireClusterHost, fakeEntireSubmoduleRepo)}).
		Directory("/out")

	conf := fmt.Sprintf(`
# The token is a map key longer than the default bucket holds.
map_hash_bucket_size 512;
map $http_authorization $entire_auth {
	default "denied";
	"" "none";
	"Bearer %[1]s" "ok";
}
map $http_git_protocol $entire_git_protocol {
	default $http_git_protocol;
	"" "-";
}
log_format entire '$request_method $uri git_protocol=$entire_git_protocol auth=$entire_auth status=$status';

server {
	listen 443 ssl;
	server_name %[2]s;
	ssl_certificate /etc/ssl/certs/entire.crt;
	ssl_certificate_key /etc/ssl/private/entire.key;
	access_log /var/log/entire/access.log entire;

	location = /.well-known/entire-cluster.json {
		default_type application/json;
		return 200 '{"core_urls":["%[3]s"]}';
	}

	location ~ ^/(?<git_path>.*)$ {
		if ($entire_auth != "ok") {
			return 401 "unauthorized";
		}
		include               /etc/nginx/fastcgi_params;
		fastcgi_param         GIT_HTTP_EXPORT_ALL "";
		fastcgi_param         GIT_PROJECT_ROOT /srv;
		fastcgi_param         GIT_PROTOCOL $http_git_protocol;
		fastcgi_param         PATH_INFO /$git_path;
		fastcgi_param         SCRIPT_FILENAME /usr/lib/git-core/git-http-backend;
		fastcgi_pass          unix:/var/run/fcgiwrap.socket;
	}
}

server {
	listen 80;
	access_log off;
	location = /_log {
		default_type text/plain;
		alias /var/log/entire/access.log;
	}
}
`, token, fakeEntireClusterHost, fakeEntireCoreURL)

	ctr := core.NewQuery(c).Container().
		From("nginx").
		WithExec([]string{"sh", "-euxc", `
apt-get update
apt-get install -y --no-install-recommends git fcgiwrap spawn-fcgi
rm -rf /var/lib/apt/lists/*
git config --system safe.directory '*'
mkdir -p /var/log/entire
chmod 777 /var/log/entire
`}).
		WithNewFile("/etc/nginx/conf.d/default.conf", conf).
		WithDirectory("/srv", repos.Directory("srv")).
		WithExposedPort(443).
		WithExposedPort(80).
		WithEntrypoint([]string{"sh", "-c",
			"spawn-fcgi -s /var/run/fcgiwrap.socket -M 766 /usr/sbin/fcgiwrap && exec nginx -g 'daemon off;'"})
	return ctr, repos
}
