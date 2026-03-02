package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

type HTTPSuite struct{}

func TestHTTP(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(HTTPSuite{})
}

func (HTTPSuite) TestHTTP(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// do two in a row to ensure each gets downloaded correctly
	url := "https://raw.githubusercontent.com/dagger/dagger/main/LICENSE"
	contents, err := c.HTTP(url).Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, contents, "copyright")

	url = "https://raw.githubusercontent.com/dagger/dagger/main/README.md"
	contents, err = c.HTTP(url).Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, contents, "Dagger")
}

func (HTTPSuite) TestHTTPName(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	url := "https://raw.githubusercontent.com/dagger/dagger/main/README.md"

	filename, err := c.HTTP(url).Name(ctx)
	require.NoError(t, err)
	require.Equal(t, "README.md", filename)

	filename, err = c.HTTP(url, dagger.HTTPOpts{Name: "FooBar.md"}).Name(ctx)
	require.NoError(t, err)
	require.Equal(t, "FooBar.md", filename)

	filename, err = c.HTTP(url, dagger.HTTPOpts{Name: "FooBar.md.x"}).Name(ctx)
	require.NoError(t, err)
	require.Equal(t, "FooBar.md.x", filename)
}

func (HTTPSuite) TestHTTPPermissions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	url := "https://raw.githubusercontent.com/dagger/dagger/main/README.md"

	f := c.HTTP(url, dagger.HTTPOpts{Permissions: 0765})
	stat, err := c.Container().From(alpineImage).
		WithFile("/target", f).
		WithExec([]string{"stat", "-c", "%a", "/target"}).
		Stdout(ctx)
	require.NoError(t, err)
	stat = strings.TrimSpace(stat)
	require.Equal(t, "765", stat)

	f2 := c.HTTP(url, dagger.HTTPOpts{Permissions: 0764})
	stat, err = c.Container().From(alpineImage).
		WithFile("/target", f2).
		WithExec([]string{"stat", "-c", "%a", "/target"}).
		Stdout(ctx)
	require.NoError(t, err)
	stat = strings.TrimSpace(stat)
	require.Equal(t, "764", stat)
}

func (HTTPSuite) TestHTTPChecksum(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	content := "Hello, checksum world!"
	svc, url := httpService(ctx, t, c, content)
	expected := digest.FromString(content).String()

	contents, err := c.HTTP(url, dagger.HTTPOpts{
		ExperimentalServiceHost: svc,
		Checksum:                expected,
	}).Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, content, contents)
}

func (HTTPSuite) TestHTTPChecksumMismatch(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	content := "Hello, checksum world!"
	svc, url := httpService(ctx, t, c, content)
	wrong := digest.FromString("something-else").String()

	_, err := c.HTTP(url, dagger.HTTPOpts{
		ExperimentalServiceHost: svc,
		Checksum:                wrong,
	}).Contents(ctx)
	require.ErrorContains(t, err, "http checksum mismatch")
	require.ErrorContains(t, err, "expected "+wrong)
}

func (HTTPSuite) TestHTTPChecksumInvalid(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	_, err := c.HTTP("https://example.com", dagger.HTTPOpts{
		Checksum: "not-a-digest",
	}).Contents(ctx)
	require.ErrorContains(t, err, `invalid checksum "not-a-digest"`)
}

func (HTTPSuite) TestHTTPService(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	svc, url := httpService(ctx, t, c, "Hello, world!")

	contents, err := c.HTTP(url, dagger.HTTPOpts{
		ExperimentalServiceHost: svc,
	}).Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, contents, "Hello, world!")
}

func (HTTPSuite) TestHTTPAuth(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	svc, svcURL := httpServiceAuth(ctx, t, c, "Hello, secret world!", "", c.SetSecret("SECRET", "personalsecret"))
	_, err := c.HTTP(svcURL, dagger.HTTPOpts{
		ExperimentalServiceHost: svc,
	}).Contents(ctx)
	require.ErrorContains(t, err, "401 Unauthorized")

	contents, err := c.HTTP(svcURL, dagger.HTTPOpts{
		ExperimentalServiceHost: svc,
		AuthHeader:              c.SetSecret("AUTH_TOKEN", basicAuthHeader(url.UserPassword("x-access-token", "personalsecret"))),
	}).Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, contents, "Hello, secret world!")
}

