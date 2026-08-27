# Editor

A Dagger-native sandboxed coding agent.

Editor is a [Dagger](https://dagger.io) module that exposes an LLM-powered coding
agent along with a set of filesystem tools for exploring and editing a workspace.
The agent runs in an isolated, sandboxed environment and interacts with your
project through a small, well-defined tool surface.

## Overview

The module is written in the [`dang`](https://github.com/vito/dang) SDK and
defines an `Editor` type. Calling `agent` returns a configured `LLM` that has
all of Editor's tools attached and a system prompt tuned for autonomous,
sandboxed coding work.

## Tools

| Tool | Description |
| --- | --- |
| `read` | Read the contents of a file, with line numbers and optional offset/limit for large files. |
| `write` | Write content to a file, creating it (and parent directories) if needed. |
| `edit` | Replace exact text within a file. Supports single or `replaceAll` replacements. |
| `find` | Find paths by glob pattern (use `**` to match across directories). |
| `grep` | Search file contents by regex or literal text, with glob/path filters. |
| `todoWrite` | Track a TODO list across `pending`, `inProgress`, and `completed` states. |

## Usage

Install the module and run the agent with Dagger. For example:

```sh
dagger install github.com/vito/editor
dagger agent
```

The agent operates against a `Workspace` and reports its progress to stdout.
Paths passed to tools are relative to the workspace root (`/workspace`).

## Project Context

When starting up, the agent looks for a project context file — one of
`DOUG.md`, `AGENTS.md`, or `CLAUDE.md` — in the workspace root. If found, its
contents are injected into the system prompt and treated as instructions that
override the agent's default behavior.

## System Prompt Guidelines

The agent is configured to:

- Use `find` and `grep` for file discovery.
- Read files before editing them.
- Prefer precise `edit` changes; use `write` only for new files or full rewrites.
- Avoid creating documentation or README files unless explicitly requested.
- Follow existing conventions in the files it edits.
- Use multiple tool calls concurrently when possible.
