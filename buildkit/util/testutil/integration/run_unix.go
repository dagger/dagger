//go:build !windows
// +build !windows

package integration

import "runtime"

func officialImages(names ...string) map[string]string {
	ns := runtime.GOARCH
	if ns == "arm64" {
		ns = "arm64v8"
	} else if ns != "amd64" {
		ns = "library"
	}
	m := map[string]string{}
	for _, name := range names {
		ref := "docker.io/" + ns + "/" + name
		if pns, ok := pins[name]; ok {
			if dgst, ok := pns[ns]; ok {
				ref += "@" + dgst
			}
		}
		m["library/"+name] = ref
	}
	return m
}
