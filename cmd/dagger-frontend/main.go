package main

import (
	"dagger.cloud/go/dagger"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
)

func main() {
	r := &dagger.Runtime{}
	if err := grpcclient.RunFromEnvironment(appcontext.Context(), r.BKFrontend); err != nil {
		panic(err)
	}
}
