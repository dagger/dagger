package netlify

import (
	"encoding/json"

	"github.com/dagger/cloak/dagger"

	// TODO: this needs to be generated based on which schemas are re-used in this schema
	"github.com/dagger/cloak/dagger/core"
)

// Netlify actions for Dagger

type DeployInput struct {
	// Contents of the site
	Contents core.FSOutput `json:"contents,omitempty"`

	// Name of the Netlify site
	// Example: "my-super-site"
	// Create the site if it doesn't exist
	// create: *true | false
	Site string `json:"site,omitempty"`
}

type DeployOutput struct {
	// URL of the deployed site
	URL string

	// URL of the latest deployment
	// URL for logs of the latest deployment
	// logsUrl: string
	DeployURL string
}

func Deploy(ctx *dagger.Context, input *DeployInput) *DeployOutput {
	rawInput, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}

	rawOutput, err := dagger.Do(ctx, "localhost:5555/dagger:netlify", "deploy", string(rawInput))
	if err != nil {
		panic(err)
	}
	output := &DeployOutput{}
	if err := rawOutput.Decode(output); err != nil {
		panic(err)
	}
	return output
}
