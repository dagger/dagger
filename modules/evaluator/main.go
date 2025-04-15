package main

import (
	"context"
	"dagger/botsbuildingbots/internal/dagger"
	"dagger/botsbuildingbots/internal/telemetry"
	_ "embed"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"

	"github.com/sourcegraph/conc/pool"
)

type Evaluator struct {
	Docs          *dagger.File
	InitialPrompt *dagger.File
	WriterModel   string
}

// Require at least 2/3 or 6/10
const MinSuccessRate = 0.6

func New(
	// The documentation for the tool calling scheme to generate a prompt for.
	// +optional
	docs *dagger.File,
	// An initial system prompt to evaluate and use as a starting point.
	// +optional
	initialPrompt *dagger.File,
	// Model to use for the evaluator agent.
	// +optional
	model string,
) *Evaluator {
	return &Evaluator{
		Docs:          docs,
		InitialPrompt: initialPrompt,
		WriterModel:   model,
	}
}

func (m *Evaluator) llm() *dagger.LLM {
	return dag.LLM(dagger.LLMOpts{Model: m.WriterModel})
}

func (m *Evaluator) env() *dagger.Env {
	env := dag.Env().
		WithWorkspaceInput("workspace", m.work(), "A space for you to work in.")
	if m.Docs != nil {
		env = env.WithFileInput("docs", m.Docs,
			"The documentation the model is meant to adhere to.")
	}
	if m.InitialPrompt != nil {
		env = env.WithFileInput("initialSystemPrompt", m.InitialPrompt,
			"An initial system prompt to evaluate and improve.")
	}
	return env
}

type ModelResult struct {
	ModelName   string
	EvalReports []EvalResult
}

type EvalResult struct {
	Name          string
	Error         string
	Report        string
	SuccessRate   float64
	TotalAttempts int
}

func (result *EvalResult) Check() error {
	if result.Error != "" {
		return errors.New(result.Error)
	}
	return nil
}

func (result *ModelResult) Check() error {
	var errs error
	for _, eval := range result.EvalReports {
		if err := eval.Check(); err != nil {
			errs = errors.Join(
				errs,
				fmt.Errorf("%s: %w", eval.Name, err),
			)
		}
	}
	return errs
}

type EvalsAcrossModels struct {
	ModelResults []ModelResult
}

func (result *EvalsAcrossModels) CSV() (string, error) {
	buf := new(strings.Builder)
	csv := csv.NewWriter(buf)
	csv.Write([]string{"model", "eval", "success_rate", "total_attempts"})
	var errs error
	for _, result := range result.ModelResults {
		for _, eval := range result.EvalReports {
			csv.Write([]string{
				result.ModelName,
				eval.Name,
				fmt.Sprintf("%0.2f", eval.SuccessRate),
				fmt.Sprintf("%d", eval.TotalAttempts),
			})
		}
	}
	csv.Flush()
	if err := csv.Error(); err != nil {
		return "", err
	}
	return buf.String(), errs
}

func (result *EvalsAcrossModels) Check() error {
	var errs error
	for _, result := range result.ModelResults {
		if err := result.Check(); err != nil {
			errs = errors.Join(errs, fmt.Errorf("%s > %w", result.ModelName, err))
		}
	}
	return errs
}

