package main

import (
	"context"
	"fmt"
	"io"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// get secret from Google Cloud Secret Manager
	secretPlaintext, err := gcpGetSecretPlaintext(ctx, "PROJECT-ID", "SECRET-ID")
	if err != nil {
		panic(err)
	}

	// initialize Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(io.Discard))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// load secret into Dagger
	secret := client.SetSecret("ghApiToken", string(secretPlaintext))

	// use secret in container environment
	out, err := client.
		Container().
		From("alpine:3.17").
		WithSecretVariable("GITHUB_API_TOKEN", secret).
		WithExec([]string{"apk", "add", "curl"}).
		WithExec([]string{"sh", "-c", `curl "https://api.github.com/repos/dagger/dagger/issues" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer $GITHUB_API_TOKEN"`}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}

	// print result
	fmt.Println(out)
}

func gcpGetSecretPlaintext(ctx context.Context, projectID, secretID string) (string, error) {
	version := 1
	secretVersion := fmt.Sprintf("projects/%s/secrets/%s/versions/%d", projectID, secretID, version)

	// initialize Google Cloud API client
	gcpClient, err := secretmanager.NewClient(ctx)
	if err != nil {
		panic(err)
	}
	defer gcpClient.Close()

	// retrieve secret
	secReq := &secretmanagerpb.AccessSecretVersionRequest{
		Name: secretVersion,
	}

	res, err := gcpClient.AccessSecretVersion(ctx, secReq)
	if err != nil {
		panic(err)
	}

	secretPlaintext := res.Payload.Data

	return string(secretPlaintext), nil
}
