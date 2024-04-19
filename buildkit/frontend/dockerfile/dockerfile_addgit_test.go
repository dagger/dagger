package dockerfile

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"text/template"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

var addGitTests = integration.TestFuncs(
	testAddGit,
)

func init() {
	allTests = append(allTests, addGitTests...)
}

func testAddGit(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	gitDir, err := os.MkdirTemp("", "buildkit")
	require.NoError(t, err)
	defer os.RemoveAll(gitDir)
	gitCommands := []string{
		"git init",
		"git config --local user.email test",
		"git config --local user.name test",
	}
	makeCommit := func(tag string) []string {
		return []string{
			"echo foo of " + tag + " >foo",
			"git add foo",
			"git commit -m " + tag,
			"git tag " + tag,
		}
	}
	gitCommands = append(gitCommands, makeCommit("v0.0.1")...)
	gitCommands = append(gitCommands, makeCommit("v0.0.2")...)
	gitCommands = append(gitCommands, makeCommit("v0.0.3")...)
	gitCommands = append(gitCommands, "git update-server-info")
	err = runShell(gitDir, gitCommands...)
	require.NoError(t, err)

	server := httptest.NewServer(http.FileServer(http.Dir(filepath.Join(gitDir))))
	defer server.Close()
	serverURL := server.URL
	t.Logf("serverURL=%q", serverURL)

	dockerfile, err := applyTemplate(`
FROM alpine

# Basic case
ADD {{.ServerURL}}/.git#v0.0.1 /x
RUN cd /x && \
  [ "$(cat foo)" = "foo of v0.0.1" ]

# Complicated case
ARG REPO="{{.ServerURL}}/.git"
ARG TAG="v0.0.2"
ADD --keep-git-dir=true --chown=4242:8484 ${REPO}#${TAG} /buildkit-chowned
RUN apk add git
USER 4242
RUN cd /buildkit-chowned && \
  [ "$(cat foo)" = "foo of v0.0.2" ] && \
  [ "$(stat -c %u foo)" = "4242" ] && \
  [ "$(stat -c %g foo)" = "8484" ] && \
  [ -z "$(git status -s)" ]
`, map[string]string{
		"ServerURL": serverURL,
	})
	require.NoError(t, err)
	t.Logf("dockerfile=%s", dockerfile)

	dir := integration.Tmpdir(t,
		fstest.CreateFile("Dockerfile", []byte(dockerfile), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)
}

func applyTemplate(tmpl string, x interface{}) (string, error) {
	var buf bytes.Buffer
	parsed, err := template.New("").Parse(tmpl)
	if err != nil {
		return "", err
	}
	if err := parsed.Execute(&buf, x); err != nil {
		return "", err
	}
	return buf.String(), nil
}
