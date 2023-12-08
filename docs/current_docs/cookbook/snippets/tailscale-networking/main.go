package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	// create Dagger client
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	tailscaleAuthKey := os.Getenv("TAILSCALE_AUTHKEY")
	if tailscaleAuthKey == "" {
		panic("TAILSCALE_AUTHKEY env var must be set")
	}
	// set secret
	authKeySecret := client.SetSecret("tailscaleAuthKey", tailscaleAuthKey)

	tailscaleServiceURL := os.Getenv("TAILSCALE_SERVICE_URL")
	if tailscaleServiceURL == "" {
		panic("TAILSCALE_SERVICE_URL env var must be set")
	}

	// create Tailscale service container
	tailscale := client.Container().
		From("tailscale/tailscale:stable").
		WithSecretVariable("TAILSCALE_AUTHKEY", authKeySecret).
		WithExec([]string{"/bin/sh", "-c", "tailscaled --tun=userspace-networking --socks5-server=0.0.0.0:1055 --outbound-http-proxy-listen=0.0.0.0:1055 & tailscale up --authkey $TAILSCALE_AUTHKEY &"}).
		WithExposedPort(1055)

	// access Tailscale network
	out, err := client.Container().
		From("alpine:3.17").
		WithExec([]string{"apk", "add", "curl"}).
		WithServiceBinding("tailscale", tailscale).
		WithEnvVariable("ALL_PROXY", "socks5://tailscale:1055/").
		WithExec([]string{"curl", "--silent", "--verbose", tailscaleServiceURL}).
		Sync(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(out)
}