// Run evals across models.
//
// Models run in parallel, and evals run in series, with all attempts in
// parallel.
func (m *Evaluator) EvalsAcrossModels(
	ctx context.Context,
	// Evals to run. Defaults to all.
	// +optional
	evals []string,
	// Models to run evals across. Defaults to all.
	// +optional
	models []string,
	// Attempts to run each eval. Defaults to a per-provider value.
	// +optional
	attempts int,
	// A system prompt to use.
	// +optional
	systemPrompt *dagger.File,
) (*EvalsAcrossModels, error) {
	work := dag.Workspace()
	if systemPrompt != nil {
		work = work.WithSystemPromptFile(systemPrompt)
	}
	if len(evals) == 0 {
		names, err := work.EvalNames(ctx)
		if err != nil {
			return nil, err
		}
		evals = names
	}
	if len(models) == 0 {
		knownModels, err := work.KnownModels(ctx)
		if err != nil {
			return nil, err
		}
		models = knownModels
	}
	p := pool.NewWithResults[ModelResult]()
	for _, model := range models {
		ctx, modelSpan := Tracer().Start(ctx, fmt.Sprintf("model: %s", model),
			telemetry.Reveal())
		p.Go(func() ModelResult {
			report := ModelResult{
				ModelName: model,
			}
			defer telemetry.End(modelSpan, func() error {
				return report.Check()
			})
			for _, name := range evals {
				result := EvalResult{
					Name: name,
				}
				evalErr := (func() (rerr error) {
					ctx, evalSpan := Tracer().Start(ctx, fmt.Sprintf("eval: %s", name),
						telemetry.Reveal(),
						telemetry.Encapsulate())
					defer telemetry.End(evalSpan, func() error { return rerr })
					stdio := telemetry.SpanStdio(ctx, "")
					defer stdio.Close()
					attempts := work.Evaluate(name, dagger.WorkspaceEvaluateOpts{
						Model:    model,
						Attempts: attempts,
					})
					var err error
					result.Report, err = attempts.Report(ctx)
					if err != nil {
						return err
					}
					fmt.Fprint(stdio.Stdout, result.Report)
					result.SuccessRate, err = attempts.SuccessRate(ctx)
					if err != nil {
						return err
					}
					result.TotalAttempts, err = attempts.TotalAttempts(ctx)
					if err != nil {
						return err
					}
					if result.SuccessRate <= MinSuccessRate {
						return fmt.Errorf("success rate too low: %.f%% (%d attempts)",
							result.SuccessRate*100,
							result.TotalAttempts)
					}
					return nil
				})()
				if evalErr != nil {
					result.Error = evalErr.Error()
				}
				report.EvalReports = append(report.EvalReports, result)
			}
			return report
		})
	}
	return &EvalsAcrossModels{
		ModelResults: p.Wait(),
	}, nil
}

func (m *Evaluator) SystemPrompt(ctx context.Context,
	// Run a particular eval, instead of leaving it open-ended.
	// +optional
	evalName string,
) (string, error) {
	env := m.env().
		WithFileInput("scheme.md", m.Docs, "The documentation to consult for generating your system prompt.").
		WithWorkspaceOutput("workspace", "The workspace with the system prompt assigned.")
	evalStep := "Run all available evaluations."
	if evalName != "" {
		evalStep = fmt.Sprintf("Run the %q evaluation.", evalName)
	}
	return m.llm().
		WithSystemPrompt(`You are an autonomous system prompt refinement loop.

Your primary loop is to:
1. Generate an efficient, minimal system prompt. Focus on framing first - try a single sentence that sets the foundation.
2. Configure the workspace with your newly generated system prompt.
3. ` + evalStep + `
4. Generate a report summarizing your understanding of the failures or successes. If there are any failures, focus on those. Be sure to include examples from the report to back up your analysis. Respond in Markdown format, with a brief summary of issues at the end.
4. If improvement is needed, generate a new system prompt and repeat the cycle.
5. If all evaluations pass fully, output the final system prompt and stop.

You control this loop end-to-end. Do not treat this as a one-shot task. Continue refining until success is achieved.
`).
		WithEnv(env).
		WithPrompt(`Read the documentation and tell me every rule that you can infer from it.`).
		WithPrompt(`If you have any major questions or notice any potential issues with the documentation, let me know.`).
		WithPrompt(`Now generate a system prompt and start your loop. Keep going until all eval attempts succeed.`).
		Env().
		Output("work").
		AsWorkspace().
		SystemPrompt(ctx)
}

func (m *Evaluator) Explore(ctx context.Context) ([]string, error) {
	return m.llm().
		WithEnv(m.env().
			WithWorkspaceOutput("findings", "The workspace with all of your findings recorded.")).
		WithPrompt(`You are a quality assurance engineer running a suite of LLM evals and finding any issues that various models have interpreting them.`).
		WithPrompt(`Focus on exploration. Find evals that work on some models, but not others.`).
		WithPrompt(`If an eval fails for all models, don't bother running it again, but if there is partial success, try running it again or with different models.`).
		WithPrompt(`BEWARE: you will almost certainly hit rate limits. Find something else to do with another model in that case, or back off for a bit.`).
		WithPrompt(`Keep performing evaluations against various models, and record any interesting findings.`).
		Env().
		Output("findings").
		AsWorkspace().
		Findings(ctx)
}

