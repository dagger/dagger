package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dagger/doug/internal/dagger"
	"slices"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	godiffpatch "github.com/sourcegraph/go-diff-patch"
)

const MaxResponseLength = 30000

// Doug coding agent module
type Doug struct {
	// +private
	Source *dagger.Directory
}

func New(
	// +defaultPath="/"
	source *dagger.Directory,
) *Doug {
	return &Doug{Source: source}
}

// A CLI friendly entrypoint for starting a coding agent developing in a workdir.
func (d *Doug) Dev(
	ctx context.Context,
	source *dagger.Directory,
	// +optional
	module *dagger.Module,
) (*dagger.LLM, error) {
	if module == nil {
		module = source.AsModule()
	}
	return d.Agent(ctx,
		dag.LLM().WithEnv(
			dag.Env().
				WithHostfs(source).
				WithModule(module),
		),
	)
}

// Returns a Doug coding agent
func (d *Doug) Agent(ctx context.Context, base *dagger.LLM) (*dagger.LLM, error) {
	provider, err := base.Provider(ctx)
	if err != nil {
		return nil, err
	}

	promptsDir := dag.CurrentModule().Source().Directory("prompts/system/")
	entries, err := promptsDir.Entries(ctx)
	if err != nil {
		return nil, err
	}

	var systemPrompt string
	providerFile := fmt.Sprintf("%s.txt", provider)

	found := slices.Contains(entries, providerFile)

	if found {
		systemPrompt, err = dag.CurrentModule().Source().File(fmt.Sprintf("prompts/system/%s", providerFile)).Contents(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		systemPrompt, err = dag.CurrentModule().Source().File("prompts/system/doug.txt").Contents(ctx)
		if err != nil {
			return nil, err
		}
	}

	reminderPrompt, err := d.reminderPrompt(ctx, base.Env().Hostfs())
	if err != nil {
		return nil, err
	}

	return base.
		WithEnv(
			base.Env().
				WithModule(dag.CurrentModule().Meta()).
				WithStringInput("TODOs", "", "Your TODO list")).
		WithSystemPrompt(systemPrompt).
		WithSystemPrompt(reminderPrompt), nil
}

// Bash executes a bash command in a sandboxed environment with git and dagger installed
func (d *Doug) Bash(ctx context.Context, command string) (*dagger.Env, error) {
	modified := dag.Wolfi().
		Container(dagger.WolfiContainerOpts{
			Packages: []string{"bash", "git"},
		}).
		WithFile("/bin/dagger", dag.DaggerCli().Binary()).
		WithMountedDirectory("/workdir", d.Source).
		WithWorkdir("/workdir").
		WithExec([]string{"bash", "-c", command}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Directory("/workdir")

	return dag.CurrentEnv().WithHostfs(modified), nil
}

// ReadFile reads a file from the project directory
func (d *Doug) ReadFile(ctx context.Context, filePath string, offset *int, limit *int) (string, error) {
	if limit == nil {
		defaultLimit := 2000
		limit = &defaultLimit
	}

	opts := dagger.FileContentsOpts{
		Limit: *limit,
	}
	if offset != nil {
		opts.Offset = *offset
	}

	contents, err := d.Source.File(filePath).Contents(ctx, opts)
	if err != nil {
		return "", err
	}

	if contents == "" {
		return "WARNING: File contents are empty.", nil
	}

	lines := strings.Split(contents, "\n")
	var numberedLines []string

	startLine := 1
	if offset != nil {
		startLine = *offset + 1
	}

	for i, line := range lines {
		lineNo := startLine + i
		numberedLines = append(numberedLines, fmt.Sprintf("%6d→%s", lineNo, line))
	}

	return strings.Join(numberedLines, "\n"), nil
}

// EditFile edits files by replacing text, creating new files, or deleting content
func (d *Doug) EditFile(ctx context.Context, filePath, oldString, newString string, replaceAll *bool) (*dagger.Env, error) {
	if replaceAll == nil {
		defaultReplaceAll := false
		replaceAll = &defaultReplaceAll
	}

	before := d.Source.File(filePath)

	after := before.WithReplaced(oldString, newString, dagger.FileWithReplacedOpts{
		All: *replaceAll,
	})

	beforeContents, err := before.Contents(ctx)
	if err != nil {
		return nil, err
	}

	afterContents, err := after.Contents(ctx)
	if err != nil {
		return nil, err
	}

	diff := godiffpatch.GeneratePatch(filePath, beforeContents, afterContents)
	tokens, err := lexers.Get("diff").Tokenise(nil, diff)
	if err != nil {
		return nil, err
	}

	if err := formatters.TTY16.Format(os.Stdout, TTYStyle, tokens); err != nil {
		return nil, err
	}

	return dag.CurrentEnv().WithHostfs(d.Source.WithFile(filePath, after)), nil
}

// taken from chroma's TTY formatter
var ttyMap = map[string]string{
	"30m": "#000000", "31m": "#7f0000", "32m": "#007f00", "33m": "#7f7fe0",
	"34m": "#00007f", "35m": "#7f007f", "36m": "#007f7f", "37m": "#e5e5e5",
	"90m": "#555555", "91m": "#ff0000", "92m": "#00ff00", "93m": "#ffff00",
	"94m": "#0000ff", "95m": "#ff00ff", "96m": "#00ffff", "97m": "#ffffff",
}

// TTY style matches to hex codes used by the TTY formatter to map them to
// specific ANSI escape codes.
var TTYStyle = styles.Register(chroma.MustNewStyle("tty", chroma.StyleEntries{
	chroma.Comment:             ttyMap["95m"] + " italic",
	chroma.CommentPreproc:      ttyMap["90m"],
	chroma.KeywordConstant:     ttyMap["33m"],
	chroma.Keyword:             ttyMap["31m"],
	chroma.KeywordDeclaration:  ttyMap["35m"],
	chroma.NameBuiltin:         ttyMap["31m"],
	chroma.NameBuiltinPseudo:   ttyMap["36m"],
	chroma.NameFunction:        ttyMap["34m"],
	chroma.NameNamespace:       ttyMap["34m"],
	chroma.LiteralNumber:       ttyMap["31m"],
	chroma.LiteralString:       ttyMap["32m"],
	chroma.LiteralStringSymbol: ttyMap["33m"],
	chroma.Operator:            ttyMap["31m"],
	chroma.Punctuation:         ttyMap["90m"],
	chroma.Error:               ttyMap["91m"], // bright red for errors
	chroma.GenericDeleted:      ttyMap["91m"], // bright red for deleted content
	chroma.GenericEmph:         "italic",
	chroma.GenericInserted:     ttyMap["92m"], // bright green for inserted content
	chroma.GenericStrong:       "bold",
	chroma.GenericSubheading:   ttyMap["90m"], // dark gray for subheadings
	chroma.KeywordNamespace:    ttyMap["95m"], // bright magenta for namespace keywords
	chroma.Literal:             ttyMap["94m"], // bright blue for literals
	chroma.LiteralDate:         ttyMap["93m"], // bright yellow for dates
	chroma.LiteralStringEscape: ttyMap["96m"], // bright cyan for string escapes
	chroma.Name:                ttyMap["97m"], // bright white for names
	chroma.NameAttribute:       ttyMap["92m"], // bright green for attributes
	chroma.NameClass:           ttyMap["92m"], // bright green for classes
	chroma.NameConstant:        ttyMap["94m"], // bright blue for constants
	chroma.NameDecorator:       ttyMap["92m"], // bright green for decorators
	chroma.NameException:       ttyMap["91m"], // bright red for exceptions
	chroma.NameOther:           ttyMap["92m"], // bright green for other names
	chroma.NameTag:             ttyMap["95m"], // bright magenta for tags
	chroma.Text:                ttyMap["97m"], // bright white for text
}))

// Write creates or updates files in the filesystem
func (d *Doug) Write(ctx context.Context, path, contents string) (*dagger.Env, error) {
	return dag.CurrentEnv().WithHostfs(d.Source.WithNewFile(path, contents)), nil
}

// Glob finds files by name and pattern
func (d *Doug) Glob(ctx context.Context, pattern string) error {
	result, err := dag.CurrentEnv().Hostfs().Glob(ctx, pattern)
	if err != nil {
		return err
	}

	if len(result) == 0 {
		fmt.Println("No files found.")
		return nil
	}

	var filtered []string
	for _, path := range result {
		parts := strings.Split(path, "/")
		hasDotFile := false
		for _, part := range parts {
			if strings.HasPrefix(part, ".") {
				hasDotFile = true
				break
			}
		}
		if !hasDotFile {
			filtered = append(filtered, path)
		}
	}

	for _, path := range filtered {
		fmt.Println(path)
	}

	return nil
}

// Grep searches file contents for patterns
func (d *Doug) Grep(ctx context.Context, pattern string, literalText *bool, paths []string, glob []string, multiline *bool, content *bool, ignoreCase *bool, limit *int) (string, error) {
	opts := dagger.DirectorySearchOpts{}

	if literalText != nil {
		opts.Literal = *literalText
	}
	if paths != nil {
		opts.Paths = paths
	}
	if glob != nil {
		opts.Globs = glob
	}
	if multiline != nil {
		opts.Multiline = *multiline
		opts.Dotall = *multiline
	}
	if content != nil {
		opts.FilesOnly = !*content
	}
	if ignoreCase != nil {
		opts.IgnoreCase = *ignoreCase
	}
	if limit != nil {
		opts.Limit = *limit
	} else {
		opts.Limit = 1000
	}

	matches, err := dag.CurrentEnv().Hostfs().Search(ctx, pattern, opts)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%d matches found", len(matches)), nil
}

// Task launches a new agent with the same tools and environment
func (d *Doug) Task(ctx context.Context, description, prompt string) (string, error) {
	systemPrompt, err := dag.CurrentModule().Source().File("prompts/task_system_prompt.md").Contents(ctx)
	if err != nil {
		return "", err
	}

	env := dag.CurrentEnv()
	reminderPrompt, err := d.reminderPrompt(ctx, env.Hostfs())
	if err != nil {
		return "", err
	}

	return dag.LLM().
		WithEnv(env.WithoutOutputs()).
		WithBlockedFunction("Doug", "task").
		WithoutDefaultSystemPrompt().
		WithSystemPrompt(systemPrompt).
		WithSystemPrompt(reminderPrompt).
		WithPrompt(prompt).
		LastReply(ctx)
}

// TodoWrite keeps track of TODO list
func (d *Doug) TodoWrite(ctx context.Context, pending []string, inProgress []string, completed []string) (*dagger.Env, error) {
	if pending == nil {
		pending = []string{}
	}
	if inProgress == nil {
		inProgress = []string{}
	}
	if completed == nil {
		completed = []string{}
	}

	currentTodos, err := dag.CurrentEnv().Input("TODOs").AsString(ctx)
	if err != nil {
		currentTodos = ""
	}

	currentTodoLines := strings.Split(currentTodos, "\n")

	var existingCompleted, existingInProgress, existingPending []string

	for _, todo := range currentTodoLines {
		if todo == "" {
			continue
		}

		parts := strings.SplitN(todo, ":", 2)
		if len(parts) != 2 {
			parts = []string{"pending", todo}
		}

		state, description := parts[0], parts[1]

		isUpdating := slices.Contains(append(append(pending, inProgress...), completed...), description)
		if isUpdating {
			continue
		}

		switch strings.ToLower(state) {
		case "completed":
			existingCompleted = append(existingCompleted, description)
		case "in_progress":
			existingInProgress = append(existingInProgress, description)
		case "pending":
			existingPending = append(existingPending, description)
		}
	}

	completedList := append(existingCompleted, completed...)
	inProgressList := append(existingInProgress, inProgress...)
	pendingList := append(existingPending, pending...)

	var formattedTodos, encodedTodos []string

	for _, todo := range completedList {
		encodedTodos = append(encodedTodos, fmt.Sprintf("completed:%s", todo))
		formattedTodos = append(formattedTodos, fmt.Sprintf("■ \033[32m\033[9m%s\033[0m", todo))
	}
	for _, todo := range inProgressList {
		encodedTodos = append(encodedTodos, fmt.Sprintf("in_progress:%s", todo))
		formattedTodos = append(formattedTodos, fmt.Sprintf("□ \033[33m%s\033[0m", todo))
	}
	for _, todo := range pendingList {
		encodedTodos = append(encodedTodos, fmt.Sprintf("pending:%s", todo))
		formattedTodos = append(formattedTodos, fmt.Sprintf("□ \033[37m%s\033[0m", todo))
	}

	fmt.Println(strings.Join(formattedTodos, "\n"))

	return dag.CurrentEnv().WithStringInput("TODOs", strings.Join(encodedTodos, "\n"), "Your TODO list"), nil
}

func (d *Doug) reminderPrompt(ctx context.Context, source *dagger.Directory) (string, error) {
	segments := []string{
		`
# System Reminder

- Do what has been asked; nothing more, nothing less.
- NEVER create files unless they're absolutely necessary for achieving your goal.
- ALWAYS prefer editing an existing file to creating a new one.
- NEVER proactively create documentation files (*.md) or README files.
- Only create documentation files if explicitly requested by the User.
`,
	}

	entries, err := source.Entries(ctx)
	if err != nil {
		return strings.Join(segments, "\n\n"), nil
	}

	contextFiles := []string{"DOUG.md", "AGENT.md", "CLAUDE.md"}
	for _, file := range contextFiles {
		for _, entry := range entries {
			if entry == file {
				contents, err := source.File(file).Contents(ctx)
				if err != nil {
					continue
				}

				segments = append(segments, fmt.Sprintf(`
# Project-Specific Context

Make sure to follow the instructions in the context below:

<project-context>
%s
</project-context>

These instructions OVERRIDE any default behavior.
`, contents))
				goto found
			}
		}
	}
found:

	return strings.Join(segments, "\n\n"), nil
}
