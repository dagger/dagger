import subprocess
from typing import Annotated

import dagger
from dagger import DefaultPath, Doc, dag, function, object_type

MAX_RESPONSE_LENGTH = 30000


@object_type
class Doug:
    source: Annotated[dagger.Directory, DefaultPath(".")]

    @function
    async def agent(self, base: dagger.LLM) -> dagger.LLM:
        "Creates a Doug coding agent."
        provider = await base.provider()
        provider_prompts = (
            await dag.current_module().source().directory("prompts/system/").entries()
        )
        if f"{provider}.txt" in provider_prompts:
            system_prompt = (
                await dag.current_module()
                .source()
                .file(f"prompts/system/{provider}.txt")
                .contents()
            )
        else:
            system_prompt = (
                await dag.current_module()
                .source()
                .file("prompts/system/doug.txt")
                .contents()
            )
        return (
            base.with_env(
                base.env()
                .with_module(dag.current_module().meta())
                .with_string_input("TODOs", "", "Your TODO list")
            )
            .without_default_system_prompt()
            .with_system_prompt(system_prompt)
            .with_system_prompt(await self.reminder_prompt(base.env().hostfs()))
        )

    @function
    async def bash(self, command: str) -> dagger.Env:
        """
        Execute a bash command in a sandboxed environment with `git` and `dagger` installed.

        USAGE NOTES:
        - The environment will have `bash`, `git`, `dagger` installed.
        - The sandboxed environment will have Git, Bash, and the Dagger CLI
        installed.
        - Command output may be truncated, showing only the most recent logs.
        Use the ReadLogs tool when necessary to read more.
        """
        modified = (
            dag.wolfi(packages=["bash", "git"])
            .container()
            .with_file("/bin/dagger", dag.dagger_cli().binary())
            .with_mounted_directory("/workdir", self.source)
            .with_workdir("/workdir")
            .with_exec(["bash", "-c", command], experimental_privileged_nesting=True)
            .directory("/workdir")
        )
        return dag.current_env().with_hostfs(modified)

    @function
    async def read_file(
        self,
        file_path: str,
        offset: int | None,
        limit: int | None = 2000,
    ) -> str:
        """
        Reads a file from the project directory.

        HOW TO USE THIS TOOL:
        - Reads the first 2000 lines by default
        - Each line is prefixed with a line number followed by an arrow (→).
        Everything that follows the arrow is the literal content of the line.
        - You can specify an offset and limit to read line regions of a large
        file
        - If the file contents are empty, you will receive a warning.
        - If multiple files are interesting, you can read them all at once using
        multiple tool calls.
        """
        contents = await self.source.file(file_path).contents(
            offset=offset, limit=limit
        )

        if contents == "":
            return "WARNING: File contents are empty."

        # Add line numbers to each line
        lines = contents.split("\n")
        numbered_lines = []
        for i, line in enumerate(lines, 1):
            lineno = i + (offset or 0)
            numbered_lines.append(f"{lineno:>6}→{line}")

        return "\n".join(numbered_lines)

    @function
    async def edit_file(
        self,
        file_path: str,
        old_string: str,
        new_string: str,
        replace_all: bool = False,
    ) -> dagger.Env:
        """
        Edits files by replacing text, creating new files, or deleting content. For moving or renaming files, use the BasicShell tool with the 'mv' command instead. For larger file edits, use the Write tool to overwrite files.

        Before using this tool, use the ReadFile tool to understand the file's contents and context

        To make a file edit, provide the following:
        1. file_path: The relative path to the file to modify within the project directory
        2. old_string: The text to replace (must be unique within the file, and must match the file contents exactly, including all whitespace and indentation)
        3. new_string: The edited text to replace the old_string
        4. replace_all: Replace all occurrences of old_string (default false)

        Special cases:
        - To create a new file: provide file_path and new_string, leave old_string empty
        - To delete content: provide file_path and old_string, leave new_string empty

        The tool will replace ONE occurrence of old_string with new_string in the specified file by default. Set replace_all to true to replace all occurrences.

        CRITICAL REQUIREMENTS FOR USING THIS TOOL:

        1. UNIQUENESS: When replace_all is false (default), the old_string MUST uniquely identify the specific instance you want to change. This means:
           - Include AT LEAST 3-5 lines of context BEFORE the change point
           - Include AT LEAST 3-5 lines of context AFTER the change point
           - Include all whitespace, indentation, and surrounding code exactly as it appears in the file

        2. SINGLE INSTANCE: When replace_all is false, this tool can only change ONE instance at a time. If you need to change multiple instances:
           - Set replace_all to true to replace all occurrences at once
           - Or make separate calls to this tool for each instance
           - Each call must uniquely identify its specific instance using extensive context

        3. VERIFICATION: Before using this tool:
           - Check how many instances of the target text exist in the file
           - If multiple instances exist and replace_all is false, gather enough context to uniquely identify each one
           - Plan separate tool calls for each instance or use replace_all

        WARNING: If you do not follow these requirements:
           - The tool will fail if old_string matches multiple locations and replace_all is false
           - The tool will fail if old_string doesn't match exactly (including whitespace)
           - You may change the wrong instance if you don't include enough context

        When making edits:
           - Ensure the edit results in idiomatic, correct code
           - Do not leave the code in a broken state
           - Always use absolute file paths (starting with /)

        Remember: when making multiple file edits in a row to the same file, you should prefer to send all edits in a single message with multiple calls to this tool, rather than multiple messages with a single call each.`
        )
        """  # noqa: E501
        before = await self.source.file(file_path)

        after = await before.with_replaced(
            search=old_string,
            replacement=new_string,
            all=replace_all,
        )

        # print a nice diff
        await before.export("a/" + file_path)
        await after.export("b/" + file_path)
        subprocess.run(  # noqa: ASYNC221,S603
            [  # noqa: S607
                "diff",
                "--unified",
                "--color=always",
                "a/" + file_path,
                "b/" + file_path,
            ],
            text=True,
            check=False,
        )

        return dag.current_env().with_hostfs(
            self.source.with_file(
                file_path,
                after,
            )
        )

    @function
    async def write(self, path: str, contents: str) -> dagger.Env:
        """
        File writing tool that creates or updates files in the filesystem, allowing you to save or modify text content.

        WHEN TO USE THIS TOOL:
        - Use when you need to create a new file
        - Helpful for updating existing files with modified content
        - Perfect for saving code, configurations, or text data

        HOW TO USE:
        - Provide the path to the file you want to write
        - Include the content to be written to the file
        - The tool will create any necessary parent directories

        FEATURES:
        - Can create new files or overwrite existing ones
        - Creates parent directories automatically if they don't exist
        - Checks if the file has been modified since last read for safety
        - Avoids unnecessary writes when content hasn't changed

        LIMITATIONS:
        - You should read a file before writing to it to avoid conflicts
        - Cannot append to files (rewrites the entire file)

        TIPS:
        - Use the View tool first to examine existing files before modifying them
        - Use the BasicShell tool to verify the correct location when creating new files
        - Combine with Glob and Grep tools to find and modify multiple files
        """  # noqa: E501
        return dag.current_env().with_hostfs(
            self.source.with_new_file(path, contents=contents)
        )

    @function
    async def glob(self, pattern: str) -> None:
        r"""
        Fast file pattern matching tool that finds files by name and pattern, returning matching paths sorted by modification time (newest first).

        WHEN TO USE THIS TOOL:
        - Use when you need to find files by name patterns or extensions
        - Great for finding specific file types across a directory structure
        - Useful for discovering files that match certain naming conventions

        HOW TO USE:
        - Provide a glob pattern to match against file paths
        - Optionally specify a starting directory (defaults to current working directory)
        - Results are sorted with most recently modified files first

        GLOB PATTERN SYNTAX:
        - '*' matches any sequence of non-separator characters
        - '**' matches any sequence of characters, including separators
        - '?' matches any single non-separator character
        - '[...]' matches any character in the brackets
        - '[!...]' matches any character not in the brackets

        COMMON PATTERN EXAMPLES:
        - '*.js' - Find all JavaScript files in the current directory
        - '**/*.js' - Find all JavaScript files in any subdirectory
        - 'src/**/*.{ts,tsx}' - Find all TypeScript files in the src directory
        - '*.{html,css,js}' - Find all HTML, CSS, and JS files

        LIMITATIONS:
        - Results are limited to 100 files (newest first)
        - Does not search file contents (use Grep tool for that)
        - Hidden files (starting with '.') are skipped

        TIPS:
        - Patterns should use forward slashes (/) for cross-platform compatibility
        - For the most useful results, combine with the Grep tool: first find files with Glob, then search their contents with Grep
        - When doing iterative exploration that may require multiple rounds of searching, consider using the Task tool instead
        - Always check if results are truncated and refine your search pattern if needed
        """  # noqa: E501
        result = await dag.current_env().hostfs().glob(pattern)
        if len(result) == 0:
            print("No files found.")  # noqa: T201
            return
        # TODO: strip out dotfiles - we should do that in core
        result = [
            f for f in result if not any(part.startswith(".") for part in f.split("/"))
        ]
        for path in result:
            print(path)  # noqa: T201

    @function
    async def grep(  # noqa: PLR0913
        self,
        pattern: str,
        literal_text: bool = False,
        paths: list[str] | None = None,
        glob: list[str] | None = None,
        multiline: bool = False,
        content: bool = False,
        ignore_case: bool = False,
        limit: int = 1000,
    ):
        r"""
        Fast content search tool that finds files containing specific text or patterns, returning matching file paths sorted by modification time (newest first).

        WHEN TO USE THIS TOOL:
        - Use when you need to find files containing specific text or patterns
        - Great for searching code bases for function names, variable declarations, or error messages
        - Useful for finding all files that use a particular API or pattern

        HOW TO USE:
        - Provide a regex pattern to search for within file contents
        - Set literal_text=true if you want to search for the exact text with special characters (recommended for non-regex users)
        - Optionally specify starting paths (defaults to current working directory)
        - Optionally provide a glob patterns to filter which files to search
        - Use the ReadLogs tool to narrow down a large search result, analogous to piping grep to grep, head, or tail

        REGEX PATTERN SYNTAX (when literal_text=false):
        - Supports standard regular expression syntax
        - 'function' searches for the literal text "function"
        - 'log\..*Error' finds text starting with "log." and ending with "Error"
        - 'import\s+.*\s+from' finds import statements in JavaScript/TypeScript

        COMMON INCLUDE PATTERN EXAMPLES:
        - '*.js' - Only search JavaScript files
        - '*.{ts,tsx}' - Only search TypeScript files
        - '*.go' - Only search Go files

        LIMITATIONS:
        - Performance depends on the number of files being searched
        - Very large binary files may be skipped
        - Hidden files (starting with '.') are skipped

        IGNORE FILE SUPPORT:
        - Respects .gitignore patterns to skip ignored files and directories
        - Both ignore files are automatically detected in the search root directory

        CROSS-PLATFORM NOTES:
        - Uses ripgrep (rg) command internally
        - File paths are normalized automatically for cross-platform compatibility

        TIPS:
        - For faster, more targeted searches, first use Glob to find relevant files, then use Grep
        - When doing iterative exploration that may require multiple rounds of searching, consider using the Task tool instead
        - Always check if results are truncated and refine your search pattern if needed, or use ReadLogs to filter the result
        - Use literal_text=true when searching for exact text containing special characters like dots, parentheses, etc.`
        )
        """  # noqa: E501
        matches = (
            await dag.current_env()
            .hostfs()
            .search(
                pattern,
                paths=paths,
                globs=glob,
                multiline=multiline,
                dotall=multiline,  # TODO: just merge these? unsure
                files_only=not content,
                ignore_case=ignore_case,
                limit=limit,
                literal=literal_text,
            )
        )

        return f"{len(matches)} matches found"

    @function
    async def task(self, description: str, prompt: str) -> str:
        """
        Launch a new agent that has access to the same tools and environment.

        When you are searching for a keyword or file and are not confident that you will find the right match on the first try, use the Task tool to perform the search for you. For example:

        - If you are searching for a keyword like "config" or "logger", or for questions like "which file does X?", the Task tool is strongly recommended
        - If you want to read a specific file path, use the View or GlobTool tool instead of the Task tool, to find the match more quickly
        - If you are searching for a specific class definition like "class Foo", use the GlobTool tool instead, to find the match more quickly

        USAGE NOTES:
        1. Launch multiple agents concurrently whenever possible, to maximize performance; to do that, use a single message with multiple tool uses
        2. When the agent is done, it will return a single message back to you. The result returned by the agent is not visible to the user. To show the user the result, you should send a text message back to the user with a concise summary of the result.
        3. Each agent invocation is stateless. You will not be able to send additional messages to the agent, nor will the agent be able to communicate with you outside of its final report. Therefore, your prompt should contain a highly detailed task description for the agent to perform autonomously and you should specify exactly what information the agent should return back to you in its final and only message to you.
        4. The agent's outputs should generally be trusted
        5. IMPORTANT: The agent runs in a copy-on-write sandboxed environment. Any writes made by the agent will not be visible to the user, but will be available to the agent's next invocation.
        """  # noqa: E501
        system_prompt = (
            await dag.current_module()
            .source()
            .file("prompts/task_system_prompt.md")
            .contents()
        )
        env = dag.current_env()
        return (
            await dag.llm()
            .with_env(env.without_outputs())
            # Don't allow arbitrarily deep tasks.
            .with_blocked_function("Doug", "task")
            .without_default_system_prompt()
            .with_system_prompt(system_prompt)
            .with_system_prompt(await self.reminder_prompt(env.hostfs()))
            .with_prompt(prompt)
            .last_reply()
        )

    @function
    async def todo_write(  # noqa: PLR0912,C901
        self,
        pending: Annotated[
            list[str] | None, Doc("List of tasks to set in pending state")
        ] = None,
        in_progress: Annotated[
            list[str] | None, Doc("List of tasks to set in completed state")
        ] = None,
        completed: Annotated[
            list[str] | None, Doc("List of tasks to set in completed state")
        ] = None,
    ) -> dagger.Env:
        """
        Keep track of your TODO list

        WHEN TO USE THIS TOOL:
          - When a task requires 3 or more distinct steps or actions
          - To break down a complex task into smaller, manageable parts
          - When the user directly gave a sequence of tasks to perform (numbered
          or comma-separated)
          - When the user gave you more tasks in the middle of a conversation
          - When you want to see the full TODO list

        SKIP THIS TOOL WHEN:
          - The task is trivial
          - The task is purely conversational or informational

        HOW TO USE:
          - Call this function with the TODOs to record in each state (pending,
          in progress, completed)
          - TODOs are additive; you can call it with incremental updates for
          individual TODOs
          - The full TODO list will be printed in response
          - Every pending TODO must be eventually completed before your task is
          completed
          - To print the current TODO list, call this function with no arguments
        """
        if pending is None:
            pending = []

        if in_progress is None:
            in_progress = []

        if completed is None:
            completed = []

        current_todos = (
            await dag.current_env().input("TODOs").as_string() or ""
        ).split("\n")

        existing_completed = []
        existing_in_progress = []
        existing_pending = []
        for todo in current_todos:
            if not todo:
                continue

            # Parse the todo format: STATE:DESCRIPTION
            parts = todo.split(":", 1)
            if len(parts) != 2:  # noqa: PLR2004
                parts = ["pending", todo]

            state, description = parts

            if (
                description in pending
                or description in in_progress
                or description in completed
            ):
                # Item is being updated; append it later instead
                continue

            # Apply ANSI coloring and effects based on state
            if state.lower() == "completed":
                existing_completed.append(description)
            elif state.lower() == "in_progress":
                existing_in_progress.append(description)
            elif state.lower() == "pending":
                existing_pending.append(description)

        # Combine existing items first, then new items
        completed_list = existing_completed + completed
        in_progress_list = existing_in_progress + in_progress
        pending_list = existing_pending + pending

        formatted_todos = []  # formatted for user/LLM
        encoded_todos = []  # formatted for storing in env

        for todo in completed_list:
            encoded_todos.append(f"completed:{todo}")
            # green + strikethrough
            formatted_todos.append(f"■ \033[32m\033[9m{todo}\033[0m")
        for todo in in_progress_list:
            encoded_todos.append(f"in_progress:{todo}")
            # yellow
            formatted_todos.append(f"□ \033[33m{todo}\033[0m")
        for todo in pending_list:
            encoded_todos.append(f"pending:{todo}")
            # white/default
            formatted_todos.append(f"□ \033[37m{todo}\033[0m")

        # Display the formatted todos
        formatted_output = "\n".join(formatted_todos)
        print(formatted_output)  # noqa: T201

        return dag.current_env().with_string_input(
            "TODOs",
            "\n".join(encoded_todos),
            "Your TODO list",
        )

    async def reminder_prompt(self, source: dagger.Directory) -> str:
        """
        Returns a prompt to reinforce general behavior and teach the agent about project-specific context.
        """
        segments = [
            """
          # System Reminder

          - Do what has been asked; nothing more, nothing less.
          - NEVER create files unless they're absolutely necessary for achieving your goal.
          - ALWAYS prefer editing an existing file to creating a new one.
          - NEVER proactively create documentation files (*.md) or README files.
          - Only create documentation files if explicitly requested by the User.
          """
        ]

        ents = await source.entries()
        for file in ["DOUG.md", "AGENT.md", "CLAUDE.md"]:
            if file in ents:
                segments.append(
                    f"""
                # Project-Specific Context

                Make sure to follow the instructions in the context below:

                <project-context>
                {await source.file(file).contents()}
                </project-context>

                These instructions OVERRIDE any default behavior.
                """
                )
                break

        return "\n\n".join(segments)
