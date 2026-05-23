package gogenerator

import (
	"os"
	"strings"

	"golang.org/x/mod/modfile"
)

func isDaggerPkgCustomReplaced(replaces []*modfile.Replace) bool {
	for _, r := range replaces {
		if r.Old.Path == daggerImportPath {
			return true
		}
	}

	return false
}

func goCommandEnv() []string {
	env := append([]string{}, os.Environ()...)
	hasProxy := false
	hasSumDB := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "GOPROXY=") {
			hasProxy = true
		}
		if strings.HasPrefix(kv, "GOSUMDB=") {
			hasSumDB = true
		}
	}
	if !hasProxy {
		env = append(env, "GOPROXY=direct")
	}
	if !hasSumDB {
		env = append(env, "GOSUMDB=off")
	}
	return env
}
