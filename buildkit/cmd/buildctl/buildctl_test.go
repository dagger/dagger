package main

import (
	"encoding/json"
	"os"
	"path"
	"testing"

	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/moby/buildkit/util/testutil/workers"
	"github.com/stretchr/testify/require"
)

func init() {
	workers.InitOCIWorker()
	workers.InitContainerdWorker()
}

func TestCLIIntegration(t *testing.T) {
	integration.Run(t, integration.TestFuncs(
		testDiskUsage,
		testBuildWithLocalFiles,
		testBuildLocalExporter,
		testBuildContainerdExporter,
		testBuildMetadataFile,
		testPrune,
		testUsage,
	),
		integration.WithMirroredImages(integration.OfficialImages("busybox:latest")),
	)
}

func testUsage(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	require.NoError(t, sb.Cmd().Run())

	require.NoError(t, sb.Cmd("--help").Run())
}

func TestWriteMetadataFile(t *testing.T) {
	tmpdir := t.TempDir()

	cases := []struct {
		name             string
		exporterResponse map[string]string
		excpected        map[string]interface{}
	}{
		{
			name: "common",
			exporterResponse: map[string]string{
				"containerimage.config.digest": "sha256:2937f66a9722f7f4a2df583de2f8cb97fc9196059a410e7f00072fc918930e66",
				"containerimage.descriptor":    "eyJtZWRpYVR5cGUiOiJhcHBsaWNhdGlvbi92bmQub2NpLmltYWdlLm1hbmlmZXN0LnYxK2pzb24iLCJkaWdlc3QiOiJzaGEyNTY6MTlmZmVhYjZmOGJjOTI5M2FjMmMzZmRmOTRlYmUyODM5NjI1NGM5OTNhZWEwYjVhNTQyY2ZiMDJlMDg4M2ZhMyIsInNpemUiOjUwNiwiYW5ub3RhdGlvbnMiOnsib3JnLm9wZW5jb250YWluZXJzLmltYWdlLmNyZWF0ZWQiOiIyMDIyLTAyLTA4VDE5OjIxOjAzWiJ9fQ==", // {"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:19ffeab6f8bc9293ac2c3fdf94ebe28396254c993aea0b5a542cfb02e0883fa3","size":506,"annotations":{"org.opencontainers.image.created":"2022-02-08T19:21:03Z"}}
				"containerimage.digest":        "sha256:19ffeab6f8bc9293ac2c3fdf94ebe28396254c993aea0b5a542cfb02e0883fa3",
			},
			excpected: map[string]interface{}{
				"containerimage.config.digest": "sha256:2937f66a9722f7f4a2df583de2f8cb97fc9196059a410e7f00072fc918930e66",
				"containerimage.descriptor": map[string]interface{}{
					"annotations": map[string]interface{}{
						"org.opencontainers.image.created": "2022-02-08T19:21:03Z",
					},
					"digest":    "sha256:19ffeab6f8bc9293ac2c3fdf94ebe28396254c993aea0b5a542cfb02e0883fa3",
					"mediaType": "application/vnd.oci.image.manifest.v1+json",
					"size":      float64(506),
				},
				"containerimage.digest": "sha256:19ffeab6f8bc9293ac2c3fdf94ebe28396254c993aea0b5a542cfb02e0883fa3",
			},
		},
		{
			name: "b64json",
			exporterResponse: map[string]string{
				"key":                   "MTI=", // 12
				"containerimage.digest": "sha256:19ffeab6f8bc9293ac2c3fdf94ebe28396254c993aea0b5a542cfb02e0883fa3",
			},
			excpected: map[string]interface{}{
				"key":                   "MTI=",
				"containerimage.digest": "sha256:19ffeab6f8bc9293ac2c3fdf94ebe28396254c993aea0b5a542cfb02e0883fa3",
			},
		},
		{
			name: "emptyjson",
			exporterResponse: map[string]string{
				"key":                   "e30=", // {}
				"containerimage.digest": "sha256:19ffeab6f8bc9293ac2c3fdf94ebe28396254c993aea0b5a542cfb02e0883fa3",
			},
			excpected: map[string]interface{}{
				"key":                   "e30=",
				"containerimage.digest": "sha256:19ffeab6f8bc9293ac2c3fdf94ebe28396254c993aea0b5a542cfb02e0883fa3",
			},
		},
		{
			name: "invalidjson",
			exporterResponse: map[string]string{
				"key":                   "W10=", // []
				"containerimage.digest": "sha256:19ffeab6f8bc9293ac2c3fdf94ebe28396254c993aea0b5a542cfb02e0883fa3",
			},
			excpected: map[string]interface{}{
				"key":                   "W10=",
				"containerimage.digest": "sha256:19ffeab6f8bc9293ac2c3fdf94ebe28396254c993aea0b5a542cfb02e0883fa3",
			},
		},
		{
			name: "nullobject",
			exporterResponse: map[string]string{
				"key":                   "eyJmb28iOm51bGwsImJhciI6ImJheiJ9", // {"foo":null,"bar":"baz"}
				"containerimage.digest": "sha256:19ffeab6f8bc9293ac2c3fdf94ebe28396254c993aea0b5a542cfb02e0883fa3",
			},
			excpected: map[string]interface{}{
				"key": map[string]interface{}{
					"foo": nil,
					"bar": "baz",
				},
				"containerimage.digest": "sha256:19ffeab6f8bc9293ac2c3fdf94ebe28396254c993aea0b5a542cfb02e0883fa3",
			},
		},
	}

	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			fname := path.Join(tmpdir, "metadata_"+tt.name)
			require.NoError(t, writeMetadataFile(fname, tt.exporterResponse))
			current, err := os.ReadFile(fname)
			require.NoError(t, err)
			var raw map[string]interface{}
			require.NoError(t, json.Unmarshal(current, &raw))
			require.Equal(t, tt.excpected, raw)
		})
	}
}
