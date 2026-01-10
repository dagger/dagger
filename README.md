## Dagger: a better way to ship

Dagger is a platform for automating software delivery. It can build, test and ship any codebase, reliably and at scale.

Dagger runs locally, in your CI server, or directly in the cloud. 

```
brew install dagger/tap/dagger
```

## Why Dagger?

Dagger makes your software delivery *programmable*, *local-first*, *repeatable* and *observable*.

**Programmable**. Shell scripts and proprietary YAML are no longer acceptable for automating software delivery. Dagger provides: a complete execution engine and system API; SDKs for 8 languages; an interactive REPL; a rich ecosystem of reusable modules; and more.

**Local-first**. Once you automate a task with Dagger, it will reliably run on any supported system: your laptop, AI sandbox, CI server, or dedicated cloud infrastructure. The only dependency is a container runtime like Docker.

**Repeatable**. Tools run in containers, orchestrated by sandboxed functions. Host dependencies are explicit and strictly typed. Intermediate artifacts are built just-in-time. Every operation is incremental by default, with advanced cache control. Whether it's a test report, a build or a deployment, Dagger gives you an output you can trust.

**Observable**. Every operation emits a full OpenTelemetry trace, enriched by granular logs and metrics. Visualize the trace in directly in the terminal, or in a web view. Debug complex workflows immediately instead of guessing what went wrong from a wall of text logs.

## Features

**System API**. A cross-language API for orchestrating containers, filesystems, secrets, git repositories, network tunnels, and more. Every operation is typed and composable.

**SDKs in 8 languages**. Native SDKs for Go, Python, TypeScript, PHP, Java, .NET, Elixir and Rust. Each SDK is generated from the API schema, so you get idiomatic code with full type safety and editor support.

**Typed artifacts**. Define custom object types with encapsulated state and functions. Types are content-addressed and can be passed across SDK language boundaries and module boundaries without serialization.

**Incremental execution**. Every operation is keyed by its inputs. Change one file and only the affected operations re-run. Caching is content-addressed and works automatically across local runs and CI.

**Runs anywhere**. The only requirement is a Linux container runtime. Runs natively on Linux, or via Docker Desktop and similar products on macOS and Windows. Local and CI behavior are identical.

**Built-in tracing**. Every operation emits OpenTelemetry spans. The CLI includes a live TUI; traces can also be exported to Jaeger, Honeycomb, or any OTel-compatible backend.


## Getting started

- [Documentation](https://docs.dagger.io)
- [Quickstart](https://docs.dagger.io/quickstart)

## Community

- [Discord](https://discord.gg/dagger-io)
- [GitHub Discussions](https://github.com/dagger/dagger/discussions)

## Contributing

See [CONTRIBUTING.md](https://github.com/dagger/dagger/blob/main/CONTRIBUTING.md).
