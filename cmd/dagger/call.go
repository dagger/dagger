package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/spf13/cobra"
)

var callCmd = &FuncCommand{
	Name:  "call",
	Short: "Call a function, or a pipeline of functions, and print the result",
	Long:  "Call a function, or a pipeline of functions, and print the result",
	OnSelectObjectLeaf: func(c *FuncCommand, name string) error {
		switch name {
		case Service:
			c.Select("id")
		case Container:
			c.Select("id")
		case Directory:
			c.Select("id")
		case File:
			c.Select("id")
		case Secret:
			c.Select("id")
		}
		return nil
	},
	AfterResponse: func(c *FuncCommand, cmd *cobra.Command, _ *modTypeDef, response any) error {
		return prettyPrint(c, cmd, response)
	},
}

// Extract the type from an ID string, and return it.
// Example return value: "core.Directory"
func matchID(ID string) string {
	if !strings.HasPrefix(ID, "core.") {
		return ""
	}
	parts := strings.SplitN(ID, ":", 2)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func prettyPrint(c *FuncCommand, cmd *cobra.Command, response any) error {
	ctx := cmd.Context()
	if list, ok := (response).([]any); ok {
		cmd.Printf("[%d objects]:\n", len(list))
		for i, v := range list {
			cmd.Printf("%d/%d\n", i+1, len(list))
			prettyPrint(c, cmd, v)
		}
		return nil
	}
	dag := c.c.Dagger()

	if str, ok := (response).(string); ok {
		// Look for IDs
		switch matchID(str) {
		case "core.Directory":
			return printDirectory(ctx, dag, str)
		case "core.Service":
			return nil
		case "core.Secret":
			return nil
		case "core.File":
			return nil
		case "core.Container":
			return printContainer(ctx, dag, str)
		}
		// Default to printing the string raw
		// FIXME: print raw, or add newline?
		fmt.Println(str)
		return nil
	}
	fmt.Printf("%v\n", response)
	return nil
}

func printDirectory(ctx context.Context, dag *dagger.Client, ID string) error {
	dir := dag.LoadDirectoryFromID(dagger.DirectoryID(ID))
	entries, err := dir.Entries(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("%v\n", entries)
	return nil
}

// Information to print about a container
// FIXME: query all of it with a single GraphQL query :)
type containerInfo struct {
	Type         string          `json:"type"`
	Entrypoint   []string        `json:"entrypoint"`
	DefaultArgs  []string        `json:"defaultArgs"`
	Platform     dagger.Platform `json:"platform"`
	EnvVariables []envVariable   `json:"envVariables"`
	Workdir      string          `json:"workdir,omitempty"`
}

type envVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Load information about a container by querying the API
// FIXME: make this one simple graphql query
func queryContainer(ctx context.Context, dag *dagger.Client, ID string) (*containerInfo, error) {
	info := new(containerInfo)
	info.Type = "container"
	ctr := dag.LoadContainerFromID(dagger.ContainerID(ID))
	// Entrypoint
	entrypoint, err := ctr.Entrypoint(ctx)
	if err != nil {
		return nil, err
	}
	info.Entrypoint = entrypoint
	// Default args
	defaultArgs, err := ctr.DefaultArgs(ctx)
	if err != nil {
		return nil, err
	}
	info.DefaultArgs = defaultArgs
	// Platform
	platform, err := ctr.Platform(ctx)
	if err != nil {
		return nil, err
	}
	info.Platform = platform
	// Environment variables
	envVariables, err := ctr.EnvVariables(ctx)
	if err != nil {
		return nil, err
	}
	info.EnvVariables = make([]envVariable, 0, len(envVariables))
	for _, kv := range envVariables {
		k, err := kv.Name(ctx)
		if err != nil {
			return nil, err
		}
		v, err := kv.Value(ctx)
		if err != nil {
			return nil, err
		}
		info.EnvVariables = append(info.EnvVariables, envVariable{Name: k, Value: v})
	}
	// Workdir
	workdir, err := ctr.Workdir(ctx)
	if err != nil {
		return nil, err
	}
	info.Workdir = workdir
	return info, nil
}

func printContainer(ctx context.Context, dag *dagger.Client, ID string) error {
	info, err := queryContainer(ctx, dag, ID)
	if err != nil {
		return err
	}
	prettyJSON, err := json.MarshalIndent(info, "", " ")
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", prettyJSON)
	return nil
}
