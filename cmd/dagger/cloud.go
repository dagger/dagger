package main

import (
	"os"

	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/spf13/cobra"
)

var cloudCmd = &cobra.Command{
	Use:   "cloud",
	Short: "Dagger Cloud commands",
}

func init() {
	cloud := &CloudCLI{}

	cloudCmd.PersistentFlags().StringVar(&cloud.API, "api", "https://api.dagger.cloud", "Dagger Cloud API URL")
	cloudCmd.PersistentFlags().BoolVar(&cloud.Trace, "trace", false, "Print API request/response headers")

	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with Dagger Cloud",
		RunE:  cloud.Login,
	}
	cloudCmd.AddCommand(loginCmd)

	orgCmd := &cobra.Command{
		Use:   "org",
		Short: "Dagger Cloud org management",
	}
	cloudCmd.AddCommand(orgCmd)

	orgCreateCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new org",
		RunE:  cloud.CreateOrg,
		Args:  cobra.ExactArgs(1),
	}
	orgCmd.AddCommand(orgCreateCmd)

	orgAddUserCmd := &cobra.Command{
		Use:   "add-user <ORG> <USER_ID>",
		Short: "Add a user role to an org",
		RunE:  cloud.AddOrgUserRole,
		Args:  cobra.ExactArgs(2),
	}
	orgAddUserCmd.Flags().String("role", "member", "Role to assign to the user (member or admin)")
	orgCmd.AddCommand(orgAddUserCmd)

	orgRemoveUserCmd := &cobra.Command{
		Use:   "remove-user <ORG> <USER_ID>",
		Short: "Remove a user role from an org",
		RunE:  cloud.RemoveOrgUserRole,
		Args:  cobra.ExactArgs(2),
	}
	orgRemoveUserCmd.Flags().String("role", "member", "Role to remove from the user (member or admin)")
	orgCmd.AddCommand(orgRemoveUserCmd)

	orgCreateTokenCmd := &cobra.Command{
		Use:   "create-token <ORG> <TOKEN_NAME>",
		Short: "Create a token for sending logs to Dagger Cloud",
		RunE:  cloud.CreateOrgEngineToken,
		Args:  cobra.ExactArgs(1),
	}
	orgCreateTokenCmd.Flags().String("name", "default", "Name for the token")
	orgCmd.AddCommand(orgCreateTokenCmd)

	orgDeleteTokenCmd := &cobra.Command{
		Use:   "delete-token <ORG> <TOKEN_NAME>",
		Short: "Delete a token",
		RunE:  cloud.DeleteOrgEngineToken,
		Args:  cobra.ExactArgs(1),
	}
	orgDeleteTokenCmd.Flags().String("name", "default", "Name of the token to delete")
	orgCmd.AddCommand(orgDeleteTokenCmd)
}

type CloudCLI struct {
	API   string
	Trace bool
}

func (cli *CloudCLI) Client() (*cloud.Client, error) {
	client, err := cloud.NewClient(cli.API)
	if err != nil {
		return nil, err
	}

	client.Trace = cli.Trace
	return client, nil
}

func (cli *CloudCLI) Login(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)
	ctx := lg.WithContext(cmd.Context())

	if err := auth.Login(ctx); err != nil {
		return err
	}

	client, err := cli.Client()
	if err != nil {
		return err
	}

	user, err := client.User(ctx)
	if err != nil {
		return err
	}

	lg.Info().Str("user", user.UserID).Msg("logged in")
	return nil
}

func (cli *CloudCLI) CreateOrg(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)
	ctx := lg.WithContext(cmd.Context())

	client, err := cli.Client()
	if err != nil {
		return err
	}

	name := args[0]

	org, err := client.CreateOrg(ctx, &cloud.CreateOrgRequest{
		Name: name,
	})
	if err != nil {
		return err
	}

	lg.Info().
		Str("name", org.Name).
		Str("id", org.OrgID).
		Msg("created org")

	return nil
}

func (cli *CloudCLI) AddOrgUserRole(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)
	ctx := lg.WithContext(cmd.Context())

	client, err := cli.Client()
	if err != nil {
		return err
	}

	orgName := args[0]
	userID := args[1]
	role, err := cmd.Flags().GetString("role")
	if err != nil {
		return err
	}

	res, err := client.AddOrgUserRole(ctx, orgName, &cloud.AddOrgUserRoleRequest{
		UserID: userID,
		Role:   role,
	})
	if err != nil {
		return err
	}

	lg.Info().
		Str("org", orgName).
		Str("user", res.UserID).
		Str("role", res.Role).
		Msg("added user to org")

	return nil
}

func (cli *CloudCLI) RemoveOrgUserRole(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)
	ctx := lg.WithContext(cmd.Context())

	client, err := cli.Client()
	if err != nil {
		return err
	}

	orgName := args[0]
	userID := args[1]
	role, err := cmd.Flags().GetString("role")
	if err != nil {
		return err
	}

	res, err := client.RemoveOrgUserRole(ctx, orgName, &cloud.RemoveOrgUserRoleRequest{
		UserID: userID,
		Role:   role,
	})
	if err != nil {
		return err
	}

	lg.Info().
		Str("org", orgName).
		Str("user", userID).
		Str("role", role).
		Bool("existed", res.Existed).
		Msg("removed user role from org")

	return nil
}

func (cli *CloudCLI) CreateOrgEngineToken(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)
	ctx := lg.WithContext(cmd.Context())

	client, err := cli.Client()
	if err != nil {
		return err
	}

	orgName := args[0]
	tokenName, err := cmd.Flags().GetString("name")
	if err != nil {
		return err
	}

	res, err := client.CreateOrgEngineToken(ctx, orgName, &cloud.CreateOrgEngineTokenRequest{
		Name: tokenName,
	})
	if err != nil {
		return err
	}

	lg.Info().
		Str("org", orgName).
		Str("name", tokenName).
		Str("token", res.Token).
		Msg("created engine token")

	return nil
}

func (cli *CloudCLI) DeleteOrgEngineToken(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)
	ctx := lg.WithContext(cmd.Context())

	client, err := cli.Client()
	if err != nil {
		return err
	}

	orgName := args[0]
	tokenName, err := cmd.Flags().GetString("name")
	if err != nil {
		return err
	}

	res, err := client.DeleteOrgEngineToken(ctx, orgName, &cloud.DeleteOrgEngineTokenRequest{
		Name: tokenName,
	})
	if err != nil {
		return err
	}

	lg.Info().
		Str("name", tokenName).
		Bool("existed", res.Existed).
		Msg("deleted engine token")

	return nil
}
