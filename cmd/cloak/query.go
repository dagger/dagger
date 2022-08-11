package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/cmd/cloak/config"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh/terminal"
	"golang.org/x/sync/errgroup"
)

var queryCmd = &cobra.Command{
	Use: "query",
	Run: Query,
}

func Query(cmd *cobra.Command, args []string) {
	ctx := context.Background()
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

	startOpts := &engine.Config{
		LocalDirs: localDirs,
	}

	// Use the provided query file if specified
	// Otherwise, if stdin is a pipe or other non-tty thing, read from it.
	// Finally, default to reading from operations.graphql next to cloak.yaml
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
	err = engine.Start(ctx, startOpts, func(ctx context.Context) error {
		cl, err := dagger.Client(ctx)
		if err != nil {
			return err
		}

		localDirMapping, err := loadLocalDirs(ctx, cl, localDirs)
		if err != nil {
			return err
		}
		for name, id := range localDirMapping {
			vars[name] = string(id)
		}

		secretMapping, err := loadSecrets(ctx, cl, secrets)
		if err != nil {
			return err
		}
		for name, id := range secretMapping {
			vars[name] = string(id)
		}

		if err := cfg.LoadExtensions(ctx, localDirMapping); err != nil {
			return err
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
			return err
		}
		if len(resp.Errors) > 0 {
			return resp.Errors
		}

		result, err = json.MarshalIndent(res, "", "    ")
		if err != nil {
			return err
		}
		return nil
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

func loadSecrets(ctx context.Context, cl graphql.Client, secrets map[string]string) (map[string]dagger.SecretID, error) {
	var eg errgroup.Group
	var l sync.Mutex

	mapping := map[string]dagger.SecretID{}
	for name, value := range secrets {
		name := name
		value := value

		eg.Go(func() error {
			res := struct {
				Core struct {
					AddSecret dagger.SecretID
				}
			}{}
			resp := &graphql.Response{Data: &res}

			err := cl.MakeRequest(ctx,
				&graphql.Request{
					Query: `
						query AddSecret($plaintext: String!) {
							core {
								addSecret(plaintext: $plaintext)
							}
						}
				`,
					Variables: map[string]any{
						"plaintext": value,
					},
				},
				resp,
			)
			if err != nil {
				return err
			}
			l.Lock()
			mapping[name] = res.Core.AddSecret
			l.Unlock()

			return nil
		})
	}

	return mapping, eg.Wait()
}

func loadLocalDirs(ctx context.Context, cl graphql.Client, localDirs map[string]string) (map[string]dagger.FSID, error) {
	var eg errgroup.Group
	var l sync.Mutex

	mapping := map[string]dagger.FSID{}
	for localID := range localDirs {
		localID := localID
		eg.Go(func() error {
			res := struct {
				Core struct {
					Clientdir struct {
						ID dagger.FSID
					}
				}
			}{}
			resp := &graphql.Response{Data: &res}

			err := cl.MakeRequest(ctx,
				&graphql.Request{
					Query: `
						query ClientDir($id: String!) {
							core {
								clientdir(id: $id) {
									id
								}
							}
						}
					`,
					Variables: map[string]any{
						"id": localID,
					},
				},
				resp,
			)
			if err != nil {
				return err
			}

			l.Lock()
			mapping[localID] = res.Core.Clientdir.ID
			l.Unlock()

			return nil
		})
	}

	return mapping, eg.Wait()
}
