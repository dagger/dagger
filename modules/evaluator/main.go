package main

import (
	"context"
	"dagger/botsbuildingbots/internal/dagger"
	"dagger/botsbuildingbots/internal/telemetry"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/sourcegraph/conc/pool"
	"go.opentelemetry.io/otel/trace"
)

type Evaluator struct {
	// The documentation for the tool calling scheme to generate a prompt for.
	Docs *dagger.File

	// A system prompt to apply to all evals.
	SystemPrompt *dagger.File

	// Whether to disable the default system prompt.
	DisableDefaultSystemPrompt bool

	EvaluatorModel string

	// +private
	Evals []*dagger.WorkspaceEval
}

const MinSuccessRate = 0.8

func New(
	// Model to use for the evaluator agent.
	// +optional
	model string,
) *Evaluator {
	return &Evaluator{
		EvaluatorModel: model,
	}
}

type Eval interface {
	Name(context.Context) (string, error)
	Prompt(base *dagger.LLM) *dagger.LLM
	Check(ctx context.Context, prompt *dagger.LLM) error

	DaggerObject
}

// Set a system prompt to be provided to the evals.
func (m *Evaluator) WithSystemPrompt(prompt string) *Evaluator {
	return m.WithSystemPromptFile(dag.File("prompt.md", prompt))
}

// Set a system prompt to be provided to the evals.
func (m *Evaluator) WithSystemPromptFile(file *dagger.File) *Evaluator {
	cp := *m
	cp.SystemPrompt = file
	return &cp
}

// Disable Dagger's built-in system prompt.
//
// You probably don't need to use this - Dagger's system prompt provides the
// fundamentals for how the agent interacts with Dagger objects. This is
// primarily exposed so that we (Dagger) can iteratively test the default system
// prompt itself.
func (m *Evaluator) WithoutDefaultSystemPrompt() *Evaluator {
	cp := *m
	cp.DisableDefaultSystemPrompt = true
	return &cp
}

// Set the full documentation the system prompt intends to effectuate.
func (m *Evaluator) WithDocs(prompt string) *Evaluator {
	return m.WithDocsFile(dag.File("prompt.md", prompt))
}

// Set the full documentation the system prompt intends to effectuate.
func (m *Evaluator) WithDocsFile(file *dagger.File) *Evaluator {
	cp := *m
	cp.Docs = file
	return &cp
}

func (m *Evaluator) WithEval(ctx context.Context, eval Eval) (*Evaluator, error) {
	id, err := eval.(interface {
		ID(context.Context) (EvalID, error)
	}).ID(ctx)
	if err != nil {
		return nil, err
	}
	// FIXME: it would be nice to not have to do this workaround. it's hard
	// because we want to accept Eval, but then the type has no AsWorkspaceEval().
	//
	// fortunately the IDs are the same nonetheless, so we can just convert it
	// with the available plumbing
	m.Evals = append(m.Evals, dag.LoadWorkspaceEvalFromID(dagger.WorkspaceEvalID(id)))
	return m, nil
}

func (m *Evaluator) WithEvals(ctx context.Context, evals []Eval) (*Evaluator, error) {
	for _, eval := range evals {
		var err error
		m, err = m.WithEval(ctx, eval)
		if err != nil {
			return nil, err
		}
	}
	return m, nil
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
) (*EvalsAcrossModels, error) {
	work := m.work()
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
				// track model span ID so we can link to it
				SpanID: modelSpan.SpanContext().SpanID().String(),
			}
			defer telemetry.End(modelSpan, report.Check)
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
					// track eval span ID so we can link to it
					result.SpanID = evalSpan.SpanContext().SpanID().String()
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
					result.InputTokens, err = attempts.InputTokens(ctx)
					if err != nil {
						return err
					}
					result.OutputTokens, err = attempts.OutputTokens(ctx)
					if err != nil {
						return err
					}
					if result.SuccessRate < MinSuccessRate {
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
		TraceID: trace.SpanContextFromContext(ctx).TraceID().String(),

		ModelResults: p.Wait(),
	}, nil
}

type EvalsAcrossModels struct {
	TraceID      string
	ModelResults []ModelResult

	// +private
	Evaluator *Evaluator
}

type ModelResult struct {
	ModelName   string
	SpanID      string
	EvalReports []EvalResult
}

