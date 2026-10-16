package main

// A directory passed to a contextual argument skips the callee's +ignore:
// the engine applies it only when it resolves the default itself. These
// copy each callee's pragma; keep them in sync with the callee.
var (
	// php-client-dev New workspaceDir (.dagger/modules/php-client-dev/main.go)
	phpClientDevWorkspaceIgnore = []string{"*", "!sdk/php", "sdk/php/.changes"}

	// python-client-dev New workspaceDir (.dagger/modules/python-client-dev/main.go)
	pythonClientDevWorkspaceIgnore = []string{
		"*",
		"!sdk/python/*.toml",
		"!sdk/python/*.lock",
		"!sdk/python/*/*.toml",
		"!sdk/python/*/*.lock",
		"!sdk/python/.python-version",
		"!sdk/python/dev/src/**/*.py",
		"!sdk/python/docs/**/*.py",
		"!sdk/python/docs/**/*.rst",
		"!sdk/python/runtime/images",
		"!sdk/python/src/**/*.py",
		"!sdk/python/src/**/py.typed",
		"!sdk/python/tests/**/*.py",
		"!sdk/python/codegen/**/*.py",
		"!sdk/python/README.md",
		"!sdk/python/LICENSE",
	}
)
