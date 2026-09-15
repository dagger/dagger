package daggercmd

import (
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestRecommendationConfigContents(t *testing.T) {
	for _, tc := range []struct {
		name     string
		match    func(string) bool
		contents string
		want     bool
	}{
		{"JSON property", hasMochaProperty, `{"mocha": {"timeout": 1000}}`, true},
		{"JSON empty property", hasMochaProperty, `{"mocha": {}}`, true},
		{"JSON nested property", hasMochaProperty, `{"scripts": {"mocha": "test"}}`, false},
		{"JSON string", hasMochaProperty, `{"description": "mocha"}`, false},
		{"JSON invalid", hasMochaProperty, `{"mocha":`, false},
		{"TOML table", hasTOMLTable("tool.ruff"), "[tool.ruff]\nline-length = 88", true},
		{"TOML nested table", hasTOMLTable("tool.ruff"), "[tool.ruff.lint]\nselect = ['ALL']", true},
		{"TOML dotted key", hasTOMLTable("tool.ruff"), "tool.ruff.line-length = 88", true},
		{"TOML inline table", hasTOMLTable("tool.ruff"), "tool.ruff = {line-length = 88}", true},
		{"TOML empty table", hasTOMLTable("tool.ruff"), "[tool.ruff]", true},
		{"TOML comment", hasTOMLTable("tool.ruff"), "# [tool.ruff]\n[project]\nname = 'demo'", false},
		{"TOML string", hasTOMLTable("tool.ruff"), "description = '''\n[tool.ruff]\n'''", false},
		{"TOML quoted key", hasTOMLTable("tool.ruff"), "['tool.ruff']", false},
		{"TOML scalar", hasTOMLTable("tool.ruff"), "tool.ruff = true", false},
		{"TOML invalid", hasTOMLTable("tool.ruff"), "[tool.ruff]\ninvalid =", false},
		{"pytest legacy TOML", hasTOMLTable("tool.pytest"), "[tool.pytest.ini_options]\naddopts = '-q'", true},
		{"pytest native TOML", hasTOMLTable("tool.pytest"), "[tool.pytest]\naddopts = ['-q']", true},
		{"INI section", hasINISection("mypy"), "[metadata]\nname = demo\n[mypy]\nstrict = true", true},
		{"INI comment suffix", hasINISection("mypy"), "[mypy] ; options\r\nstrict = true", true},
		{"INI commented section", hasINISection("mypy"), "# [mypy]\n; [mypy]", false},
		{"INI option value", hasINISection("mypy"), "[metadata]\ndescription = text\n    [mypy]", false},
		{"INI wrong section", hasINISection("mypy"), "[mypy-other]", false},
		{"INI invalid section", hasINISection("mypy"), "[mypy]invalid", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.match(tc.contents))
		})
	}
}

func TestModuleRecommendationsInspectContents(t *testing.T) {
	ctx := t.Context()
	dag, err := dagger.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dag.Close()) })

	pyproject := "[tool.mypy]\n[tool.ruff.lint]\n[tool.pytest.ini_options]\n[tool.ty.rules]\n[tool.uv]"
	files := map[string]string{
		"package.json":          `{"mocha": {"timeout": 1000}}`,
		"js/.mocharc.json":      "{}",
		"js/package.json":       `{"name": "example", "description": "mocha"}`,
		"bad/package.json":      `{"mocha":`,
		"pyproject.toml":        "[project]\nname = 'example'",
		"python/pyproject.toml": pyproject,
		"bad/pyproject.toml":    "[tool.ruff]\ninvalid =",
		"text/pyproject.toml":   "description = '''\n[tool.pytest]\n[tool.mypy]\n[tool.ruff]\n[tool.ty]\n[tool.uv]\n'''",
		"native/pyproject.toml": "[tool.pytest]\naddopts = ['-q']",
		"mypy/.mypy.ini":        "[mypy]",
		"mypy/setup.cfg":        "[mypy]\nstrict = true",
		"other/setup.cfg":       "[metadata]\ndescription = mypy pytest",
		"pytest/.pytest.ini":    "[pytest]",
		"pytest/tox.ini":        "[pytest]\naddopts = -q",
		"pytest/setup.cfg":      "[tool:pytest]\naddopts = -q",
		"other/tox.ini":         "[tox]\nenvlist = py313",
		"ruff/.ruff.toml":       "line-length = 88",
		"ty/ty.toml":            "[rules]",
		"uv/uv.lock":            "version = 1",
		"uv/uv.toml":            "offline = true",
	}
	dir := dag.Directory()
	for path, contents := range files {
		dir = dir.WithNewFile(path, contents)
	}
	for _, excluded := range []string{".git", ".dagger", "node_modules", "vendor", "dist", "build", "target"} {
		for _, prefix := range []string{excluded, "python/" + excluded} {
			dir = dir.WithNewFile(prefix+"/pyproject.toml", pyproject).
				WithNewFile(prefix+"/package.json", `{"mocha": {}}`).
				WithNewFile(prefix+"/setup.cfg", "[mypy]\n[tool:pytest]")
		}
	}
	ws := dir.AsWorkspace(dagger.DirectoryAsWorkspaceOpts{Cwd: "/python"})
	want := map[string][]string{
		"mochajs": {"js/.mocharc.json", "package.json"},
		"mypy":    {"mypy/.mypy.ini", "mypy/setup.cfg", "python/pyproject.toml"},
		"pytest":  {"pytest/.pytest.ini", "native/pyproject.toml", "python/pyproject.toml", "pytest/tox.ini", "pytest/setup.cfg"},
		"ruff":    {"ruff/.ruff.toml", "python/pyproject.toml"},
		"ty":      {"ty/ty.toml", "python/pyproject.toml"},
		"uv":      {"uv/uv.lock", "uv/uv.toml", "python/pyproject.toml"},
	}
	for _, mod := range loadModuleRegistry() {
		expected, ok := want[mod.Name]
		if !ok {
			continue
		}
		t.Run(mod.Name, func(t *testing.T) {
			matches, err := mod.Recommend(ctx, ws)
			require.NoError(t, err)
			require.ElementsMatch(t, expected, matches)
		})
	}
}
