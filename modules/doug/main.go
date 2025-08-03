package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"dagger/doug/internal/dagger"
	"slices"
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

// Agent creates a Doug coding agent
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

	if _, err := before.Export(ctx, "a/"+filePath); err != nil {
		return nil, err
	}
	if _, err := after.Export(ctx, "b/"+filePath); err != nil {
		return nil, err
	}

	cmd := exec.Command("diff", "--unified", "--color=always", "a/"+filePath, "b/"+filePath)
	_ = cmd.Run()

	return dag.CurrentEnv().WithHostfs(d.Source.WithFile(filePath, after)), nil
}

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
