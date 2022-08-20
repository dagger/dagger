package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
)

const (
	projectContextLocalName = ".projectContext"
)

var doCmd = &cobra.Command{
	Use:  "do",
	Run:  Do,
	Args: cobra.MaximumNArgs(1), // operation can be specified
}

func Do(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	var operation string
	if len(args) > 0 {
		operation = args[0]
	}

	vars := getKVInput(queryVarsInput)
	secrets := getKVInput(secretsInput)

	localDirs := getKVInput(localDirsInput)
	localDirs[projectContextLocalName] = projectContext

	startOpts := &engine.Config{
		LocalDirs: localDirs,
	}

	// Use the provided query file if specified
	// Otherwise, if stdin is a pipe or other non-tty thing, read from it.
	// Finally, default to the operations returned by the loadExtension query
	var operations string
	if queryFile != "" {
		inBytes, err := os.ReadFile(queryFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		operations = string(inBytes)
	} else if !term.IsTerminal(int(os.Stdin.Fd())) {
		inBytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		operations = string(inBytes)
	}

	var result []byte
	err := engine.Start(ctx, startOpts, func(ctx context.Context) error {
		cl, err := dagger.Client(ctx)
		if err != nil {
			return err
		}

		localDirMapping, err := loadLocalDirs(ctx, cl, localDirs)
		if err != nil {
			return err
		}
		for name, id := range localDirMapping {
			if name == projectContextLocalName {
				continue
			}
			vars[name] = string(id)
		}

		secretMapping, err := loadSecrets(ctx, cl, secrets)
		if err != nil {
			return err
		}
		for name, id := range secretMapping {
			vars[name] = string(id)
		}

		defaultOperations, err := installProject(ctx, cl, localDirMapping[projectContextLocalName])
		if err != nil {
			return err
		}
		if operations == "" {
			operations = defaultOperations
		}

		res := make(map[string]interface{})
		resp := &graphql.Response{Data: &res}
		err = cl.MakeRequest(ctx,
			&graphql.Request{
				Query:     operations,
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

func installProject(ctx context.Context, cl graphql.Client, contextFS dagger.FSID) (operations string, rerr error) {
	res := struct {
		Core struct {
			Filesystem struct {
				LoadExtension struct {
					Operations string
				}
			}
		}
	}{}
	resp := &graphql.Response{Data: &res}

	err := cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			query LoadExtension($fs: FSID!, $configPath: String!) {
				core {
					filesystem(id: $fs) {
						loadExtension(configPath: $configPath) {
							install
							operations
						}
					}
				}
			}`,
			Variables: map[string]any{
				"fs":         contextFS,
				"configPath": projectFile,
			},
		},
		resp,
	)
	if err != nil {
		return "", err
	}

	return res.Core.Filesystem.LoadExtension.Operations, nil
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
