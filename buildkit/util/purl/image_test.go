package purl

import (
	"net/url"
	"testing"

	"github.com/containerd/containerd/platforms"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	packageurl "github.com/package-url/packageurl-go"
	"github.com/stretchr/testify/require"
)

func TestRefToPURL(t *testing.T) {
	testDgst := digest.FromBytes([]byte("test")).String()
	p := platforms.DefaultSpec()
	testPlatform := &p

	expPlatform := url.QueryEscape(platforms.Format(platforms.Normalize(p)))

	tcases := []struct {
		ref      string
		platform *ocispecs.Platform
		expected string
		err      bool
	}{
		{
			ref:      "alpine",
			expected: "pkg:docker/alpine@latest",
		},
		{
			ref:      "library/alpine:3.15",
			expected: "pkg:docker/alpine@3.15",
		},
		{
			ref:      "docker.io/library/alpine:latest",
			expected: "pkg:docker/alpine@latest",
		},
		{
			ref:      "docker.io/library/alpine:latest@" + testDgst,
			expected: "pkg:docker/alpine@latest?digest=" + testDgst,
		},
		{
			ref:      "docker.io/library/alpine@" + testDgst,
			expected: "pkg:docker/alpine?digest=" + testDgst,
		},
		{
			ref:      "user/test:v2",
			expected: "pkg:docker/user/test@v2",
		},
		{
			ref:      "ghcr.io/foo/bar:v2",
			expected: "pkg:docker/ghcr.io/foo/bar@v2",
		},
		{
			ref:      "ghcr.io/foo/bar",
			expected: "pkg:docker/ghcr.io/foo/bar@latest",
		},
		{
			ref:      "busybox",
			platform: testPlatform,
			expected: "pkg:docker/busybox@latest?platform=" + expPlatform,
		},
		{
			ref:      "busybox@" + testDgst,
			platform: testPlatform,
			expected: "pkg:docker/busybox?digest=" + testDgst + "&platform=" + expPlatform,
		},
		{
			ref: "inv:al:id",
			err: true,
		},
	}

	for _, tc := range tcases {
		tc := tc
		t.Run(tc.ref, func(t *testing.T) {
			purl, err := RefToPURL(packageurl.TypeDocker, tc.ref, tc.platform)
			if tc.err {
				require.Error(t, err)
				return
			}
			if err != nil {
				require.NoError(t, err)
			}
			require.Equal(t, tc.expected, purl)
		})
	}
}

func TestPURLToRef(t *testing.T) {
	testDgst := digest.FromBytes([]byte("test")).String()
	p := platforms.Normalize(platforms.DefaultSpec())
	p.OSVersion = "" // OSVersion is not supported in PURL
	testPlatform := &p

	encPlatform := url.QueryEscape(platforms.Format(platforms.Normalize(p)))

	tcases := []struct {
		purl     string
		err      bool
		expected string
		platform *ocispecs.Platform
	}{
		{
			purl:     "pkg:docker/alpine@latest",
			expected: "docker.io/library/alpine:latest",
		},
		{
			purl:     "pkg:docker/alpine",
			expected: "docker.io/library/alpine:latest",
		},
		{
			purl:     "pkg:docker/alpine?digest=" + testDgst,
			expected: "docker.io/library/alpine@" + testDgst,
		},
		{
			purl:     "pkg:docker/library/alpine@3.15?digest=" + testDgst,
			expected: "docker.io/library/alpine:3.15@" + testDgst,
		},
		{
			purl:     "pkg:docker/ghcr.io/foo/bar@v2",
			expected: "ghcr.io/foo/bar:v2",
		},
		{
			purl:     "pkg:docker/ghcr.io/foo/bar@v2",
			expected: "ghcr.io/foo/bar:v2",
		},
		{
			purl:     "pkg:docker/busybox@latest?platform=" + encPlatform,
			expected: "docker.io/library/busybox:latest",
			platform: testPlatform,
		},
		{
			purl: "pkg:busybox@latest",
			err:  true,
		},
	}

	for _, tc := range tcases {
		tc := tc
		t.Run(tc.purl, func(t *testing.T) {
			ref, platform, err := PURLToRef(tc.purl)
			if tc.err {
				require.Error(t, err)
				return
			}
			if err != nil {
				require.NoError(t, err)
			}
			require.Equal(t, tc.expected, ref)
			if platform == nil {
				require.Nil(t, tc.platform)
			} else {
				require.Equal(t, *tc.platform, *platform)
			}
		})
	}
}
