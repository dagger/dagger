package core

import (
	"context"
	"testing"
	"testing/fstest"

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
	require.Len(t, sources, 4)
	require.IsType(t, embeddedSkillSource{}, sources[0])
	require.IsType(t, directorySkillSource{}, sources[1])
	require.IsType(t, directorySkillSource{}, sources[2])
	require.IsType(t, workspaceSkillSource{}, sources[3])
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
