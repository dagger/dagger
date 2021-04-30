package input

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"dagger.io/go/cmd/dagger/cmd/common"
	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger"

	"cuelang.org/go/cue/ast"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var listCmd = &cobra.Command{
	Use:   "list [TARGET] [flags]",
	Short: "List for the inputs of a deployment",
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

		store, err := dagger.DefaultStore()
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to load store")
		}

		debug := viper.GetBool("debug-inputs")

		deployment := common.GetCurrentDeploymentState(ctx, store)

		// possibly print any persisted inputs
		if (viper.GetBool("show-current") || viper.GetBool("show-all")) && len(deployment.Inputs) > 0 {
			fmt.Println("Saved Inputs:")
			for _, input := range deployment.Inputs {
				// Todo, how to pull apart an input to print relevant information
				fmt.Printf("%s: %v\n", input.Key, input.Value)
			}
			// add some space
			fmt.Println()
		}
		if viper.GetBool("show-current") {
			return
		}

		lg = lg.With().
			Str("deploymentName", deployment.Name).
			Str("deploymentId", deployment.ID).
			Logger()

		c, err := dagger.NewClient(ctx, "", false)
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to create client")
		}

		_, err = c.Do(ctx, deployment, func(lCtx context.Context, lDeploy *dagger.Deployment, lSolver dagger.Solver) error {
			inputs, err := lDeploy.ScanInputs()
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "Plan Inputs:")
			fmt.Fprintln(w, "Path\tFrom\tType")

			for _, val := range inputs {
				inst, path := val.Reference()
				l, _ := val.Label()
				op, vals := val.Expr()
				S := val.Source()

				if debug {
					fmt.Println("======================")
					fmt.Println(l, val)
					fmt.Printf("%#+v\n%v %v\n", inst, path, op)
					fmt.Printf("%#+v\n", S)
				}

				// Debug, print expression vals
				if debug {
					for i, ve := range vals {
						s := ve.Source()
						fmt.Printf("%d: %#+v\n", i, s)
						inst, path := ve.Reference()
						fmt.Println(ve, inst, path)
					}
				}

				// possibly filter optional from output (includes values with defaults)
				if !viper.GetBool("show-optional") && !viper.GetBool("show-all") {
					// exclude if default
					_, has := val.Default()
					if has {
						continue
					}
					// exclude if field is optional
					switch t := S.(type) {
					case *ast.Field:
						// optional member is "non-nil"
						if t.Optional.File() != nil {
							continue
						}
					}
				}

				var isDaggerSpecial = false
				if inst != nil && inst.ImportPath == "dagger.io/dagger" && len(path) > 0 {
					p := path[0]
					if p == "#Secret" || p == "#Artifact" {
						isDaggerSpecial = true
					}
				}

				// search for and filter references
				if !isDaggerSpecial && !viper.GetBool("keep-references") && !viper.GetBool("show-all") {
					foundRef := false
					for _, ve := range vals {

						if !viper.GetBool("no-deep-references") {
							inst, path :=  ve.Reference()
							if inst != nil {
								if debug {
									fmt.Println("Hiding:", path)
								}
								// continue
								foundRef = true
							}
						}

						// check if there is an Ident
						if viper.GetBool("elem-identifiers") {
							s := ve.Source()
							switch s.(type) {
							case *ast.Ident:
								foundRef = true
							}
						}
					}
					if foundRef {
						continue
					}
				}

				if debug {
					fmt.Println("======================")
					fmt.Println()
				}

				// Construct output as a tab-table
				// get path / pkg import (if available)
				// inst, _ := val.Reference()
				pkg := "(plan)"
				if inst != nil {
					pkg = inst.ImportPath
				}

				fmt.Fprintf(w, "%s\t%s\t%v\n", val.Path(), pkg, val)

			}
			// ensure we flush the output buf
			w.Flush()

			return nil
		})

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to query deployment")
		}

	},
}

func init() {
	// some are temporary, tbd
	listCmd.Flags().BoolP("show-all", "A", false, "Show all inputs")
	listCmd.Flags().BoolP("show-optional", "O", false, "Include optional inputs")
	listCmd.Flags().BoolP("show-current", "C", false, "Print existing persisted inputs")
	listCmd.Flags().BoolP("keep-references", "K", false, "Do not eliminate references")
	listCmd.Flags().BoolP("no-deep-references", "R", false, "Do not try to recurse and find deep references")
	listCmd.Flags().BoolP("elem-identifiers", "I", false, "Try to recurse and remove identifiers")
	listCmd.Flags().BoolP("debug-inputs", "D", false, "Show debug printing")

	if err := viper.BindPFlags(listCmd.Flags()); err != nil {
		panic(err)
	}
}
