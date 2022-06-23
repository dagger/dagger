package alpine

import (
	"github.com/dagger/cloak/dagger"
)

type BuildInput struct {
	Packages []dagger.String `json:"packages,omitempty"`
}

type BuildOutput struct {
	Root dagger.FS `json:"root,omitempty"`
}

type BuildResult struct {
	res dagger.Result
}

func (o *BuildResult) MarshalJSON() ([]byte, error) {
	return o.res.MarshalJSON()
}

func (o *BuildResult) UnmarshalJSON(b []byte) error {
	return o.res.UnmarshalJSON(b)
}

func (o *BuildResult) Root() dagger.FS {
	return o.res.GetField("root").FS()
}

func Build(ctx *dagger.Context, input *BuildInput) *BuildResult {
	result, err := dagger.Do(ctx, "localhost:5555/dagger:alpine", "build", input)
	if err != nil {
		panic(err)
	}
	return &BuildResult{res: *result}
}
