// A Dagger module for evaluating and improving LLM performance across multiple models.
//
// The Evaluator provides a comprehensive framework for testing AI models against
// custom evaluations, analyzing failures, and iteratively refining system prompts
// to improve performance. It supports parallel execution across multiple models,
// automatic prompt optimization, and detailed reporting with telemetry integration.
//
// Key features:
// - Run evaluations across multiple AI models in parallel
// - Automatically analyze failures and generate improved system prompts
// - Export results to CSV format for further analysis
// - Compare evaluation results between different runs
// - Integrated with Dagger's telemetry for detailed tracing
//
// More info: https://dagger.io/blog/evals-as-code

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
	// The documentation that defines expected model behavior and serves as the reference for evaluations.
	Docs *dagger.File

	// A system prompt file that will be applied to all evaluations to provide consistent guidance.
	SystemPrompt *dagger.File

	// Whether to disable Dagger's built-in default system prompt (usually not recommended).
	DisableDefaultSystemPrompt bool

	// The AI model to use for the evaluator agent that performs analysis and prompt generation.
	EvaluatorModel string

	// +private
	Evals []*dagger.WorkspaceEval
}

const MinSuccessRate = 0.8

func New(
	// The AI model name to use for the evaluator agent (e.g., "gpt-4o", "claude-sonnet-4-5").
	// If not specified, uses the default model configured in the environment.
	// +optional
	model string,
) *Evaluator {
	return &Evaluator{
		EvaluatorModel: model,
	}
}

// Eval represents a single evaluation that can be run against an LLM.
//
// Implementations must provide a name, a prompt to test, and a check function
// to validate the LLM's response.
type Eval interface {
	Name(context.Context) (string, error)
	Prompt(base *dagger.LLM) *dagger.LLM
	Check(ctx context.Context, prompt *dagger.LLM) error

	DaggerObject
}

// Set a system prompt to be provided to all evaluations.
//
// The system prompt provides foundational instructions and context that will be
// applied to every evaluation run. This helps ensure consistent behavior across
// all models and evaluations.
func (m *Evaluator) WithSystemPrompt(
	// The system prompt text to use for all evaluations.
	prompt string,
) *Evaluator {
	return m.WithSystemPromptFile(dag.File("prompt.md", prompt))
}

// Set a system prompt from a file to be provided to all evaluations.
//
// This allows you to load a system prompt from an external file, which is useful
// for managing longer prompts or when the prompt content is maintained separately
// from your code.
func (m *Evaluator) WithSystemPromptFile(
	// The file containing the system prompt to use for all evaluations.
	file *dagger.File,
) *Evaluator {
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

// Set the documentation content that the system prompt should enforce.
//
// This documentation serves as the reference material that evaluations will test
// against. The system prompt should guide the model to follow the principles and
// patterns defined in this documentation.
func (m *Evaluator) WithDocs(
	// The documentation content as a string.
	prompt string,
) *Evaluator {
	return m.WithDocsFile(dag.File("prompt.md", prompt))
}

// Set the documentation file that the system prompt should enforce.
//
// This allows you to load documentation from an external file. The documentation
// serves as the reference material for what behavior the evaluations should test,
// and the system prompt should guide the model to follow these principles.
func (m *Evaluator) WithDocsFile(
	// The file containing the documentation to reference.
	file *dagger.File,
) *Evaluator {
	cp := *m
	cp.Docs = file
	return &cp
}

// WithEval adds a single evaluation to the evaluator.
func (m *Evaluator) WithEval(
	ctx context.Context,
	// The evaluation to add to the list of evals to run.
	eval Eval,
) (*Evaluator, error) {
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

// WithEvals adds multiple evaluations to the evaluator.
func (m *Evaluator) WithEvals(
	ctx context.Context,
	// The list of evaluations to add to the evaluator.
	evals []Eval,
) (*Evaluator, error) {
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

// EvalsAcrossModels represents the results of running evaluations across multiple models.
type EvalsAcrossModels struct {
	TraceID      string
	ModelResults []ModelResult

	// +private
	Evaluator *Evaluator
}

// ModelResult represents the evaluation results for a single model.
type ModelResult struct {
	ModelName   string
	SpanID      string
	EvalReports []EvalResult
}

// EvalResult represents the results of a single evaluation.
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

// AnalyzeAndGenerateSystemPrompt performs comprehensive failure analysis and generates an improved system prompt.
//
// This function implements a sophisticated multi-stage analysis process:
//
//  1. **Report Generation**: Collects all evaluation reports from different models and
//     organizes them for analysis, providing a comprehensive view of successes and failures.
//
//  2. **Initial Analysis**: Generates a summary of current understanding, grading overall
//     results and focusing on failure patterns. Uses specific examples from reports to
//     support the analysis.
//
//  3. **Cross-Reference Analysis**: Compares the analysis against the original documentation
//     and system prompt, suggesting improvements without over-specializing for specific
//     evaluations. Focuses on deeper, systemic issues rather than superficial fixes.
//
//  4. **Success Pattern Analysis**: Compares successful results with failed ones to identify
//     what made the successful cases work. Extracts generalizable principles from the
//     documentation and prompts that led to success.
//
//  5. **Prompt Generation**: Creates a new system prompt incorporating all insights,
//     focusing on incremental improvements rather than complete rewrites unless absolutely
//     necessary.
//
// The process emphasizes finding general, root-cause issues over specific evaluation
// failures, ensuring that improvements help broadly rather than just fixing individual
// test cases.
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

// Explore evaluations across models to identify patterns and issues.
//
// This function uses an LLM agent to act as a quality assurance engineer,
// automatically running evaluations across different models and identifying
// interesting patterns. It focuses on finding evaluations that work on some
// models but fail on others, helping to identify model-specific weaknesses
// or strengths.
//
// The agent will avoid re-running evaluations that fail consistently across
// all models, but will retry evaluations that show partial success to gather
// more insights.
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

// Generate a new system prompt based on the provided documentation.
//
// This function uses an LLM to analyze the documentation and generate a system
// prompt that captures the key rules and principles. The process involves first
// interpreting the documentation to extract all inferable rules, then crafting
// a focused system prompt that provides proper framing without being overly
// verbose or turning into meaningless word salad.
//
// The generated prompt aims to establish foundation and context while allowing
// the model flexibility to apply the guidelines appropriately.
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

// llm returns a configured LLM instance for the evaluator agent.
//
// This creates an LLM instance using the evaluator's configured model,
// which is used for analysis, prompt generation, and exploration tasks.
func (m *Evaluator) llm() *dagger.LLM {
	return dag.LLM(dagger.LLMOpts{Model: m.EvaluatorModel})
}

// env returns a configured environment for the evaluator agent.
//
// This sets up the environment context that the evaluator LLM will use,
// including the workspace for running evaluations, documentation files
// (if provided), and any initial system prompt files. The environment
// provides the agent with all necessary context and tools.
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

// work returns a configured workspace for running evaluations.
//
// This creates a workspace instance with the evaluator's configuration
// applied, including all registered evaluations and system prompt settings.
// The workspace is what actually executes the evaluations against different
// models.
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
