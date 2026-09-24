package core

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

func TestParseSkillFrontmatter(t *testing.T) {
	t.Run("name and description", func(t *testing.T) {
		fm, err := parseSkillFrontmatter([]byte("---\nname: dang-language\ndescription: Teaches Dang.\n---\n\n# Body\n"))
		require.NoError(t, err)
		require.Equal(t, "dang-language", fm.Name)
		require.Equal(t, "Teaches Dang.", fm.Description)
	})

	t.Run("description with a colon and em dash", func(t *testing.T) {
		fm, err := parseSkillFrontmatter([]byte("---\ndescription: \"Use when: authoring — reviewing\"\n---\nbody"))
		require.NoError(t, err)
		require.Equal(t, "Use when: authoring — reviewing", fm.Description)
	})

	t.Run("tolerates a leading BOM", func(t *testing.T) {
		fm, err := parseSkillFrontmatter([]byte("\ufeff---\ndescription: x\n---\nbody"))
		require.NoError(t, err)
		require.Equal(t, "x", fm.Description)
	})

	t.Run("missing frontmatter", func(t *testing.T) {
		_, err := parseSkillFrontmatter([]byte("# Just a heading\n"))
		require.Error(t, err)
	})

	t.Run("unterminated frontmatter", func(t *testing.T) {
		_, err := parseSkillFrontmatter([]byte("---\ndescription: x\n"))
		require.Error(t, err)
	})

	t.Run("terminator must be exactly ---", func(t *testing.T) {
		// A ---- rule (or any line merely starting with ---) does not close
		// the block, so this frontmatter is unterminated.
		_, err := parseSkillFrontmatter([]byte("---\ndescription: x\n----\nbody"))
		require.Error(t, err)
	})

	t.Run("terminator as the final line without a newline", func(t *testing.T) {
		fm, err := parseSkillFrontmatter([]byte("---\ndescription: x\n---"))
		require.NoError(t, err)
		require.Equal(t, "x", fm.Description)
	})

	t.Run("CRLF line endings", func(t *testing.T) {
		fm, err := parseSkillFrontmatter([]byte("---\r\ndescription: x\r\n---\r\nbody"))
		require.NoError(t, err)
		require.Equal(t, "x", fm.Description)
	})
}

func TestSkillDescription(t *testing.T) {
	_, err := skillDescription([]byte("---\nname: x\n---\nbody"))
	require.Error(t, err, "description is required")

	desc, err := skillDescription([]byte("---\ndescription: hi\n---\nbody"))
	require.NoError(t, err)
	require.Equal(t, "hi", desc)
}

func TestSkillFilePath(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"", "SKILL.md"},
		{".", "SKILL.md"},
		{"SKILL.md", "SKILL.md"},
		{"reference/objects.md", "reference/objects.md"},
		{"/reference/objects.md", "reference/objects.md"},
		{"../../etc/passwd", "etc/passwd"},            // traversal pinned inside the skill dir
		{"reference/../SKILL.md", "SKILL.md"},         // collapses to a safe path
		{"../../../reference/x.md", "reference/x.md"}, // never escapes upward
	} {
		got := skillFilePath(tc.in)
		require.Equal(t, tc.want, got, "input %q", tc.in)
	}
}

func testSkillsFS() fstest.MapFS {
	return fstest.MapFS{
		"dang-language/SKILL.md":             {Data: []byte("---\nname: dang-language\ndescription: Teaches Dang.\n---\n\n# Dang\n")},
		"dang-language/reference/objects.md": {Data: []byte("# Objects\n")},
		"builtin-dsl/SKILL.md":               {Data: []byte("---\nname: builtin-dsl\ndescription: Contributor skill.\n---\n")},
	}
}

