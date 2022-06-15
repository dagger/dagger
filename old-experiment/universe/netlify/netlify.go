package netlify

import (
	"dagger.io/dagger"
	"dagger.io/universe/alpine"
)

const script = `
netlify deploy \
	--build \
	--site="$site_id" \
	--prod |
	tee /tmp/stdout

url="$(grep </tmp/stdout Website | grep -Eo 'https://[^ >]+' | head -1)"
deployUrl="$(grep </tmp/stdout Unique | grep -Eo 'https://[^ >]+' | head -1)"
logsUrl="$(grep </tmp/stdout Logs | grep -Eo 'https://[^ >]+' | head -1)"

# Write output files
mkdir -p /netlify
echo -n "$url" >/netlify/url
echo -n "$deployUrl" >/netlify/deployUrl
echo -n "$logsUrl" >/netlify/logsUrl
`

type Deployment struct {
	URL       dagger.String
	DeployURL dagger.String
}

func Deploy(source *dagger.FS) *Deployment {
	base := alpine.
		New().
		Add("bash", "curl", "jq", "npm").
		Image()

	image := base.
		Run("npm", "-g", "install", "netlify-cli@8.6.21").
		Run("sh", "-c", script)

	return &Deployment{
		URL:       image.FS().ReadFile("/netlify/deployUrl"),
		DeployURL: image.FS().ReadFile("/netlify/url"),
	}
}
