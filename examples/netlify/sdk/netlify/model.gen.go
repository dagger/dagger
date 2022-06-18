package netlify

import (
	"github.com/dagger/cloak/dagger"
)

// Netlify actions for Dagger

type DeployInput struct {
	// Contents of the site
	Contents dagger.FS `json:"contents,omitempty"`

	// Name of the Netlify site
	// Example: "my-super-site"
	// Create the site if it doesn't exist
	// create: *true | false
	Site string `json:"site,omitempty"`
}

type DeployOutput struct {
	// URL of the deployed site
	URL string `json:"url,omitempty"`

	// URL of the latest deployment
	// URL for logs of the latest deployment
	// logsUrl: string
	DeployURL string `json:"deployurl,omitempty"`
}

func Deploy(ctx *dagger.Context, input *DeployInput) *DeployOutput {
	fsInput, err := dagger.Marshal(ctx, input)
	if err != nil {
		panic(err)
	}

	fsOutput, err := dagger.Do(ctx, "localhost:5555/dagger:netlify", "deploy", fsInput)
	if err != nil {
		panic(err)
	}
	output := &DeployOutput{}
	if err := dagger.Unmarshal(ctx, fsOutput, output); err != nil {
		panic(err)
	}
	return output
}
