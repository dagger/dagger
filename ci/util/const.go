package util

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/dagger/dagger/engine/distconsts"
	"golang.org/x/exp/maps"

	"dagger/internal/dagger"
)

var dag = dagger.Connect()

const (
	engineServerPath    = "/usr/local/bin/dagger-engine"
	engineDialStdioPath = "/usr/local/bin/dial-stdio"
	engineShimPath      = distconsts.EngineShimPath

	golangVersion = "1.21.3"
	alpineVersion = "3.18"
	ubuntuVersion = "22.04"
	runcVersion   = "v1.1.12"
	cniVersion    = "v1.3.0"
	qemuBinImage  = "tonistiigi/binfmt@sha256:e06789462ac7e2e096b53bfd9e607412426850227afeb1d0f5dfa48a731e0ba5"

	engineTomlPath = "/etc/dagger/engine.toml"

	engineEntrypointPath = "/usr/local/bin/dagger-entrypoint.sh"

	CacheConfigEnvName = "_EXPERIMENTAL_DAGGER_CACHE_CONFIG"
	GPUSupportEnvName  = "_EXPERIMENTAL_DAGGER_GPU_SUPPORT"
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

func getEntrypoint(kvs []string) (string, error) {
	opts := map[string]string{}
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return "", fmt.Errorf("no value for key %q", k)
		}
		opts[k] = v
	}
	keys := maps.Keys(opts)
	sort.Strings(keys)

	var entrypoint string

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
		EngineBin:         engineServerPath,
		EngineConfig:      engineTomlPath,
		EntrypointArgs:    opts,
		EntrypointArgKeys: keys,
	})
	if err != nil {
		panic(err)
	}
	entrypoint = buf.String()

	return entrypoint, nil
}

func getConfig(kvs []string) (string, error) {
	opts := map[string]string{}
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return "", fmt.Errorf("no value for key %q", k)
		}
		opts[k] = v
	}
	keys := maps.Keys(opts)
	sort.Strings(keys)

	var config string

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
	config = buf.String()

	return config, nil
}
