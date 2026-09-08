package daggercmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
)

var cloudCLI = &CloudCLI{}

var loginSwitchAccount bool

var cloudCmd = &cobra.Command{
	Use:   "cloud",
	Short: "Manage Dagger Cloud",
}

var (
	cloudLoginCmd = newLoginCmd(false)
	loginCmd      = newLoginCmd(true)
)

var (
	cloudLogoutCmd = newLogoutCmd(false)
	logoutCmd      = newLogoutCmd(true)
)

func init() {
	cloudCmd.AddCommand(cloudLoginCmd, cloudLogoutCmd)
	rootCmd.AddCommand(cloudCmd, loginCmd, logoutCmd)
}

func newLoginCmd(hidden bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "login [options] [org]",
		Short:  "Log in to Dagger Cloud",
		Args:   cobra.MaximumNArgs(1),
		Hidden: hidden,
		RunE:   cloudCLI.Login,
	}
	cmd.Flags().BoolVar(&loginSwitchAccount, "switch-account", false, "Choose a different Dagger Cloud account")
	return cmd
}

func newLogoutCmd(hidden bool) *cobra.Command {
	return &cobra.Command{
		Use:    "logout",
		Short:  "Log out from Dagger Cloud",
		Args:   cobra.NoArgs,
		Hidden: hidden,
		RunE:   cloudCLI.Logout,
	}
}

type CloudCLI struct{}

func (cli *CloudCLI) Login(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	outW := cmd.OutOrStdout()
	errW := cmd.ErrOrStderr()

	var orgName string
	if len(args) > 0 {
		orgName = args[0]
	}
	if orgName == "" {
		orgName = cloudOrgFlag
	}

	loginOpts := []auth.LoginOption{}
	if loginSwitchAccount {
		loginOpts = append(loginOpts, auth.WithSwitchAccount())
	}
	if err := auth.Login(ctx, outW, loginOpts...); err != nil {
		return err
	}

	var t *oauth2.Token
	var err error
	if t, err = auth.Token(ctx); err != nil {
		return err
	}

	client, err := cloud.NewClient(ctx, &auth.Cloud{Token: t})
	if err != nil {
		return err
	}

	user, err := client.User(ctx)
	if err != nil {
		return err
	}
	var selectedOrg *auth.Org
	switch len(user.Orgs) {
	case 0:
		selectedOrg, err = createNewOrg(ctx, client, user, orgName, errW)
		if err != nil {
			fmt.Fprintf(errW, "Error setting up new organization: %v\n", err)
			return idtui.Fail
		}
	case 1:
		if orgName != "" && user.Orgs[0].Name != orgName {
			fmt.Fprintln(errW, "Organization", orgName, "not found.")
			return idtui.Fail
		}
		selectedOrg = &user.Orgs[0]
	default:
		if orgName == "" {
			for _, org := range user.Orgs {
				fmt.Fprintf(errW, "- %s\n", org.Name)
			}
			fmt.Fprintf(errW, "\n\nYou are a member of multiple organizations. Please select one with `dagger login <org>`.\n")
			return idtui.Fail
		}
		for _, org := range user.Orgs {
			if org.Name == orgName {
				selectedOrg = &org
				break
			}
		}
		if selectedOrg == nil {
			fmt.Fprintln(errW, "Organization", orgName, "not found.")
			return idtui.Fail
		}
	}

	if err := auth.SetCurrentOrg(selectedOrg); err != nil {
		return err
	}
	if err := clearSetupCloudLoginPromptPreference(); err != nil {
		return err
	}

	fmt.Fprintln(outW, "Success.")

	return nil
}

// createNewOrg creates a free Dagger Cloud organization entirely from the CLI
// (no browser round-trip). The org name is taken from the requested name when
// provided, otherwise derived from the account so the user isn't forced to
// invent one. The server may adjust the name (e.g. to resolve collisions), so
// the returned org reflects the actual name.
func createNewOrg(ctx context.Context, cli *cloud.Client, user *cloud.UserResponse, requestedName string, w io.Writer) (*auth.Org, error) {
	name := requestedName
	if name == "" {
		name = defaultOrgName(user)
	}

	fmt.Fprintln(w, "You are not a member of any Dagger Cloud organizations.")
	fmt.Fprintf(w, "Creating a new organization %q...\n", name)

	org, err := cli.CreateQuickstartOrg(ctx, name)
	if err != nil {
		return nil, err
	}
	if org.Name != name {
		fmt.Fprintf(w, "Created organization %q.\n", org.Name)
	}
	return &auth.Org{ID: org.ID, Name: org.Name}, nil
}

// defaultOrgName derives an org name from the authenticated account, preferring
// the account nickname (typically the GitHub login), then the email local part,
// falling back to a generic name. Org names are not user-facing identifiers, so
// a sensible default avoids an unnecessary prompt.
func defaultOrgName(user *cloud.UserResponse) string {
	if user != nil {
		if name := sanitizeOrgName(user.Nickname); name != "" {
			return name
		}
		if local, _, ok := strings.Cut(user.Email, "@"); ok {
			if name := sanitizeOrgName(local); name != "" {
				return name
			}
		}
	}
	return "my-org"
}

// sanitizeOrgName reduces an arbitrary string to a lowercase slug of ASCII
// alphanumerics and single hyphens, suitable as a default org name.
func sanitizeOrgName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastHyphen := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		case r == '-' || r == '_' || r == '.' || r == ' ':
			if b.Len() > 0 && !lastHyphen {
				b.WriteRune('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func (cli *CloudCLI) Logout(cmd *cobra.Command, args []string) error {
	return auth.Logout()
}
