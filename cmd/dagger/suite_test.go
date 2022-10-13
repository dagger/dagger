package main

import "go.dagger.io/dagger/internal/testutil"

func init() {
	if err := testutil.SetupBuildkitd(); err != nil {
		panic(err)
	}
}
