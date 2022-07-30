package dagger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

	object, ok := input["object"].(string)
	if !ok {
		writeErrorf(fmt.Errorf("unexpected object: %T %v", input["object"], input["object"]))
	}
	// capitalize first letter
	object = strings.ToUpper(string([]rune(object)[0])) + string([]rune(object)[1:])

	resolver, ok := resolvers[object]
	if !ok {
		writeErrorf(fmt.Errorf("missing result for: %s", object))
	}

	args, ok := input["args"].(map[string]interface{})
	if !ok {
		writeErrorf(fmt.Errorf("unexpected args: %T %v", input["args"], input["args"]))
	}

	result, err := resolver(ctx, ArgsInput{Args: args})
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

	if err := os.WriteFile("/outputs/dagger.json", output, 0644); err != nil {
		return fmt.Errorf("unable to write response file: %v", err)
	}
	return nil
}

func writeErrorf(err error) {
	fmt.Println(err.Error())
	os.Exit(1)
}