type EvalResult struct {
	Name          string
	SpanID        string
	Error         string
	Report        string
	SuccessRate   float64
	TotalAttempts int
	InputTokens   int
	OutputTokens  int
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

func (evals *EvalsAcrossModels) Check() error {
	var errs error
	for _, result := range evals.ModelResults {
		if err := result.Check(); err != nil {
			errs = errors.Join(errs, fmt.Errorf("%s > %w", result.ModelName, err))
		}
	}
	return errs
}

func (evals *EvalsAcrossModels) AnalyzeAndGenerateSystemPrompt(ctx context.Context) (string, error) {
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
	// an initial env with no outputs, since message history is all we want at
	// first
	researchEnv :=
		evals.Evaluator.env().
			WithStringInput("failures",
				evals.Check().Error(),
				"The summary of failures.").
			WithDirectoryInput("reports",
				reports,
				"A directory containing all reports: "+strings.Join(reportFilenames, ", "))
	return evals.Evaluator.llm().
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
		WithPrompt("Generate a new system prompt incorporating your suggestions. Make incremental improvements - only completely rewrite the prompt as a last resort.").
		Env().
		Output("prompt").
		AsString(ctx)
}

func (m *Evaluator) Explore(ctx context.Context) ([]string, error) {
	return m.llm().
		WithEnv(m.env().
			WithWorkspaceOutput("findings", "The workspace with all of your findings recorded.")).
		WithPrompt(`You are a quality assurance engineer running a suite of LLM evals and finding any issues that various models have interpreting them.`).
		WithPrompt(`Focus on exploration. Find evals that work on some models, but not others.`).
		WithPrompt(`If an eval fails for all models, don't bother running it again, but if there is partial success, try running it again or with different models.`).
		WithPrompt(`Keep performing evaluations against various models, and record any interesting findings.`).
		Env().
		Output("findings").
		AsWorkspace().
		Findings(ctx)
}

func (m *Evaluator) GenerateSystemPrompt(ctx context.Context) (string, error) {
	return m.llm().
		WithEnv(m.env().
			WithStringOutput("prompt", "A newly generated system prompt."),
		).
		WithPrompt("Interpret the documentation and tell me everything rule that you can infer from it.").
		Loop().
		WithPrompt("Set a system prompt based on your understanding of the documentation. Keep it short and focused, but not so short to the point of being useless word salad. Focus on framing and foundation and let the model do the rest.").
		Env().
		Output("generated").
		AsWorkspace().
		SystemPrompt(ctx)
}

// Iterate runs all evals across all models in a loop until all of the evals
// succeed, analyzing the failures and generating a new system prompt to
// course-correct.
func (m *Evaluator) Iterate(ctx context.Context) (string, error) {
	for {
		evals, err := m.EvalsAcrossModels(ctx, nil, nil, 0)
		if err != nil {
			return "", err
		}
		if evals.Check() == nil {
			// all checks passed; return the current system prompt
			return m.SystemPrompt.Contents(ctx)
		}
		prompt, err := evals.AnalyzeAndGenerateSystemPrompt(ctx)
		if err != nil {
			return "", err
		}
		// switch to the newly generated prompt for the next iteration
		m = m.WithSystemPrompt(prompt)
	}
}

func (m *Evaluator) llm() *dagger.LLM {
	return dag.LLM(dagger.LLMOpts{Model: m.EvaluatorModel})
}

func (m *Evaluator) env() *dagger.Env {
	env := dag.Env().
		WithWorkspaceInput("workspace", m.work(), "A space for you to work in.")
	if m.Docs != nil {
		env = env.WithFileInput("docs", m.Docs,
			"The documentation the model is meant to adhere to.")
	}
	if m.SystemPrompt != nil {
		env = env.WithFileInput("initialSystemPrompt", m.SystemPrompt,
			"An initial system prompt to evaluate and improve.")
	}
	return env
}

func (m *Evaluator) work() *dagger.Workspace {
	work := dag.Workspace().
		WithEvals(m.Evals)
	if m.DisableDefaultSystemPrompt {
		work = work.WithoutDefaultSystemPrompt()
	}
	if m.SystemPrompt != nil {
		work = work.WithSystemPromptFile(m.SystemPrompt)
	}
	return work
}
