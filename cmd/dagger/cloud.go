package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
)

var cloudGroup = &cobra.Group{
	ID:    "cloud",
	Title: "Dagger Cloud Commands",
}

var cloudCLI = &CloudCLI{}

var loginSwitchAccount bool

var loginCmd = &cobra.Command{
	Use:     "login [options] [org]",
	Aliases: []string{"signup"},
	Short:   "Log in or sign up to Dagger Cloud",
	GroupID: cloudGroup.ID,
	RunE:    cloudCLI.Login,
}

var logoutCmd = &cobra.Command{
	Use:     "logout",
	Short:   "Log out from Dagger Cloud",
	GroupID: cloudGroup.ID,
	RunE:    cloudCLI.Logout,
}

var cloudCmd = &cobra.Command{
	Use:     "cloud",
	Aliases: []string{"c"},
	Short:   "Manage Dagger Cloud",
	GroupID: cloudGroup.ID,
}

var cloudLoginCmd = &cobra.Command{
	Use:     "login [options] [org]",
	Aliases: []string{"signup"},
	Short:   "Log in or sign up to Dagger Cloud",
	RunE:    cloudCLI.Login,
}

var cloudLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out from Dagger Cloud",
	RunE:  cloudCLI.Logout,
}

func init() {
	loginCmd.Flags().BoolVar(&loginSwitchAccount, "switch-account", false, "Choose a different Dagger Cloud account")
	cloudLoginCmd.Flags().BoolVar(&loginSwitchAccount, "switch-account", false, "Choose a different Dagger Cloud account")
	rootCmd.AddGroup(cloudGroup)
	cloudCmd.AddCommand(cloudLoginCmd, cloudLogoutCmd)
	rootCmd.AddCommand(cloudCmd, loginCmd, logoutCmd)
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

	signup := strings.EqualFold(cmd.CalledAs(), "signup")
	loginOpts := []auth.LoginOption{}
	if signup {
		loginOpts = append(loginOpts, auth.WithSignup())
	}
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
		fmt.Fprintln(errW, "You are not a member of any organizations, creating a new one...")
		selectedOrg, err = createNewOrg(ctx, client, errW)
		if err != nil {
			// logging out user here so terminal is not filled with 403
			// errors the next time `dagger login` is called since the user
			// still doesn't have an org set
			auth.Logout()
			fmt.Fprintf(errW, "Error setting up new organization: %v", err)
			return idtui.Fail
		}
	case 1:
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

	fmt.Fprintln(outW, "Success.")
	cli.printPostLoginRepoSetupHint(ctx, client, selectedOrg, outW)

	return nil
}

func (cli *CloudCLI) printPostLoginRepoSetupHint(ctx context.Context, client *cloud.Client, org *auth.Org, out io.Writer) {
	if org == nil {
		return
	}
	if _, err := repoFromArgOrGit(ctx, nil); err != nil {
		return
	}
	integrations, err := client.Integrations(ctx, org.ID)
	if err != nil {
		return
	}
	for _, integration := range integrations {
		if strings.EqualFold(integration.Name, "GitHub") && integrationEnabled(integration) {
			return
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "next:")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "1. add a github integration")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  dagger integration add github")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "2. enable autocheck for this repo")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  dagger repo enable autocheck")
}

func createNewOrg(ctx context.Context, cli *cloud.Client, w io.Writer) (*auth.Org, error) {
	url := "https://dagger.cloud/traces/setup"
	err := browser.OpenURL(url)
	if err != nil {
		fmt.Fprintf(w, "Unable to open browser automatically, please visit %s to create an organization.\n", url)
	}

	timer := time.After(15 * time.Second)
	t := time.NewTicker(1 * time.Second)

	defer t.Stop()

	for {
		select {
		case <-timer:
			return nil, errors.New("timed out waiting to create an organization")
		case <-t.C:
			u, err := cli.User(ctx)
			if err != nil {
				return nil, err
			}
			if len(u.Orgs) == 0 {
				continue
			}

			user, err := cli.User(ctx)
			if err != nil {
				return nil, err
			}
			return &user.Orgs[0], nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (cli *CloudCLI) Logout(cmd *cobra.Command, args []string) error {
	return auth.Logout()
}
