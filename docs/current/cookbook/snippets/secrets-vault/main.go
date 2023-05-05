package main

import (
	"context"
	"os"
	"fmt"

	"dagger.io/dagger"
	"github.com/hashicorp/vault-client-go"
	"github.com/hashicorp/vault-client-go/schema"
)

func main() {
	ctx := context.Background()

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// get secret from Vault
	secretPlaintext, err := getVaultSecret(MOUNT-PATH, SECRET-ID, SECRET-KEY)
	if err != nil {
		panic(err)
	}

	// load secret into Dagger
	secret := client.SetSecret("ghApiToken", secretPlaintext)

	// use secret in container environment
	out, err := client.Container().
		From("alpine:3.17").
		WithSecretVariable("GITHUB_API_TOKEN", secret).
		WithExec([]string{"apk", "add", "curl"}).
		WithExec([]string{"sh", "-c", "curl \"https://api.github.com/repos/dagger/dagger/issues\" --header \"Accept: application/vnd.github+json\" --header \"Authorization: Bearer $GITHUB_API_TOKEN\""}).
		Stdout(ctx)

	// print result
	fmt.Println(out)
}

func getVaultSecret(mountPath, secretID, secretKey string) (string, error) {
	ctx := context.Background()

	// check for required variables in host environment
	address := os.Getenv("VAULT_ADDRESS")
	role_id := os.Getenv("VAULT_ROLE_ID")
	secret_id := os.Getenv("VAULT_SECRET_ID")

	// create Vault client
	client, err := vault.New(
		vault.WithAddress(address),
	)
	if err != nil {
		return "", err
	}

	// log in to Vault
	resp, err := client.Auth.AppRoleLogin(
		ctx,
		schema.AppRoleLoginRequest{
			RoleId:   role_id,
			SecretId: secret_id,
		},
		vault.WithMountPath(mountPath),
	)
	if err != nil {
		return "", err
	}

	if err := client.SetToken(resp.Auth.ClientToken); err != nil {
		return "", err
	}

	// read and return secret
	secret, err := client.Secrets.KvV2Read(
		ctx,
		secretID,
		vault.WithMountPath(mountPath),
	)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s", secret.Data.Data[secretKey]), nil
}
