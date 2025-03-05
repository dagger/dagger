package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
)

const (
	llmPromptSymbol = "💬"
)

// Variables for llm command flags
var (
	llmModel string
)

func llmAddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&llmModel, "model", "m", "", "LLM model to use (e.g., 'claude-3-5-sonnet', 'gpt-4o')")
}

var llmCmd = &cobra.Command{
	Use:   "llm [options]",
	Short: "Run an interactive LLM interface",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SetContext(idtui.WithPrintTraceLink(cmd.Context(), true))
		return withEngine(cmd.Context(), client.Params{}, LLMLoop)
	},
	Hidden: true,
	Annotations: map[string]string{
		"experimental": "true",
	},
}

func LLMLoop(ctx context.Context, engineClient *client.Client) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	dag := engineClient.Dagger()

	shellHandler := &shellCallHandler{
		dag:   dag,
		debug: debug,
	}

	if err := shellHandler.Initialize(ctx); err != nil {
		return err
	}

	// give ourselves a blank slate by zooming into a passthrough span
	shellCtx, shellSpan := Tracer().Start(ctx, "llm", telemetry.Passthrough())
	defer telemetry.End(shellSpan, func() error { return nil })
	Frontend.SetPrimary(dagui.SpanID{SpanID: shellSpan.SpanContext().SpanID()})

	// TODO: initialize LLM with current module, matching shell behavior?

	llm := dag.Llm()

	mu := &sync.Mutex{}
	Frontend.Shell(shellCtx,
		func(ctx context.Context, line string) (rerr error) {
			mu.Lock()
			defer mu.Unlock()

			if line == "exit" {
				cancel()
				return nil
			}

			ctx, span := Tracer().Start(ctx, line)
			defer telemetry.End(span, func() error { return rerr })
			stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
			defer stdio.Close()

			// if line starts with "/with", run shell and change to result
			if strings.HasPrefix(line, "/with ") {
				shell := strings.TrimSpace(strings.TrimPrefix(line, "/with "))
				resp, typeDef, err := shellHandler.Eval(ctx, shell)
				if err != nil {
					return err
				}
				if typeDef.AsFunctionProvider() != nil {
					llmId, err := llm.ID(ctx)
					if err != nil {
						return err
					}
					if err := dag.QueryBuilder().
						Select("loadLlmFromID").
						Arg("id", llmId).
						Select(fmt.Sprintf("with%s", typeDef.Name())).
						Arg("value", resp).
						Select("id").
						Bind(&llmId).
						Execute(ctx); err != nil {
						return err
					}
					llm = dag.LoadLlmFromID(llmId)
				}
				return nil
			}

			if strings.TrimSpace(line) == "" {
				return nil
			}

			prompted, err := llm.WithPrompt(line).Sync(ctx)
			if err != nil {
				return err
			}

			llm = prompted

			return nil
		},
		nil,
		func(out idtui.TermOutput, fg termenv.Color) string {
			return out.String(idtui.PromptSymbol + " ").Foreground(fg).String()
		},
	)

	return nil
}
