package core

// These tests cover the LLM skill system: engine-embedded skills, SKILL.md
// files discovered in the bound workspace, and skill directories installed
// with withSkills. Discovery is asserted through LLM.skills — the same index
// the list_skills tool serves the model — and the tool path itself is driven
// end to end with a canned replay conversation (see llm_test.go).

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// skillIndex returns the LLM's skill discovery index as name → description.
func skillIndex(ctx context.Context, t *testctx.T, llm *dagger.LLM) map[string]string {
	t.Helper()
	skills, err := llm.Skills(ctx)
	require.NoError(t, err)
	idx := map[string]string{}
	for _, sk := range skills {
		name, err := sk.Name(ctx)
		require.NoError(t, err)
		desc, err := sk.Description(ctx)
		require.NoError(t, err)
		idx[name] = desc
	}
	return idx
}

func (LLMSuite) TestSkillsEngineEmbedded(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	skills := skillIndex(ctx, t, c.LLM())
	require.Contains(t, skills, "dang-language")
	require.NotEmpty(t, skills["dang-language"])
}

func (LLMSuite) TestSkillsWorkspaceDiscovery(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ws := c.Directory().
		// named by frontmatter, discoverable anywhere in the tree
		WithNewFile("docs/skills/publish/SKILL.md",
			"---\nname: publish\ndescription: How to publish this project.\n---\n\n# Publishing\n").
		// name falls back to the containing directory
		WithNewFile("guides/release-notes/SKILL.md",
			"---\ndescription: How to write release notes.\n---\n\n# Release notes\n").
		// not a well-formed skill; skipped rather than failing the listing
		WithNewFile("junk/SKILL.md", "# no frontmatter here\n").
		AsWorkspace()

	skills := skillIndex(ctx, t, c.LLM().WithWorkspace(ws))
	require.Equal(t, "How to publish this project.", skills["publish"])
	require.Equal(t, "How to write release notes.", skills["release-notes"])
	require.NotContains(t, skills, "junk")
	// engine-embedded skills remain visible alongside the workspace's
	require.Contains(t, skills, "dang-language")
}

func (LLMSuite) TestSkillsInstallDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	skillsDir := c.Directory().
		WithNewFile("deploy/SKILL.md",
			"---\ndescription: Installed deploy guidance.\n---\n\n# Deploy\n")
	// a directory that is itself a single skill, named by its frontmatter
	standalone := c.Directory().
		WithNewFile("SKILL.md",
			"---\nname: standalone\ndescription: A single skill at the directory root.\n---\n\n# Standalone\n")

	skills := skillIndex(ctx, t, c.LLM().WithSkills(skillsDir).WithSkills(standalone))
	require.Equal(t, "Installed deploy guidance.", skills["deploy"])
	require.Equal(t, "A single skill at the directory root.", skills["standalone"])
	require.Contains(t, skills, "dang-language")
}

func (LLMSuite) TestSkillsPrecedence(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ws := c.Directory().
		WithNewFile("skills/deploy/SKILL.md",
			"---\ndescription: From the workspace.\n---\nworkspace body").
		AsWorkspace()
	installed := c.Directory().
		WithNewFile("deploy/SKILL.md",
			"---\ndescription: From withSkills.\n---\ninstalled body").
		// engine-embedded skills cannot be shadowed
		WithNewFile("dang-language/SKILL.md",
			"---\ndescription: An impostor.\n---\nimpostor body")

	skills := skillIndex(ctx, t, c.LLM().WithWorkspace(ws).WithSkills(installed))
	require.Equal(t, "From withSkills.", skills["deploy"],
		"an installed skill should win a name collision with a workspace skill")
	require.NotEqual(t, "An impostor.", skills["dang-language"],
		"an installed skill must not shadow an engine-embedded skill")
}

// TestSkillsSurviveWorkspaceReset verifies that installed skill directories are
// selector-expressible state: withResetWorkspace re-emits them into the flat
// recipe, and they survive a portableID save/load round trip.
func (LLMSuite) TestSkillsSurviveWorkspaceReset(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	installed := c.Directory().
		WithNewFile("deploy/SKILL.md",
			"---\ndescription: Installed deploy guidance.\n---\nbody")
	reset := c.LLM().WithSkills(installed).WithResetWorkspace()

	skills := skillIndex(ctx, t, reset)
	require.Equal(t, "Installed deploy guidance.", skills["deploy"])

	portableID, err := reset.PortableID(ctx)
	require.NoError(t, err)
	reloaded := dagger.Ref[*dagger.LLM](c, portableID)
	skills = skillIndex(ctx, t, reloaded)
	require.Equal(t, "Installed deploy guidance.", skills["deploy"])
}

// TestSkillTools drives the real list_skills/read_skill tools through a canned
// replay conversation: the recorded tool results are placeholders, so any
// skill content appearing in the transcript is the live result of dispatching
// the tools against the installed skill directory.
func (LLMSuite) TestSkillTools(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	skillsDir := c.Directory().
		WithNewFile("deploy/SKILL.md",
			"---\nname: deploy\ndescription: How to deploy this project.\n---\n\n# Deploying\nFollow reference/checklist.md before shipping.\n").
		WithNewFile("deploy/reference/checklist.md",
			"# Checklist\n- run the tests\n- ship it\n")

	prompt := "How do I deploy this project?"
	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt(prompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Let me see what skills are available."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "list_skills", Arguments: dagger.JSON(`{}`)},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "There is a deploy skill. Let me read it."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "read_skill", Arguments: dagger.JSON(`{"name":"deploy"}`)},
		}).
		WithToolResult("call_2", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "It points to a checklist."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_3", ToolName: "read_skill", Arguments: dagger.JSON(`{"name":"deploy","file":"reference/checklist.md"}`)},
		}).
		WithToolResult("call_3", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Run the tests, then ship it."},
		}))

	llm := c.LLM().
		WithModel(model).
		WithSkills(skillsDir).
		WithPrompt(prompt).
		Loop()

	transcript, err := llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "How to deploy this project.",
		"the live list_skills result should carry the installed skill's description")
	require.Contains(t, transcript, "# Deploying",
		"the live read_skill result should carry the SKILL.md body")
	require.Contains(t, transcript, "- ship it",
		"the live read_skill result should carry the reference file")

	reply, err := llm.LastReply(ctx)
	require.NoError(t, err)
	require.Equal(t, "Run the tests, then ship it.", reply)
}
