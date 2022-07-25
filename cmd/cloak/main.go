package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Khan/genqlient/graphql"

	"github.com/dagger/cloak/cmd/web/config"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
)

type kvInput map[string]string

func (i kvInput) String() string {
	return fmt.Sprintf("%+v", map[string]string(i))
}

func (i kvInput) Set(value string) error {
	kvs := strings.Split(value, ",")
	for _, kv := range kvs {
		split := strings.SplitN(kv, "=", 2)
		i[split[0]] = split[1]
	}
	return nil
}

func main() {
	localDirs := kvInput{}
	flag.Var(&localDirs, "local-dirs", "local directories to import")

	secrets := kvInput{}
	flag.Var(&secrets, "secrets", "secrets to import")

	var configFile string
	flag.StringVar(&configFile, "f", "./dagger.yaml", "config file")

	flag.Parse()

	cfg, err := config.ParseFile(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	for name, dir := range cfg.LocalDirs() {
		localDirs[name] = dir
	}

	startOpts := &engine.StartOpts{
		LocalDirs: localDirs,
		Secrets:   secrets,
	}

	err = engine.Start(context.Background(), startOpts,
		func(ctx context.Context, localDirs map[string]dagger.FS, secrets map[string]string) (*dagger.FS, error) {
			if err := cfg.Import(ctx, localDirs); err != nil {
				return nil, err
			}

			inBytes, err := io.ReadAll(os.Stdin)
			if err != nil {
				return nil, err
			}

			vars := map[string]any{}
			for name, fs := range localDirs {
				// TODO: need better naming convention
				vars["local_"+name] = fs
			}
			for name, fs := range secrets {
				// TODO: need better naming convention
				vars["secret_"+name] = fs
			}

			cl, err := dagger.Client(ctx)
			if err != nil {
				return nil, err
			}
			res := make(map[string]interface{})
			resp := &graphql.Response{Data: &res}
			err = cl.MakeRequest(ctx,
				&graphql.Request{
					Query:     string(inBytes),
					Variables: vars,
				},
				resp,
			)
			if err != nil {
				return nil, err
			}
			if len(resp.Errors) > 0 {
				return nil, resp.Errors
			}
			return nil, nil
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