func basicAuthHeader(info *url.Userinfo) string {
	username := info.Username()
	password, _ := info.Password()
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

func (HTTPSuite) TestHTTPTimestamp(ctx context.Context, t *testctx.T) {
	// checks that the file timestamp is set to the last-modified header

	c := connect(ctx, t)

	dir := c.Container().
		From(alpineImage).
		WithWorkdir("/src").
		WithNewFile("index.html", "Hello, world!").
		WithExec([]string{"touch", "-m", "--date=@0", "index.html"}).
		Directory(".")
	svc, url := httpServiceDir(ctx, t, c, dir)

	file := c.HTTP(url, dagger.HTTPOpts{ExperimentalServiceHost: svc})
	require.Equal(t, 0, getFileTimestamp(ctx, t, c, file)) // httpService sets mtime to the unix epoch
}

func (HTTPSuite) TestHTTPCachePerSessions(ctx context.Context, t *testctx.T) {
	port := counterService(ctx, t, false)

	c := connect(ctx, t)
	svc := c.Host().Service([]dagger.PortForward{{
		Backend:  port,
		Frontend: port,
	}})
	hostname, err := svc.Hostname(ctx)
	require.NoError(t, err)
	url := fmt.Sprintf("http://%s:%d?add=true", hostname, port)

	f := c.HTTP(url, dagger.HTTPOpts{ExperimentalServiceHost: svc})
	contents, err := f.Contents(ctx)
	baseTimestamp := getFileTimestamp(ctx, t, c, f)
	require.NoError(t, err)
	require.Equal(t, contents, "count: 1")

	// avoid making requests more than once per session
	f = c.HTTP(url, dagger.HTTPOpts{ExperimentalServiceHost: svc})
	contents, err = f.Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, contents, "count: 1")
	require.Equal(t, baseTimestamp, getFileTimestamp(ctx, t, c, f))

	time.Sleep(5 * time.Second) // wait so that the timestamp can get bigger

	// but if we create a new session, then we should be making another request
	c2 := connect(ctx, t)
	svc2 := c2.Host().Service([]dagger.PortForward{{
		Backend:  port,
		Frontend: port,
	}})
	hostname2, err := svc2.Hostname(ctx)
	require.NoError(t, err)
	url2 := fmt.Sprintf("http://%s:%d?add=true", hostname2, port)

	f2 := c2.HTTP(url2, dagger.HTTPOpts{ExperimentalServiceHost: svc2})
	contents, err = f2.Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, contents, "count: 2")

	require.NotEqual(t, baseTimestamp, getFileTimestamp(ctx, t, c2, f2))
}

func (HTTPSuite) TestHTTPUsedInCache(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	port := counterService(ctx, t, false)
	svc := c.Host().Service([]dagger.PortForward{{
		Backend:  port,
		Frontend: port,
	}})
	hostname, err := svc.Hostname(ctx)
	require.NoError(t, err)
	url := fmt.Sprintf("http://%s:%d", hostname, port)

	// request two different urls, but with the same content + timestamp

	out, err := c.Container().
		From(alpineImage).
		WithMountedFile("/index.html", c.HTTP(url+"/test?query=1", dagger.HTTPOpts{ExperimentalServiceHost: svc})).
		WithExec([]string{"sh", "-c", "cat /index.html && head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
	require.Contains(t, out, "count: 0")
	require.NoError(t, err)

	out2, err := c.Container().
		From(alpineImage).
		WithMountedFile("/index.html", c.HTTP(url+"/test?query=2", dagger.HTTPOpts{ExperimentalServiceHost: svc})).
		WithExec([]string{"sh", "-c", "cat /index.html && head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out2, "count: 0")
	require.Equal(t, out, out2)

	out3, err := c.Container().
		From(alpineImage).
		WithMountedFile("/index.html", c.HTTP(url+"/test?add=3", dagger.HTTPOpts{ExperimentalServiceHost: svc})).
		WithExec([]string{"sh", "-c", "cat /index.html && head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out3, "count: 1")
	require.NotEqual(t, out, out3)
}

func (HTTPSuite) TestHTTPETag(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const hostname = "testhttpetag"

	// query in first session
	port := counterService(ctx, t, true)
	svc := c.Host().Service([]dagger.PortForward{{
		Backend:  port,
		Frontend: port,
	}}).WithHostname(hostname)
	url := fmt.Sprintf("http://%s:%d", hostname, port)

	f := c.HTTP(url+"?query=1", dagger.HTTPOpts{ExperimentalServiceHost: svc})
	contents, err := f.Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "count: 0", contents)

	// query in second session, the http client should present If-None-Match using the etag
	c2 := connect(ctx, t)
	svc2 := c2.Host().Service([]dagger.PortForward{{
		Backend:  port,
		Frontend: port,
	}}).WithHostname(hostname)

	f2 := c2.HTTP(url+"?query=1", dagger.HTTPOpts{ExperimentalServiceHost: svc2})
	contents, err = f2.Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "count: 0", contents)

	// check that we did actually hit the cache!
	cacheF := c.HTTP(url+"?cache=1", dagger.HTTPOpts{ExperimentalServiceHost: svc})
	contents, err = cacheF.Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "cache: 1", contents)

	// part2! we bump now
	bumpF := c.HTTP(url+"?add=2", dagger.HTTPOpts{ExperimentalServiceHost: svc})
	contents, err = bumpF.Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "count: 1", contents)

	// query in second session, the http client should present, but there shouldn't be an ETag match
	f2 = c2.HTTP(url+"?query=2", dagger.HTTPOpts{ExperimentalServiceHost: svc2})
	contents, err = f2.Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "count: 1", contents)

	// check that the cache wasn't hit
	cacheF = c.HTTP(url+"?cache=2", dagger.HTTPOpts{ExperimentalServiceHost: svc})
	contents, err = cacheF.Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "cache: 1", contents)
}

