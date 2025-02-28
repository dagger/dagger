## What is Dagger?

Dagger is an open-source composable runtime for AI agents, by the creators of Docker. Originally developed and still used to replace CI workflows, Dagger's container-based execution engine has proven ideal for building AI agents — it transforms complex operations into programmable, repeatable, and observable workflows that scale seamlessly across environments.

### Why Dagger for AI Agents?

1. **Reproducible Execution**

Every AI operation—model inference, code generation, data transforms—runs in isolated, cacheable containers, guaranteeing consistent results across environments.

2. **End-to-End Observability**

Dagger traces agent workflows from prompt to system call. Dig into performance, logs, and container state without black-box uncertainty.

3. **Flexible AI Integration**

Attach LLM state directly to your workflow, choosing from OpenAI, Anthropic, or any other endpoint.
Give your AI agent dynamic access to code, files, and more.

4. **Fast Iteration**

With caching and concurrency, repeat tasks only once and run multiple tasks in parallel—perfect for iterative model tuning or experimentation.

5. **Meets you where you are**

Dagger supports multiple programming languages (Python, Go, TypeScript, Java, PHP), can run anywhere, and is easy to integrate with the frameworks and infrastructure you already use.

### Key Features

- **Typed composition**
  Define workflow steps as discrete, strongly typed functions. Chain, parallelize, or reuse them with minimal effort.

- **Cross-Language Module System**
  Package those functions as modules, exposing them via GraphQL. Import them in Python, Go, TypeScript, Java, or PHP.

- **Built-In Container Runtime**
  Dagger includes its own OCI-compatible runtime—no need for Docker, Containerd, or other external engines.

- **Caching & Concurrency**
  Automate detection of redundant work, cache results, and parallelize tasks for 2x–10x faster builds and AI workflows.

- **Deep tracing**
  Trace execution via OpenTelemetry, inspect container states, and debug every function call with precision.

- **AI integration**
  Connect to existing inference endpoints (OpenAI, Anthropic, Ollama, etc.), and easily mix and match models.
  Attach Dagger objects to the model’s environment for tool calling and reproducible state.

- **Easy to integrate**
  Embed Dagger modules in your existing application, with generated bindings or native MCP support (coming soon).

- **Interactive shell**
  Compose workflows interactively from the command-line, for rapid experiments, lightning-speed debugging, and cool demos

### Is Dagger only for AI?

No. It’s equally suited for CI/CD, general build automation, or any container-based workflow. AI is just one (very powerful) use case.
You can also take advantage of its native AI features to gradually introduce "agentic" features in your existing workflows,
without throwing away the stack you have.

## Getting started

- [Getting started with Dagger for AI agents](./agents/README.md) *(technology preview)*
- [Getting started with Dagger for CI/CD](https://docs.dagger.io/quickstart)

## Join the community

* Join the [Dagger community server](https://discord.gg/ufnyBtc8uY)
* Follow us on [Twitter](https://twitter.com/dagger_io)
* Check out our [community activities](https://dagger.io/community)
* Read more in our [documentation](https://docs.dagger.io)

## Contributing

Interested in contributing or building dagger from scratch? See
[CONTRIBUTING.md](https://github.com/dagger/dagger/tree/main/CONTRIBUTING.md).
