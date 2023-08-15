package main

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/iancoleman/strcase"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/vito/progrock"
)

var artifactCmd = &cobra.Command{
	Use:                "artifact",
	DisableFlagParsing: true,
	Long:               `Query the status of your environment's artifacts.`,
	RunE:               loadEnvCmdWrapper(ListArtifacts),
}

func init() {
	rootCmd.AddCommand(artifactCmd)

	artifactCmd.PersistentFlags().StringVarP(&outputPath, "output", "o", "", "Write the artifact to this path.")
	artifactCmd.PersistentFlags().BoolVar(&doFocus, "focus", true, "Only show output for focused commands.")

	artifactCmd.AddCommand(
		&cobra.Command{
			Use:          "list",
			Long:         `List your environment's artifacts`,
			SilenceUsage: true,
			RunE:         loadEnvCmdWrapper(ListArtifacts),
		},
	)

	artifactCmd.AddCommand(
		&cobra.Command{
			Use:          "export [artifact name]",
			Long:         `Export an environment's artifact to a local path`,
			SilenceUsage: true,
			RunE:         loadEnvCmdWrapper(ExportArtifact),
		},
	)

}

func ListArtifacts(ctx context.Context, _ *client.Client, c *dagger.Client, loadedEnv *dagger.Environment, cmd *cobra.Command, dynamicCmdArgs []string) (err error) {
	rec := progrock.RecorderFromContext(ctx)
	vtx := rec.Vertex("cmd-list-artifacts", "list artifacts", progrock.Focused())
	defer func() { vtx.Done(err) }()

	envArtifacts, err := loadedEnv.Artifacts(ctx)
	if err != nil {
		return fmt.Errorf("failed to get environment artifacts: %w", err)
	}

	tw := tabwriter.NewWriter(vtx.Stdout(), 0, 0, 2, ' ', 0)

	if stdoutIsTTY {
		fmt.Fprintf(tw, "%s\t%s\n", termenv.String("artifact name").Bold(), termenv.String("description").Bold())
	}

	var printArtifact func(*dagger.EnvironmentArtifact) error
	printArtifact = func(artifact *dagger.EnvironmentArtifact) error {
		name, err := artifact.Name(ctx)
		if err != nil {
			return fmt.Errorf("failed to get artifact name: %w", err)
		}
		name = strcase.ToKebab(name)

		descr, err := artifact.Description(ctx)
		if err != nil {
			return fmt.Errorf("failed to get artifact description: %w", err)
		}
		fmt.Fprintf(tw, "%s\t%s\n", name, descr)
		return nil
	}

	for _, artifact := range envArtifacts {
		// TODO: this shouldn't be needed, there is a bug in our codegen for lists of objects. It should
		// internally be doing this so it's not needed explicitly
		artifactID, err := artifact.ID(ctx)
		if err != nil {
			return fmt.Errorf("failed to get artifact id: %w", err)
		}
		artifact = *c.EnvironmentArtifact(dagger.EnvironmentArtifactOpts{ID: artifactID})
		err = printArtifact(&artifact)
		if err != nil {
			return err
		}
	}

	return tw.Flush()
}

func ExportArtifact(ctx context.Context, _ *client.Client, c *dagger.Client, loadedEnv *dagger.Environment, cmd *cobra.Command, dynamicCmdArgs []string) (err error) {
	rec := progrock.RecorderFromContext(ctx)
	artifactName := dynamicCmdArgs[0]
	vtx := rec.Vertex("cmd-export-artifact", "export "+artifactName, progrock.Focused())
	defer func() { vtx.Done(err) }()

	cmd.Println("Exporting artifact", artifactName)

	_, err = loadedEnv.Artifact(strcase.ToLowerCamel(artifactName)).Export(ctx, outputPath)
	return err
}
