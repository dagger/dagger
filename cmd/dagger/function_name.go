package main

import (
	"strings"

	"github.com/dagger/dagger/engine/client"
)

func initModuleParams(a []string) client.Params {
	params := client.Params{
		ExecCmd:  a,
		Function: functionName(a),
		EagerModuleLoading: eagerModuleLoading,
	}

	if !moduleNoURL {
		modRef, _ := getExplicitModuleSourceRef()
		if modRef != "" {
			params.Module = modRef
		}
	}
	return params
}

func functionName(args []string) string {
	if len(args) == 0 {
		return ""
	}

	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			if !strings.Contains(arg, "=") {
				// this flag is not self-contained so we can't be sure what the
				// top level function is
				return ""
			}
			// it is a self contained flag os we are fine to continue scanning
			continue
		}

		// we have the first non-flag argument which should be the top level function
		// name so we return it
		return arg
	}

	// weird case that would happen when users make a dagger call with no functions
	// an edge-case but we cover it anyway
	return ""
}
