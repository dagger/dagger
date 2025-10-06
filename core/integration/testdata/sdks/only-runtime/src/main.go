package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/dag"
)

func main() {
	ctx := context.Background()
	defer dag.Close()

	name, err := dag.CurrentModule().Name(ctx)
	if err != nil {
		fmt.Println(fmt.Errorf("failed to get module name: %w", err))

		os.Exit(2)
	}

	formattedName := strings.ToUpper(string(name[0])) + name[1:]

	if err := dispatch(ctx, formattedName); err != nil {
		fmt.Println(err)

		os.Exit(2)
	}

}

func dispatch(ctx context.Context, modName string) error {
	fnCall := dag.CurrentFunctionCall()

	parentName, err := fnCall.ParentName(ctx)
	if err != nil {
		return fmt.Errorf("failed to get parent name: %w", err)
	}

	var result any

	if parentName == "" {
		mod := dag.Module()

		mainObj := dag.TypeDef().WithObject(modName).
			WithFunction(dag.Function("HelloWorld", dag.TypeDef().WithKind(dagger.TypeDefKindStringKind)))

		mod = mod.WithObject(mainObj)

		result = mod
	} else {
		result = "Hello world"
	}

	resultBytes, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	if err := fnCall.ReturnValue(ctx, dagger.JSON(resultBytes)); err != nil {
		return fmt.Errorf("failed to set return value: %w", err)
	}

	return nil
}
