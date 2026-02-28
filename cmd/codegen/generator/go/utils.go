package gogenerator

import (
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
