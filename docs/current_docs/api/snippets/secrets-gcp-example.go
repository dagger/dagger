// Example of using GCP Secret Manager with Dagger
package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// Initialize Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// Example 1: Using simple secret name (requires GCP_PROJECT_ID env var)
	secret1 := client.SetSecret("api-key", "gcp://my-api-key")
	
	// Example 2: Using secret with specific version
	secret2 := client.SetSecret("api-key-v2", "gcp://my-api-key/versions/2")
	
	// Example 3: Using full resource name
	secret3 := client.SetSecret("api-key-full", "gcp://projects/my-project/secrets/my-api-key")
	
	// Example 4: Using secret with TTL for caching
	secret4 := client.SetSecret("api-key-cached", "gcp://my-api-key?ttl=5m")

	// Use the secret in a container
	container := client.Container().
		From("alpine:latest").
		WithSecretVariable("API_KEY", secret1).
		WithExec([]string{"sh", "-c", "echo Secret loaded successfully"})

	// Execute the container
	_, err = container.Sync(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println("Successfully loaded secret from GCP Secret Manager!")
}