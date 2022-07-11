package main

import (
	"os"

	"github.com/dagger/cloak/dagger"
)

func main() {
	err := dagger.Client(func(ctx *dagger.Context) error {
		token, err := os.ReadFile("/home/sipsma/netflify.token") // TODO:
		if err != nil {
			return err
		}
		dagger.AddSecret(ctx, "token", string(token))

		rawOutput, err := dagger.Do(ctx, "localhost:5555/dagger:netlify", "deploy", map[string]interface{}{
			"Site":  dagger.ToString("foobar"),
			"Token": dagger.SecretID("token"),
		})
		if err != nil {
			return err
		}
		output := rawOutput.GetField("fs").FS()
		output.Evaluate(ctx)

		/*
			root := output.Root()
			root.Evaluate(ctx)

				bytes, err := json.Marshal(output)
				if err != nil {
					panic(err)
				}

				fmt.Printf("%s\n", string(bytes))

		*/
		if err := dagger.Shell(ctx, output); err != nil {
			panic(err)
		}

		return nil
	})
	if err != nil {
		panic(err)
	}
}
