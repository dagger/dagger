package main

import (
	"context"
	"fmt"
	"os"
	"time"

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

	// set secret
	authKeySecret := client.SetSecret("tailscaleAuthkey", "TS-KEY")

	// create Tailscale service container
	tailscale, err := client.Container().
		From("tailscale/tailscale:stable").
		WithSecretVariable("TAILSCALE_AUTHKEY", authKeySecret).
		WithExec([]string{"/bin/sh", "-c", "tailscaled --tun=userspace-networking --socks5-server=0.0.0.0:1055 --outbound-http-proxy-listen=0.0.0.0:1055 & tailscale up --authkey $TAILSCALE_AUTHKEY &"}).
		WithExposedPort(1055)

	// access Tailscale network
	http, err := client.Container().
		From("alpine:3.17")
		WithExec([]string{"apk", "add", "curl"}).
		WithServiceBinding("tailscale", tailscale).
		WithEnvVariable("ALL_PROXY", "socks5://tailscale:1055/").
		WithExec([]string{"curl https://my.url.only.accessible.on.tailscale.network.com"})



	if err != nil {
		panic(err)
	}
	fmt.Println(output)
}
