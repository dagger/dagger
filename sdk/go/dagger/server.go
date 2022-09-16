package dagger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// TODO: this just makes it easier to align with the default gql codgen code,
// in longer term we will rely less on that and don't have to be tied to this structure
type ArgsInput struct {
	Args         map[string]interface{}
	ParentResult interface{}
}

func Serve(ctx context.Context, resolvers map[string]func(context.Context, ArgsInput) (interface{}, error)) {
	ctx = WithUnixSocketAPIClient(ctx, "/dagger.sock")

	inputBytes, err := os.ReadFile("/inputs/dagger.json")
	if err != nil {
		writeErrorf(fmt.Errorf("unable to open request file: %w", err))
	}
	input := make(map[string]interface{}) // TODO: actual type
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		writeErrorf(fmt.Errorf("unable to parse request file: %w", err))
	}

	resolverName, ok := input["resolver"].(string)
	if !ok {
		writeErrorf(fmt.Errorf("unexpected resolver: %T %v", input["resolver"], input["resolver"]))
	}

	resolver, ok := resolvers[resolverName]
	if !ok {
		writeErrorf(fmt.Errorf("missing resolver for: %s", resolverName))
	}

	args, ok := input["args"].(map[string]interface{})
	if !ok {
		writeErrorf(fmt.Errorf("unexpected args: %T %v", input["args"], input["args"]))
	}

	argsInput := ArgsInput{Args: args}

	parent, ok := input["parent"]
	if ok {
		argsInput.ParentResult = parent
	}

	result, err := resolver(ctx, argsInput)
	if err != nil {
		writeErrorf(fmt.Errorf("unexpected error: %w", err))
	}
	if err := writeResult(result); err != nil {
		writeErrorf(fmt.Errorf("unable to write result: %w", err))
	}
}

func writeResult(result interface{}) error {
	output, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("unable to marshal response: %v", err)
	}

	if err := os.WriteFile("/outputs/dagger.json", output, 0600); err != nil {
		return fmt.Errorf("unable to write response file: %v", err)
	}
	return nil
}

func writeErrorf(err error) {
	fmt.Println(err.Error())
	os.Exit(1)
}
