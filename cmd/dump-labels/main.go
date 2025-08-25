package main

import (
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/pathutil"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

func main() {
	workdir, err := normalizeWorkdir(".")
	if err != nil {
		panic(err)
	}

	labels := enginetel.LoadDefaultLabels(workdir, engine.Version)
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		fmt.Println(k + "=" + labels[k])
	}
}

func normalizeWorkdir(workdir string) (string, error) {
	if workdir == "" {
		workdir = os.Getenv("DAGGER_WORKDIR")
	}

	if workdir == "" {
		var err error
		workdir, err = pathutil.Getwd()
		if err != nil {
			return "", err
		}
	}
	workdir, err := pathutil.Abs(workdir)
	if err != nil {
		return "", err
	}

	return workdir, nil
}
