package main

import (
	"encoding/json"
	"fmt"

	"github.com/dagger/cloak/dagger"
)

func main() {
	err := dagger.Client(func(ctx *dagger.Context) error {
		output, err := dagger.Do(ctx, `{alpine{build(pkgs:["curl","bash"]){fs}}}`)
		if err != nil {
			return err
		}
		var result dagger.AlpineResult
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			return err
		}
		fsBytes, err := json.Marshal(result.Alpine.Build.FS)
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
