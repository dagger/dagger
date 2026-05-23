package gogenerator

import (
	"os"

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
	env = append(env,
		"GOPROXY=direct",
		"GOSUMDB=off",
	)
	return env
}
