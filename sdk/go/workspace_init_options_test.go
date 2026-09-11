package dagger

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type initOptionsCaptureConn struct{ query string }

func (*initOptionsCaptureConn) Host() string { return "unused" }
func (*initOptionsCaptureConn) Close() error { return nil }
func (c *initOptionsCaptureConn) Do(req *http.Request) (*http.Response, error) {
	var body struct{ Query string }
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, err
	}
	c.query = body.Query
	return nil, errors.New("request captured")
}

func TestWorkspaceInitModuleBooleanOptions(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name                        string
		opts                        []WorkspaceWithInitModuleOpts
		wantInstall, wantEntrypoint string
	}{
		{name: "omitted"},
		{name: "zero options", opts: []WorkspaceWithInitModuleOpts{{}}},
		{"true", []WorkspaceWithInitModuleOpts{{Install: &yes, Entrypoint: &yes}}, "install:true", "entrypoint:true"},
		{"false", []WorkspaceWithInitModuleOpts{{Install: &no, Entrypoint: &no}}, "install:false", "entrypoint:false"},
		{"install only", []WorkspaceWithInitModuleOpts{{Install: &no}}, "install:false", ""},
		{"entrypoint only", []WorkspaceWithInitModuleOpts{{Entrypoint: &no}}, "", "entrypoint:false"},
		{"false overrides true", []WorkspaceWithInitModuleOpts{{Install: &no, Entrypoint: &no}, {Install: &yes, Entrypoint: &yes}}, "install:false", "entrypoint:false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &initOptionsCaptureConn{}
			client, err := Connect(t.Context(), WithConn(conn))
			require.NoError(t, err)
			defer client.Close()
			_, err = client.CurrentWorkspace().WithInitModule("test", tc.opts...).ID(t.Context())
			require.ErrorContains(t, err, "request captured")
			for arg, want := range map[string]string{"install:": tc.wantInstall, "entrypoint:": tc.wantEntrypoint} {
				if want == "" {
					require.NotContains(t, conn.query, arg)
				} else {
					require.Contains(t, conn.query, want)
				}
			}
		})
	}
}