func TestEmbeddedSkillSource(t *testing.T) {
	ctx := context.Background()
	src := embeddedSkillSource{fsys: testSkillsFS(), allow: []string{"dang-language"}}

	t.Run("list surfaces only allowlisted skills", func(t *testing.T) {
		metas, err := src.list(ctx)
		require.NoError(t, err)
		require.Len(t, metas, 1)
		require.Equal(t, "dang-language", metas[0].Name)
		require.Equal(t, "Teaches Dang.", metas[0].Description)
	})

	t.Run("read defaults to SKILL.md", func(t *testing.T) {
		content, err := src.read(ctx, "dang-language", "")
		require.NoError(t, err)
		require.Contains(t, content, "# Dang")
	})

	t.Run("read a reference file", func(t *testing.T) {
		content, err := src.read(ctx, "dang-language", "reference/objects.md")
		require.NoError(t, err)
		require.Contains(t, content, "# Objects")
	})

	t.Run("non-allowlisted skill is not found", func(t *testing.T) {
		_, err := src.read(ctx, "builtin-dsl", "")
		require.ErrorIs(t, err, errSkillNotFound)
	})

	t.Run("missing file errors", func(t *testing.T) {
		_, err := src.read(ctx, "dang-language", "reference/nope.md")
		require.Error(t, err)
		require.NotErrorIs(t, err, errSkillNotFound)
	})
}

func TestListSkillsDedupAndSort(t *testing.T) {
	ctx := context.Background()
	first := embeddedSkillSource{fsys: fstest.MapFS{
		"b/SKILL.md": {Data: []byte("---\ndescription: from-first\n---\n")},
	}, allow: []string{"b"}}
	second := embeddedSkillSource{fsys: fstest.MapFS{
		"a/SKILL.md": {Data: []byte("---\ndescription: a\n---\n")},
		"b/SKILL.md": {Data: []byte("---\ndescription: from-second\n---\n")},
	}, allow: []string{"a", "b"}}

	metas, err := listSkills(ctx, []skillSource{first, second})
	require.NoError(t, err)
	require.Equal(t, []*LLMSkill{
		{Name: "a", Description: "a"},
		{Name: "b", Description: "from-first"}, // earlier source wins the collision
	}, metas)
}

func TestSkillDescriptionExcerpt(t *testing.T) {
	for _, tc := range []struct {
		name, description, want string
	}{
		{"empty", "", ""},
		{"whitespace", "  Use when\n running\t tests.\r\n", "Use when running tests."},
		{"abbreviations and paths", "Use e.g. core/llm.go and dagger.json. Read more.", "Use e.g. core/llm.go and dagger.json. Read more."},
		{"at limit", strings.Repeat("x", 160), strings.Repeat("x", 160)},
		{"long token", strings.Repeat("x", 161), strings.Repeat("x", 159) + "…"},
		{"word boundary", strings.Repeat("word ", 40), strings.TrimSpace(strings.Repeat("word ", 32)) + "…"},
		{"partial word", strings.Repeat("word ", 31) + "longword", strings.TrimSpace(strings.Repeat("word ", 31)) + "…"},
		{"unicode", strings.Repeat("界", 161), strings.Repeat("界", 159) + "…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := skillDescriptionExcerpt(tc.description)
			require.Equal(t, tc.want, got)
			require.True(t, utf8.ValidString(got))
			require.LessOrEqual(t, utf8.RuneCountInString(got), 160)
		})
	}
}

func TestRenderSkillList(t *testing.T) {
	require.Equal(t, "No skills available.", renderSkillList(nil))
	name := "skill-" + strings.Repeat("long-name-", 20)
	description := "Use when testing. " + strings.Repeat("Detailed trigger. ", 40)
	content := "---\ndescription: " + description + "\n---\nFull guidance.\n"
	src := embeddedSkillSource{
		fsys:  fstest.MapFS{name + "/SKILL.md": {Data: []byte(content)}},
		allow: []string{name},
	}
	skills, err := listSkills(t.Context(), []skillSource{src})
	require.NoError(t, err)
	out := renderSkillList(skills)
	require.Contains(t, out, "ReadSkill gives full guidance and triggers")
	require.Contains(t, out, "\n- "+name+": Use when testing.")
	require.True(t, strings.HasSuffix(out, "…"))
	require.Equal(t, strings.TrimSpace(description), skills[0].Description,
		"rendering must not mutate LLM.skills metadata")
	full, err := readSkill(t.Context(), []skillSource{src}, name, "")
	require.NoError(t, err)
	require.Equal(t, content, full, "ReadSkill must preserve all triggers and guidance")
}

