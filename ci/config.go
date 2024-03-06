package main

import (
	"bytes"
	"dagger/internal/dagger"
	"dagger/util"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"golang.org/x/exp/maps"
)

const (
	engineTomlPath       = "/etc/dagger/engine.toml"
	engineEntrypointPath = "/usr/local/bin/dagger-entrypoint.sh"
)

const engineEntrypointTmpl = `#!/bin/sh
set -e

cat $0

# cgroup v2: enable nesting
# see https://github.com/moby/moby/blob/38805f20f9bcc5e87869d6c79d432b166e1c88b4/hack/dind#L28
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
	# move the processes from the root group to the /init group,
	# otherwise writing subtree_control fails with EBUSY.
	# An error during moving non-existent process (i.e., "cat") is ignored.
	mkdir -p /sys/fs/cgroup/init
	xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init/cgroup.procs || :
	# enable controllers
	sed -e 's/ / +/g' -e 's/^/+/' < /sys/fs/cgroup/cgroup.controllers \
		> /sys/fs/cgroup/cgroup.subtree_control
fi

exec {{.EngineBin}} --config {{.EngineConfig}} {{ range $key := .EntrypointArgKeys -}}--{{ $key }}="{{ index $.EntrypointArgs $key }}" {{ end -}} "$@"
`

const engineConfigTmpl = `
debug = true
insecure-entitlements = ["security.insecure"]
{{ range $key := .ConfigKeys }}
[{{ $key }}]
{{ index $.ConfigEntries $key }}
{{ end -}}
`

func generateEntrypoint(kvs []string) (*File, error) {
	opts := map[string]string{}
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("no value for key %q", k)
		}
		opts[k] = v
	}
	keys := maps.Keys(opts)
	sort.Strings(keys)

	type entrypointTmplParams struct {
		Bridge            string
		EngineBin         string
		EngineConfig      string
		EntrypointArgs    map[string]string
		EntrypointArgKeys []string
	}
	tmpl := template.Must(template.New("entrypoint").Parse(engineEntrypointTmpl))
	buf := new(bytes.Buffer)
	err := tmpl.Execute(buf, entrypointTmplParams{
		EngineBin:         util.EngineServerPath,
		EngineConfig:      engineTomlPath,
		EntrypointArgs:    opts,
		EntrypointArgKeys: keys,
	})
	if err != nil {
		panic(err)
	}

	entrypoint := dag.Directory().
		WithNewFile("dagger-entrypoint.sh", buf.String(), dagger.DirectoryWithNewFileOpts{
			Permissions: 0o755,
		}).
		File("dagger-entrypoint.sh")
	return entrypoint, nil
}

func generateConfig(kvs []string) (*File, error) {
	opts := map[string]string{}
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("no value for key %q", k)
		}
		opts[k] = v
	}
	keys := maps.Keys(opts)
	sort.Strings(keys)

	type configTmplParams struct {
		ConfigEntries map[string]string
		ConfigKeys    []string
	}
	tmpl := template.Must(template.New("config").Parse(engineConfigTmpl))
	buf := new(bytes.Buffer)
	err := tmpl.Execute(buf, configTmplParams{
		ConfigEntries: opts,
		ConfigKeys:    keys,
	})
	if err != nil {
		panic(err)
	}

	config := dag.Directory().
		WithNewFile("engine.toml", buf.String()).
		File("engine.toml")
	return config, nil
}
