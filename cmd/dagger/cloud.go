package main

import (
	"context"
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
		RunE:  cloud.CreateOrgEngineIngestionToken,
		Args:  cobra.ExactArgs(1),
	}
	orgCreateTokenCmd.Flags().String("name", "default", "Name for the token")
	orgCmd.AddCommand(orgCreateTokenCmd)

	orgDeleteTokenCmd := &cobra.Command{
		Use:   "delete-token <ORG> <TOKEN>",
		Short: "Delete a token",
		RunE:  cloud.DeleteOrgEngineIngestionToken,
		Args:  cobra.ExactArgs(2),
	}
	orgCmd.AddCommand(orgDeleteTokenCmd)
}

type CloudCLI struct {
	API   string
	Trace bool
}

func (cli *CloudCLI) Client(ctx context.Context) (*cloud.Client, error) {
	client, err := cloud.NewClient(ctx, cli.API)
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

	client, err := cli.Client(ctx)
	if err != nil {
		return err
	}

	user, err := client.User(ctx)
	if err != nil {
		return err
	}

	lg.Info().Str("user", user.ID).Msg("logged in")
	return nil
}

func (cli *CloudCLI) CreateOrg(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)
	ctx := lg.WithContext(cmd.Context())

	client, err := cli.Client(ctx)
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
		Str("id", org.ID).
		Msg("created org")

	return nil
}

func (cli *CloudCLI) AddOrgUserRole(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)
	ctx := lg.WithContext(cmd.Context())

	client, err := cli.Client(ctx)
	if err != nil {
		return err
	}

	orgName := args[0]
	userID := args[1]
	role, err := cmd.Flags().GetString("role")
	if err != nil {
		return err
	}

	org, err := client.Org(ctx, orgName)
	if err != nil {
		return err
	}

	res, err := client.AddOrgUserRole(ctx, &cloud.AddOrgUserRoleRequest{
		OrgID:  org.ID,
		UserID: userID,
		Role:   cloud.NewRole(role),
	})
	if err != nil {
		return err
	}

	lg.Info().
		Str("org", orgName).
		Str("user", res.UserID).
		Str("role", res.Role.String()).
		Msg("added user to org")

	return nil
}

func (cli *CloudCLI) RemoveOrgUserRole(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)
	ctx := lg.WithContext(cmd.Context())

	client, err := cli.Client(ctx)
	if err != nil {
		return err
	}

	orgName := args[0]
	userID := args[1]
	role, err := cmd.Flags().GetString("role")
	if err != nil {
		return err
	}

	org, err := client.Org(ctx, orgName)
	if err != nil {
		return err
	}

	res, err := client.RemoveOrgUserRole(ctx, &cloud.RemoveOrgUserRoleRequest{
		OrgID:  org.ID,
		UserID: userID,
		Role:   cloud.NewRole(role),
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

func (cli *CloudCLI) CreateOrgEngineIngestionToken(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)
	ctx := lg.WithContext(cmd.Context())

	client, err := cli.Client(ctx)
	if err != nil {
		return err
	}

	orgName := args[0]
	tokenName, err := cmd.Flags().GetString("name")
	if err != nil {
		return err
	}

	org, err := client.Org(ctx, orgName)
	if err != nil {
		return err
	}

	res, err := client.CreateOrgEngineIngestionToken(ctx, &cloud.CreateOrgEngineIngestionTokenRequest{
		OrgID: org.ID,
		Name:  tokenName,
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

func (cli *CloudCLI) DeleteOrgEngineIngestionToken(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)
	ctx := lg.WithContext(cmd.Context())

	client, err := cli.Client(ctx)
	if err != nil {
		return err
	}

	orgName := args[0]
	token := args[1]

	org, err := client.Org(ctx, orgName)
	if err != nil {
		return err
	}

	res, err := client.DeleteOrgEngineIngestionToken(ctx, &cloud.DeleteOrgEngineIngestionTokenRequest{
		OrgID: org.ID,
		Token: token,
	})
	if err != nil {
		return err
	}

	lg.Info().
		Bool("existed", res.Existed).
		Msg("deleted engine token")

	return nil
}