func (m *Evaluator) GenerateSystemPrompt(ctx context.Context) (string, error) {
	return m.llm().
		WithEnv(m.env().
			WithWorkspaceOutput("generated",
				"The workspace with the system prompt configured."),
		).
		WithPrompt("Interpret the documentation and tell me everything rule that you can infer from it.").
		Loop().
		WithPrompt("Set a system prompt based on your understanding of the documentation. Keep it short and focused, but not so short to the point of being useless word salad. Focus on framing and foundation and let the model do the rest.").
		Env().
		Output("generated").
		AsWorkspace().
		SystemPrompt(ctx)
}

func (m *Evaluator) Evaluate(ctx context.Context, model, name string) (string, error) {
	eval := m.work().Evaluate(name, dagger.WorkspaceEvaluateOpts{
		Model: model,
	})
	report, err := eval.Report(ctx)
	if err != nil {
		return "", err
	}
	return m.analyzeAndGenerateSystemPrompt(
		ctx,
		// an initial env with no outputs, since message history is all we want at
		// first
		dag.Env().
			WithFileInput("docs", m.Docs,
				"The documentation the model is meant to adhere to.").
			WithFileInput("initialSystemPrompt", m.InitialPrompt,
				"An initial system prompt to evaluate and improve.").
			WithFileInput("report",
				dag.Directory().
					WithNewFile("report.txt", report).
					File("report.txt"),
				"The report of all eval attempt results."),
	)
}

// Iterate continuously runs evals across all models.
func (m *Evaluator) Iterate(ctx context.Context) (string, error) {
	prompt, err := m.InitialPrompt.Contents(ctx)
	if err != nil {
		return "", err
	}
	for {
		// little awkward, we turn it back into a file for ease-of-reuse of
		// EvalsAcrossModels (w2b self calls)
		promptFile := dag.Directory().WithNewFile("prompt.txt", prompt).File("prompt.txt")

		evals, err := m.EvalsAcrossModels(ctx, nil, nil, 0, promptFile)
		if err != nil {
			return "", err
		}
		if evals.Check() == nil {
			return prompt, nil
		}
		reports := dag.Directory()
		for _, report := range evals.ModelResults {
			for _, result := range report.EvalReports {
				reports = reports.WithNewFile(report.ModelName+"-"+result.Name+".md", result.Report)
			}
		}
		reportFilenames, err := reports.Entries(ctx)
		if err != nil {
			return "", err
		}
		prompt, err = m.analyzeAndGenerateSystemPrompt(
			ctx,
			// an initial env with no outputs, since message history is all we want at
			// first
			dag.Env().
				WithFileInput("docs", m.Docs,
					"The documentation the model is meant to adhere to.").
				WithFileInput("systemPrompt",
					promptFile,
					"The system prompt to evaluate and improve.").
				WithStringInput("failures", evals.Check().Error(), "The summary of failures.").
				WithDirectoryInput("reports",
					reports,
					"A directory containing all reports: "+strings.Join(reportFilenames, ", ")),
		)
		if err != nil {
			return "", err
		}
	}
}

func (m *Evaluator) analyzeAndGenerateSystemPrompt(ctx context.Context, researchEnv *dagger.Env) (string, error) {
	return m.llm().
		WithEnv(researchEnv).
		WithPrompt("Generate a report summarizing your current understanding of the failures or successes. Grade the overall result, with a brief description., followed by further analysis. If there are any failures, focus on those. Be sure to include examples from the report to back up your analysis. Respond in Markdown format, with a brief summary of issues at the end.").
		Loop().
		WithPrompt("Cross reference your summary with the documentation and the system prompt that was used. Suggest improvements without over-specializing for any particular evaluation. Focus on deeper issues -- don't cheat.").
		WithPrompt("Compare the successful results with the failed ones - why did the successful ones work? What element of the documentation or prompt was most relevant, in the general sense? How can the prompt guide the model to achieve the same result? When failures occur for multiple reasons, the more general reason is always more interesting.").
		Loop().
		WithEnv(
			researchEnv.
				WithStringOutput("prompt", "Your newly generated prompt."),
		).
		WithPrompt("Generate a new system prompt incorporating your suggestions. Focus on brevity - remember that each word has a cost, both monetary and in context waste.").
		Env().
		Output("prompt").
		AsString(ctx)
}

func (m *Evaluator) work() *dagger.Workspace {
	work := dag.Workspace()
	if m.InitialPrompt != nil {
		work = work.WithSystemPromptFile(m.InitialPrompt)
	}
	return work
}