func (HTTPSuite) TestHTTPServiceStableDigest(ctx context.Context, t *testctx.T) {
	content := identity.NewID()
	hostname := func(c *dagger.Client) string {
		svc, url := httpService(ctx, t, c, content)

		hn, err := c.Container().
			From(alpineImage).
			WithMountedFile("/index.html", c.HTTP(url, dagger.HTTPOpts{
				ExperimentalServiceHost: svc,
			})).
			WithDefaultArgs([]string{"sleep"}).
			AsService().
			Hostname(ctx)
		require.NoError(t, err)
		return hn
	}

	c1 := connect(ctx, t)
	c2 := connect(ctx, t)
	require.Equal(t, hostname(c1), hostname(c2))
}

func getFileTimestamp(ctx context.Context, t *testctx.T, c *dagger.Client, f *dagger.File) int {
	t.Helper()

	out, err := c.Container().From(alpineImage).
		WithMountedFile("/index.html", f).
		WithExec([]string{"stat", "/index.html", "-c", "%Y"}).
		Stdout(ctx)
	require.NoError(t, err)
	out = strings.TrimSpace(out)

	i, err := strconv.Atoi(out)
	require.NoError(t, err)
	return i
}

func counterService(ctx context.Context, t *testctx.T, serveEtags bool) (port int) {
	t.Helper()

	l, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		l.Close()
	})
	port = l.Addr().(*net.TCPAddr).Port

	counters := make(map[string]int)
	cacheHits := make(map[string]int)
	lastModified := make(map[string]time.Time)
	httpSrv := http.Server{
		BaseContext: func(net.Listener) context.Context { return ctx },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if path == "/" {
				path = "/index.html"
			}

			w.Header().Set("Content-Type", "text/html")

			switch {
			case r.URL.Query().Get("add") != "":
				counters[path]++
				lastModified[path] = time.Now()
			case r.URL.Query().Get("query") != "":
				if vals := r.Header.Values("If-None-Match"); serveEtags && len(vals) > 0 {
					for _, val := range vals {
						n, err := strconv.Atoi(val)
						if err != nil {
							continue
						}
						if counters[path] != n {
							continue
						}

						cacheHits[path]++
						w.WriteHeader(http.StatusNotModified)
						return
					}
				}
			case r.URL.Query().Get("cache") != "":
				w.WriteHeader(http.StatusOK)
				fmt.Fprintf(w, "cache: %d", cacheHits[path])
				return

			default:
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			w.Header().Set("Last-Modified", lastModified[path].Format(http.TimeFormat))
			if serveEtags {
				w.Header().Set("ETag", fmt.Sprintf("%d", counters[path]))
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "count: %d", counters[path])
		}),
	}
	go httpSrv.Serve(l)

	return port
}
