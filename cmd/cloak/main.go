package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/dagger/cloak/cmd/dev/config"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
)

var (
	configFile     string
	queryFile      string
	operation      string
	queryVarsInput []string
	localDirsInput []string
	secretsInput   []string
)

func init() {
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "f", "./dagger.yaml", "config file")
	rootCmd.PersistentFlags().StringVarP(&queryFile, "query", "q", "", "query file")
	rootCmd.PersistentFlags().StringVarP(&operation, "op", "o", "", "operation to execute")
	rootCmd.PersistentFlags().StringSliceVarP(&queryVarsInput, "set", "s", []string{}, "query variable")
	rootCmd.PersistentFlags().StringSliceVarP(&localDirsInput, "local-dir", "l", []string{}, "local directory to import")
	rootCmd.PersistentFlags().StringSliceVarP(&secretsInput, "secret", "e", []string{}, "secret to import")
}

func getKVInput(kvs []string) map[string]string {
	m := make(map[string]string)
	fmt.Printf("kvs: %v\n", kvs)
	for _, kv := range kvs {
		split := strings.SplitN(kv, "=", 2)
		m[split[0]] = split[1]
	}
	return m
}

var rootCmd = &cobra.Command{
	Run: Run,
}

func Run(cmd *cobra.Command, args []string) {
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

	var inBytes []byte
	if queryFile != "" {
		inBytes, err = os.ReadFile(queryFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
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

func main() {
	rootCmd.Execute()
}
