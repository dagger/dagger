package cloud

import "context"

// EntireInstallation is an installation of the Dagger plugin on an Entire
// project.
type EntireInstallation struct {
	// ID is the installation's Git source ID, such as "entire:42".
	ID        string `json:"id"`
	Name      string `json:"name"`
	ManageURL string `json:"manageURL"`
}

const getEntireInstallationsOperation = `
query GetEntireInstallations($org: ID!) {
	entireInstallations(org: $org) {
		id
		name
		manageURL
	}
}
`

// EntireInstallations returns the Entire installations the user can use in
// the org.
func (c *Client) EntireInstallations(ctx context.Context, orgID string) ([]EntireInstallation, error) {
	var data struct {
		EntireInstallations []EntireInstallation `json:"entireInstallations"`
	}
	if err := c.doGraphQL(ctx, "GetEntireInstallations", getEntireInstallationsOperation, map[string]any{
		"org": orgID,
	}, &data); err != nil {
		return nil, err
	}
	return data.EntireInstallations, nil
}

const getEntireInstallURLOperation = `
query GetEntireInstallURL($org: ID!, $redirectURI: String!) {
	entireInstallURL(org: $org, redirectURI: $redirectURI)
}
`

// EntireInstallURL returns the Entire page that installs the Dagger plugin on
// a project. Entire returns the browser to redirectURI, which must be one of
// the plugin's registered post-install URLs.
func (c *Client) EntireInstallURL(ctx context.Context, orgID, redirectURI string) (string, error) {
	var data struct {
		EntireInstallURL string `json:"entireInstallURL"`
	}
	if err := c.doGraphQL(ctx, "GetEntireInstallURL", getEntireInstallURLOperation, map[string]any{
		"org":         orgID,
		"redirectURI": redirectURI,
	}, &data); err != nil {
		return "", err
	}
	return data.EntireInstallURL, nil
}
