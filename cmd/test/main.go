package main

import (
	"encoding/json"
	"fmt"

	"github.com/dagger/cloak/dagger"
)

func main() {
	err := dagger.Client(func(ctx *dagger.Context) error {
		var output string
		var err error

		_, err = dagger.Do(ctx, `mutation{import(ref:"alpine"){name}}`)
		if err != nil {
			return err
		}

		/*
			output, err = dagger.Do(ctx, tools.IntrospectionQuery)
			if err != nil {
				return err
			}
			fmt.Printf("schema: %s\n", output)
		*/

		output, err = dagger.Do(ctx, `{alpine{build(pkgs:["gcc","python3"]){fs}}}`)
		if err != nil {
			return err
		}
		fmt.Printf("output: %s\n", output)

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			return err
		}
		fsBytes, err := json.Marshal(result["alpine"].(map[string]interface{})["build"].(map[string]interface{})["fs"])
		if err != nil {
			return err
		}

		output, err = dagger.Do(ctx, fmt.Sprintf(`mutation{evaluate(fs:%q)}`, string(fsBytes)))
		if err != nil {
			return err
		}
		var evalResult dagger.EvaluateResult
		if err := json.Unmarshal([]byte(output), &evalResult); err != nil {
			return err
		}
		if err := dagger.Shell(ctx, evalResult.Evaluate); err != nil {
			panic(err)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
}