// Keep the actual repository's roster discoverable within a small tool result,
// including engine-debugging, which was hidden by verbose JSON descriptions.
func TestRenderRepositorySkillList(t *testing.T) {
	sources := []skillSource{engineSkills, daggerSkills}
	root := os.DirFS("..")
	for _, pattern := range []string{"skills/*/SKILL.md", ".dagger/modules/*/skills/*/SKILL.md"} {
		paths, err := fs.Glob(root, pattern)
		require.NoError(t, err)
		if len(paths) == 0 {
			t.Skip("requires the full repository; engine-dev filters out workspace skills")
		}
		for _, p := range paths {
			dir := path.Dir(p)
			fsys, err := fs.Sub(root, path.Dir(dir))
			require.NoError(t, err)
			sources = append(sources, embeddedSkillSource{fsys: fsys, allow: []string{path.Base(dir)}})
		}
	}
	skills, err := listSkills(t.Context(), sources)
	require.NoError(t, err)
	out := renderSkillList(skills)
	for _, skill := range skills {
		require.Contains(t, out, "\n- "+skill.Name+": ")
	}
	require.Contains(t, out, "- engine-debugging: Run Dagger repo tests and debug Dagger engine")
	require.Less(t, len(out), 3072, "the current roster should fit in a 3 KiB result")
	full, err := json.Marshal(skills)
	require.NoError(t, err)
	t.Logf("%d skills: full JSON %d bytes, compact index %d bytes:\n%s", len(skills), len(full), len(out), out)
}

func TestReadSkillFallthrough(t *testing.T) {
	ctx := context.Background()
	src := embeddedSkillSource{fsys: testSkillsFS(), allow: []string{"dang-language"}}

	content, err := readSkill(ctx, []skillSource{src}, "dang-language", "")
	require.NoError(t, err)
	require.Contains(t, content, "# Dang")

	_, err = readSkill(ctx, []skillSource{src}, "nope", "")
	require.ErrorContains(t, err, "unknown skill")

	_, err = readSkill(ctx, []skillSource{src}, "", "")
	require.ErrorContains(t, err, "required")
}

func TestResolveSkillManifest(t *testing.T) {
	t.Run("name from frontmatter", func(t *testing.T) {
		sk, ok := resolveSkillManifest(
			".agents/skills/deploy/SKILL.md",
			[]byte("---\nname: deployer\ndescription: How to deploy.\n---\nbody"),
		)
		require.True(t, ok)
		require.Equal(t, "deployer", sk.name)
		require.Equal(t, ".agents/skills/deploy", sk.dir)
		require.Equal(t, "How to deploy.", sk.description)
	})

	t.Run("name falls back to the directory base", func(t *testing.T) {
		sk, ok := resolveSkillManifest(
			"skills/my-skill/SKILL.md",
			[]byte("---\ndescription: no name here\n---\nbody"),
		)
		require.True(t, ok)
		require.Equal(t, "my-skill", sk.name)
		require.Equal(t, "skills/my-skill", sk.dir)
	})

	t.Run("root SKILL.md is named by its frontmatter", func(t *testing.T) {
		sk, ok := resolveSkillManifest(
			"SKILL.md",
			[]byte("---\nname: standalone\ndescription: A whole directory as one skill.\n---\nbody"),
		)
		require.True(t, ok)
		require.Equal(t, "standalone", sk.name)
		require.Equal(t, ".", sk.dir)
	})

	t.Run("rejects a root SKILL.md without a frontmatter name", func(t *testing.T) {
		// A root-level SKILL.md has no containing directory to name it after.
		_, ok := resolveSkillManifest("SKILL.md", []byte("---\ndescription: nameless\n---\nbody"))
		require.False(t, ok)
	})

	t.Run("rejects a file without frontmatter", func(t *testing.T) {
		_, ok := resolveSkillManifest("skills/x/SKILL.md", []byte("# no frontmatter\n"))
		require.False(t, ok)
	})
}

// TestSkillSourcesOrder guards the precedence of skill origins: engine-embedded
// skills cannot be shadowed, explicitly installed directories (LLM.withSkills)
// win over skills discovered in the workspace, and installed directories are
// consulted in install order.
func TestSkillSourcesOrder(t *testing.T) {
	m := newMCP().
		WithSkills(dagql.ObjectResult[*Directory]{}).
		WithSkills(dagql.ObjectResult[*Directory]{})
	sources := m.skillSources()
	require.Len(t, sources, 5)
	require.IsType(t, embeddedSkillSource{}, sources[0])
	require.IsType(t, embeddedSkillSource{}, sources[1])
	require.IsType(t, directorySkillSource{}, sources[2])
	require.IsType(t, directorySkillSource{}, sources[3])
	require.IsType(t, workspaceSkillSource{}, sources[4])
}

