# Dagger Python SDK

Install [hatch](https://hatch.pypa.io/latest/install/). Recommend [`pipx`](https://github.com/pypa/pipx)), e.g. `pipx install hatch`

Run tests with `hatch run test`. Assumes `go run ./cmd/cloak dev --workdir sdk/python` is running.

Run the linter, reformatting code with `hatch run lint:fmt` or just check with `hatch run lint:style`.

Re-regenerate client with `hatch run generate`. Remember to run `hatch run lint:fmt` afterwards for consistent output!

Check types with `hatch run lint:typing`.
