---
name: dagger-chores
description: Handle quick, repeatable Dagger repository maintenance chores. Use when the user asks for small operational changes and wants the same established edits and commit style applied quickly.
---

# Dagger Chores

## Go Version Bump

Use this checklist when asked to bump Go.

1. Update the Go version string in `engine/distconsts/consts.go`:
- `GolangVersion = "X.Y.Z"`

2. Update the Go default version string in `toolchains/go/main.go`:
- `// +default="X.Y.Z"` on the `version` argument in `New(...)`

3. Use a short commit message in this format:
- `chore: bump to go <major.minor>`
- Example: `chore: bump to go 1.26`

4. Create a signed commit:
- `git commit -s -m "chore: bump to go <major.minor>"`

5. Tell the user to double-check whether new Go version locations have been introduced since the last bump, and mention they can ask the agent for help finding them.
- Suggested wording: `Please double-check if any additional Go version strings were added in new files; these locations can change over time. If helpful, I can also help search for those locations.`

## Regenerate Generated Files

Use this checklist when asked to regenerate generated files.

1. From the Dagger repo root, create a temp file for command output and store its path in `tmp_log`.

2. Run generation and redirect all output to the temp file:
- `dagger --progress=plain call generate layer export --path . >"$tmp_log" 2>&1`

3. Search the temp file as needed instead of printing full output.

4. Delete the temp file when done.