func TestLLMSkillOwnership(t *testing.T) {
	ctx := llmTestContext()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, LLMTestQuery{})
	srv.InstallObject(dagql.NewClass[*Directory](srv))
	dir := newTypeDefAttachedResult(t, ctx, cache, srv, "skills", &Directory{})
	replacement := newTypeDefAttachedResult(t, ctx, cache, srv, "attached-skills", &Directory{})
	base, err := (&Query{}).NewLLM(ctx, "test-model", "")
	require.NoError(t, err)
	base = base.WithSkills(dir).
		WithSkillsOwner(dir, "outer").
		WithSkillsOwner(dir, "inner").
		WithSkillsOwner(dir, "outer")
	owners := func(llm *LLM) []string {
		var owners []string
		for _, dir := range llm.mcp.skillDirs {
			owners = append(owners, dir.Owner)
		}
		return owners
	}
	require.Equal(t, []string{"", "outer", "inner", "outer"}, owners(base))
	require.Equal(t, owners(base), owners(base.WithoutComposition("")))
	removed := base.WithoutComposition("outer")
	require.Equal(t, []string{"", "inner"}, owners(removed))
	require.Len(t, base.mcp.skillDirs, 4, "removal must not mutate the base")
	require.Len(t, removed.mcp.skillSources(), 5, "unowned and nested module skills survive")

	clone := base.Clone()
	deps, err := clone.AttachDependencyResults(ctx, nil, func(res dagql.AnyResult) (dagql.AnyResult, error) {
		attached, ok := res.(dagql.ObjectResult[*Directory])
		require.True(t, ok)
		require.Same(t, dir.Self(), attached.Self())
		return replacement, nil
	})
	require.NoError(t, err)
	require.Len(t, deps, 4)
	require.Equal(t, owners(base), owners(clone), "attachment retains owners")
	for i := range base.mcp.skillDirs {
		require.Same(t, dir.Self(), base.mcp.skillDirs[i].Directory.Self())
		require.Same(t, replacement.Self(), clone.mcp.skillDirs[i].Directory.Self())
	}
}

// TestEngineSkills checks the real embedded source: the dang-language skill is
// exposed with a description and its reference files are readable, while the
// compiler-contributor skills are curated out.
func TestEngineSkills(t *testing.T) {
	ctx := context.Background()

	metas, err := engineSkills.list(ctx)
	require.NoError(t, err)
	names := make([]string, len(metas))
	for i, m := range metas {
		names[i] = m.Name
		assert.NotEmpty(t, m.Description, "skill %q should have a description", m.Name)
	}
	require.Contains(t, names, "dang-language")
	require.NotContains(t, names, "builtin-dsl")
	require.NotContains(t, names, "dang-internals")

	skill, err := engineSkills.read(ctx, "dang-language", "")
	require.NoError(t, err)
	require.Contains(t, skill, "Dang")

	ref, err := engineSkills.read(ctx, "dang-language", "reference/objects.md")
	require.NoError(t, err)
	require.NotEmpty(t, ref)

	_, err = engineSkills.read(ctx, "builtin-dsl", "")
	require.ErrorIs(t, err, errSkillNotFound)
}

// TestDaggerSkills checks the Dagger-authored embedded source: the
// dang-dagger-modules skill is exposed with a description and readable.
func TestDaggerSkills(t *testing.T) {
	ctx := context.Background()

	metas, err := daggerSkills.list(ctx)
	require.NoError(t, err)
	names := make([]string, len(metas))
	for i, m := range metas {
		names[i] = m.Name
		assert.NotEmpty(t, m.Description, "skill %q should have a description", m.Name)
	}
	require.Contains(t, names, "dang-dagger-modules")

	skill, err := daggerSkills.read(ctx, "dang-dagger-modules", "")
	require.NoError(t, err)
	require.Contains(t, skill, "self-call")

	_, err = daggerSkills.read(ctx, "dang-language", "")
	require.ErrorIs(t, err, errSkillNotFound)
}
