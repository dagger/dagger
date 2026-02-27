package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger/dag"
	"golang.org/x/mod/semver"
)

func main() {
	ctx := context.Background()

	repoDir := dag.Host().Directory("/work/repo")
	currentDir := dag.Host().Directory("/work/current")

	changes := repoDir.Changes(currentDir)
	isSame, err := changes.IsEmpty(ctx)
	if err != nil {
		fmt.Printf("failed to check differences: %s\n", err.Error())
		os.Exit(1)
	}

	// If it's different load package.json and compare the version
	if !isSame {
		repoPackageVersion, err := repoDir.
			File("package.json").
			AsJSON().
			Field([]string{"version"}).
			AsString(ctx)
		if err != nil {
			fmt.Printf("failed to load package.json: %s\n", err.Error())
			os.Exit(1)
		}

		currentPackageVersion, err := currentDir.
			File("package.json").
			AsJSON().
			Field([]string{"version"}).
			AsString(ctx)
		if err != nil {
			fmt.Printf("failed to load package.json: %s\n", err.Error())
			os.Exit(1)
		}

		// add leading `v` so semver.compare works as expected
		currentPackageVersion = "v" + currentPackageVersion

		if semver.Compare(repoPackageVersion, currentPackageVersion) > 0 {
			fmt.Println("package.json version must be bumped")
			os.Exit(1)
		}
	}
}
