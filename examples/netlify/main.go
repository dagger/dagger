package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/dagger/cloak/dagger"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:                "dagger",
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		d := dagger.New()

		d.Action("deploy", func(ctx *dagger.Context, input []byte) ([]byte, error) {
			cmd := exec.Command("bash", "-e", "-x", "-o", "pipefail", "dagger.bash")
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return nil, err
			}

			// TODO: highly silly, we read the bytes from here, return them, then the caller of this func writes them back to the file... maybe we should just always write the file here in both bash and go runtimes. Or perhaps having the action implementation write to the file is a bad idea and we should just parse it from stdout? That can be tricky too though...
			// TODO: similarly, we ignore the input bytes here and just have the action implementation read them from the file directly...
			return os.ReadFile("/outputs/dagger.json")
		})

		if err := d.Serve(); err != nil {
			panic(err)
		}
	},
}

var inputCmd = &cobra.Command{
	Use:                "input",
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		bytes, err := os.ReadFile("/inputs/dagger.json")
		if err != nil {
			panic(err)
		}
		flags := getArbitraryArgs(args)
		if len(flags) > 1 {
			panic("only one flag is allowed")
		}
		if len(flags) == 1 {
			var field string
			for k := range flags {
				field = k
			}
			rawInput := make(map[string]interface{})
			if err := json.Unmarshal(bytes, &rawInput); err != nil {
				panic(err)
			}
			bytes, err = json.Marshal(rawInput[field])
			if err != nil {
				panic(err)
			}
		}
		_, err = os.Stdout.Write(bytes)
		if err != nil {
			panic(err)
		}
	},
}

var outputCmd = &cobra.Command{
	Use:                "output",
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		bytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			panic(err)
		}
		result := &dagger.Result{}
		if err := json.Unmarshal(bytes, result); err != nil {
			panic(err)
		}

		flags := getArbitraryArgs(args)
		if len(flags) > 1 || len(flags) == 0 {
			// TODO:
			panic("exactly one flag must be provided")
		}
		var field string
		for k := range flags {
			field = k
		}

		bytes, err = json.Marshal(map[string]interface{}{field: result})
		if err != nil {
			panic(err)
		}
		err = os.WriteFile("/outputs/dagger.json", bytes, 0644)
		if err != nil {
			panic(err)
		}
	},
}

var readStringCmd = &cobra.Command{
	Use:                "read-string",
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		err := dagger.RunWithContext(func(ctx *dagger.Context) error {
			bytes, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			s := &dagger.String{}
			err = s.UnmarshalJSON(bytes)
			if err != nil {
				return err
			}
			val := s.Evaluate(ctx)
			_, err = os.Stdout.Write([]byte(val))
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			panic(err)
		}
	},
}

var readSecretCmd = &cobra.Command{
	Use:                "read-secret",
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		err := dagger.RunWithContext(func(ctx *dagger.Context) error {
			bytes, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}

			s := &dagger.Secret{}
			err = s.UnmarshalJSON(bytes)
			if err != nil {
				return err
			}
			secretID := s.Evaluate(ctx)
			val, err := dagger.ReadSecret(ctx, secretID)
			if err != nil {
				return err
			}

			_, err = os.Stdout.Write([]byte(val))
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			panic(err)
		}
	},
}

var readFileCmd = &cobra.Command{
	Use:                "read-file",
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		err := dagger.RunWithContext(func(ctx *dagger.Context) error {
			bytes, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			fs := &dagger.FS{}
			err = fs.UnmarshalJSON(bytes)
			if err != nil {
				return err
			}
			readBytes, err := fs.ReadFile(ctx, args[0])
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(readBytes)
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			panic(err)
		}
	},
}

var getFieldCmd = &cobra.Command{
	Use:                "get-field",
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		bytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			panic(err)
		}
		r := &dagger.Result{}
		err = r.UnmarshalJSON(bytes)
		if err != nil {
			panic(err)
		}
		bytes, err = r.GetField(args[0]).MarshalJSON()
		if err != nil {
			panic(err)
		}
		_, err = os.Stdout.Write(bytes)
		if err != nil {
			panic(err)
		}
	},
}

var doCmd = &cobra.Command{
	Use:                "do",
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		err := dagger.RunWithContext(func(ctx *dagger.Context) error {
			pkg := args[0]
			action := args[1]

			// TODO: this can't currently handle arbitrary nesting of structs... maybe it shouldn't?
			// TODO: Given the above and other hacks noted below, maybe cli flags are a best-effort convenience but we also support a way of easily passing in arbitrary json (or whatever format) for complex cases
			parsed := map[string]interface{}{}
			for k, vs := range getArbitraryArgs(args[2:]) {
				// TODO: handle map keys...
				if len(vs) == 0 {
					parsed[k] = true
				} else if len(vs) == 1 {
					// TODO: no way of determining whether this should be a json struct or just a string, other than trying to marshal as json... :-(
					var asJson map[string]interface{}
					if err := json.Unmarshal([]byte(vs[0]), &asJson); err == nil {
						parsed[k] = asJson
					} else {
						parsed[k] = dagger.ToString(vs[0])
					}
				} else {
					parsed[k] = dagger.ToStrings(vs...)
				}
			}

			res, err := dagger.Do(ctx, pkg, action, parsed)
			if err != nil {
				return err
			}
			bytes, err := res.MarshalJSON()
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(bytes)
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			panic(err)
		}
	},
}

// TODO: is cobra even providing any benefit?
func getArbitraryArgs(args []string) map[string][]string {
	flags := make(map[string][]string)
	var curFlag string
	for _, arg := range args {
		if strings.HasPrefix(arg, "--") {
			curFlag = arg[2:]
			flags[curFlag] = nil
		} else {
			flags[curFlag] = append(flags[curFlag], arg)
		}
	}
	return flags
}

func init() {
	rootCmd.AddCommand(inputCmd)
	rootCmd.AddCommand(outputCmd)
	rootCmd.AddCommand(getFieldCmd)
	rootCmd.AddCommand(readStringCmd)
	rootCmd.AddCommand(readSecretCmd)
	rootCmd.AddCommand(readFileCmd)
	rootCmd.AddCommand(doCmd)
}

func main() {
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		panic(err)
	}
}
