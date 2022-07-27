package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/cmd/cloak/config"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh/terminal"
)

var queryCmd = &cobra.Command{
	Use: "query",
	Run: Query,
}

func Query(cmd *cobra.Command, args []string) {
	cfg, err := config.ParseFile(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	vars := getKVInput(queryVarsInput)
	localDirs := getKVInput(localDirsInput)
	secrets := getKVInput(secretsInput)

	for name, dir := range cfg.LocalDirs() {
		localDirs[name] = dir
	}

	startOpts := &engine.StartOpts{
		LocalDirs: localDirs,
		Secrets:   secrets,
	}

	// Use the provided query file if specified
	// Otherwise, if stdin is a pipe or other non-tty thing, read from it.
	// Finally, default to reading from operations.graphql next to dagger.yaml
	isTerminal := terminal.IsTerminal(int(os.Stdin.Fd()))
	if queryFile == "" && isTerminal {
		queryFile = filepath.Join(filepath.Dir(configFile), "operations.graphql")
	}

	var inBytes []byte
	if queryFile != "" {
		// use the provided query file if specified
		inBytes, err = os.ReadFile(queryFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		// otherwise, if stdin is a pipe or other non-tty thing, read from it
		inBytes, err = io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	var result []byte
	err = engine.Start(context.Background(), startOpts,
		func(ctx context.Context, localDirs map[string]dagger.FS, secrets map[string]string) (*dagger.FS, error) {
			if err := cfg.Import(ctx, localDirs); err != nil {
				return nil, err
			}

			for name, fs := range localDirs {
				vars[name] = string(fs)
			}
			for name, id := range secrets {
				vars[name] = id
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
					OpName:    operation,
				},
				resp,
			)
			if err != nil {
				return nil, err
			}
			if len(resp.Errors) > 0 {
				return nil, resp.Errors
			}

			result, err = json.MarshalIndent(res, "", "    ")
			if err != nil {
				return nil, err
			}
			return nil, nil
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s\n", result)
}

func getKVInput(kvs []string) map[string]string {
	m := make(map[string]string)
	for _, kv := range kvs {
		split := strings.SplitN(kv, "=", 2)
		m[split[0]] = split[1]
	}
	return m
}
