package input

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"go.dagger.io/dagger/client"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/state"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var listCmd = &cobra.Command{
	Use:   "list [TARGET] [flags]",
	Short: "List for the inputs of an environment",
	Args:  cobra.MaximumNArgs(1),
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		lg := logger.New()
		ctx := lg.WithContext(cmd.Context())

		workspace := common.CurrentWorkspace(ctx)
		st := common.CurrentEnvironmentState(ctx, workspace)

		lg = lg.With().
			Str("environment", st.Name).
			Logger()

		c, err := client.New(ctx, "", false)
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to create client")
		}

		_, err = c.Do(ctx, st, func(ctx context.Context, env *environment.Environment, s solver.Solver) error {
			inputs := env.ScanInputs(ctx)

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "Input\tType\tValue\tSet by user\tDescription")

			for _, inp := range inputs {
				isConcrete := (inp.IsConcreteR() == nil)
				_, hasDefault := inp.Default()
				valStr := "-"
				if isConcrete {
					valStr, _ = inp.Cue().String()
				}
				if hasDefault {
					valStr = fmt.Sprintf("%s (default)", valStr)
				}

				if !viper.GetBool("all") {
					// skip input that is not overridable
					if !hasDefault && isConcrete {
						continue
					}
				}

				fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%s\n",
					inp.Path(),
					getType(inp),
					valStr,
					isUserSet(st, inp),
					getDocString(inp),
				)
			}

			w.Flush()
			return nil
		})

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to query environment")
		}

	},
}

func isUserSet(env *state.State, val *compiler.Value) bool {
	for key := range env.Inputs {
		if val.Path().String() == key {
			return true
		}
	}

	return false
}

func getType(val *compiler.Value) string {
	if val.HasAttr("artifact") {
		return "dagger.#Artifact"
	}
	if val.HasAttr("secret") {
		return "dagger.#Secret"
	}
	return val.Cue().IncompleteKind().String()
}

func getDocString(val *compiler.Value) string {
	docs := []string{}
	for _, c := range val.Cue().Doc() {
		docs = append(docs, strings.TrimSpace(c.Text()))
	}
	doc := strings.Join(docs, " ")

	lines := strings.Split(doc, "\n")

	// Strip out FIXME, TODO, and INTERNAL comments
	docs = []string{}
	for _, line := range lines {
		if strings.HasPrefix(line, "FIXME: ") ||
			strings.HasPrefix(line, "TODO: ") ||
			strings.HasPrefix(line, "INTERNAL: ") {
			continue
		}
		if len(line) == 0 {
			continue
		}
		docs = append(docs, line)
	}
	if len(docs) == 0 {
		return "-"
	}
	return strings.Join(docs, " ")
}

func init() {
	listCmd.Flags().BoolP("all", "a", false, "List all inputs (include non-overridable)")

	if err := viper.BindPFlags(listCmd.Flags()); err != nil {
		panic(err)
	}
}
