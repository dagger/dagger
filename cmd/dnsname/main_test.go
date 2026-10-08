package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/stretchr/testify/require"
)

// A container with no name and no aliases gets no hosts entry: the line would
// hold only its IP, resolve nothing, and never be removed. Named and aliased
// containers keep theirs.
func TestCmdAddHostsEntry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		podname string
		aliases []string
		want    string // hosts file content; "" means the file is not written
	}{
		{name: "unnamed", want: ""},
		{name: "named", podname: "web", want: "10.87.0.5\tweb\n"},
		{name: "aliases only", aliases: []string{"db"}, want: "10.87.0.5\t\tdb\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// dnsmasq is not running: point the pidfile at an exited process,
			// so the reload signal is skipped.
			exited := exec.Command("true")
			require.NoError(t, exited.Run())
			pidfile := filepath.Join(dir, "pidfile")
			require.NoError(t, os.WriteFile(pidfile, []byte(strconv.Itoa(exited.Process.Pid)), 0o600))
			hosts := filepath.Join(dir, "addnhosts")

			conf := map[string]any{
				"cniVersion": "0.4.0",
				"name":       "dagger",
				"type":       "dnsname",
				"domainName": "dagger.local",
				"hosts":      hosts,
				"pidfile":    pidfile,
				"lockfile":   filepath.Join(dir, "lock"),
				"runtimeConfig": map[string]any{
					"aliases": map[string][]string{"dagger": tc.aliases},
				},
				"prevResult": map[string]any{
					"cniVersion": "0.4.0",
					"interfaces": []map[string]any{{"name": "lo"}},
					"ips":        []map[string]any{{"version": "4", "address": "10.87.0.5/16"}},
				},
			}
			stdin, err := json.Marshal(conf)
			require.NoError(t, err)
			args := ""
			if tc.podname != "" {
				args = "K8S_POD_NAME=" + tc.podname
			}

			require.NoError(t, cmdAdd(&skel.CmdArgs{ContainerID: "ctr", StdinData: stdin, Args: args}))

			got, err := os.ReadFile(hosts)
			if tc.want == "" {
				require.ErrorIs(t, err, os.ErrNotExist)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, string(got))
		})
	}
}
