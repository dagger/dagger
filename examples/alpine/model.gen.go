package alpine

import (
	"encoding/json"
	"sync"

	"github.com/dagger/cloak/dagger"

	// TODO: this needs to be generated based on which schemas are re-used in this schema
	"github.com/dagger/cloak/dagger/core"
)

type Build struct {
	Packages []string `json:"packages,omitempty"`

	once sync.Once
	memo buildOutput
}

func (a *Build) FS(ctx *dagger.Context) core.FSOutput {
	return a.outputOnce(ctx).FS
}

type buildOutput struct {
	FS core.FSOutput
}

type BuildOutput interface {
	FS(ctx *dagger.Context) core.FSOutput
}

func (a *Build) outputOnce(ctx *dagger.Context) buildOutput {
	a.once.Do(func() {
		input, err := json.Marshal(a)
		if err != nil {
			panic(err)
		}
		rawOutput, err := dagger.Do(ctx, "localhost:5555/dagger:alpine", "build", string(input))
		if err != nil {
			panic(err)
		}
		if err := rawOutput.Decode(&a.memo); err != nil {
			panic(err)
		}
	})
	return a.memo
}
