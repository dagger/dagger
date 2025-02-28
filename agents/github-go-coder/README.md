# GoCoder

A small agent that writes Go programs and interacts with GitHub.

## Functions

### Assignment

Give the go-coder an assignment and it will write a Go program to solve it.

Example: `assignment "write a curl clone" | terminal`

### Solve Issue

Give the go-coder a GitHub issue and it will write a Go program to solve it, and then open a PR with the solution.

Example:

For the GitHub issue at github.com/USER/REPO/issues/123 and your github token in the environment variable GITHUB_TOKEN

`solve-issue GITHUB_TOKEN https://github.com/USER/REPO 123 --model gpt-4o`

### PR Feedback

When the GoCoder has created a PR, you can give it follow-up feedback to iterate on the solution.

Example:

When the GoCoder has created PR github.com/USER/REPO/pull/124 and your github token in the environment variable GITHUB_TOKEN

`pr-feedback GITHUB_TOKEN https://github.com/USER/REPO 124 "please add tests for the new code" --model gpt-4o`
