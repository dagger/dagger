package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	dangskills "github.com/vito/dang/v2/.agents/skills"
	"gopkg.in/yaml.v3"

	"github.com/dagger/dagger/dagql"
)

// Skills are task-specific guidance (a SKILL.md plus optional reference files)
// that the model discovers with list_skills and reads with read_skill. This gives
// it progressive disclosure: it sees a one-line description per skill, opens the
// SKILL.md when relevant, and follows that skill's own routing into reference
// files only as needed — nothing but a name+description index is ever forced into
// context. This supersedes the old "glob **/SKILL.md via dang_eval" convention:
// the model can't read a Dang-teaching skill through a Dang eval it doesn't yet
// know how to write.
//
// Skills come from ordered sources behind a common skillSource interface:
// engine-embedded skills (the dang-language skill shipped in the engine), skill
// directories installed via LLM.withSkills, and SKILL.md files discovered in the
// bound workspace.

// errSkillNotFound signals that a source does not provide the requested skill, so
// read_skill should consult the next source rather than fail.
var errSkillNotFound = errors.New("skill not found")

// LLMSkill is the discovery-time view of a skill: enough for the model to decide
// whether to open it. It is exactly what the list_skills tool serves, and is
// also exposed over the API as LLM.skills so callers can see the model's view.
type LLMSkill struct {
	Name        string `field:"true" json:"name" doc:"The skill name, as passed to read_skill."`
	Description string `field:"true" json:"description" doc:"The one-line description from the SKILL.md frontmatter."`
}

func (*LLMSkill) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LLMSkill",
		NonNull:   true,
	}
}

func (*LLMSkill) TypeDescription() string {
	return "A skill available to a model: task-specific guidance discovered with list_skills and read with read_skill."
}

// skillSource enumerates and reads skills from one origin.
type skillSource interface {
	// list returns discovery metadata for every skill this source exposes.
	list(ctx context.Context) ([]*LLMSkill, error)
	// read returns the contents of file rel within skill name (rel defaults to
	// SKILL.md when empty). It returns errSkillNotFound if this source does not
	// provide the skill, and other errors for a bad path or read failure.
	read(ctx context.Context, name, rel string) (string, error)
}

// skillSources returns the ordered skill origins consulted by list_skills and
// read_skill. Engine-embedded skills come first, so the dang-language skill
// cannot be shadowed; directories installed explicitly via LLM.withSkills come
// before skills discovered in the bound workspace, so an installed skill wins a
// name collision with an ambient one.
func (m *MCP) skillSources() []skillSource {
	sources := []skillSource{engineSkills}
	for _, dir := range m.skillDirs {
		sources = append(sources, directorySkillSource{m: m, dir: dir})
	}
	return append(sources, workspaceSkillSource{m: m})
}

// engineSkills exposes the engine-embedded skills. Only skills useful for writing
// Dang against a workspace are surfaced; the compiler-contributor skills in the
// embed (builtin-dsl, dang-internals, editor-syntaxes, testing) are kept out of
// the model's view via the allowlist.
var engineSkills = embeddedSkillSource{
	fsys:  dangskills.FS,
	allow: []string{"dang-language"},
}

// embeddedSkillSource serves an allowlisted subset of an embedded FS rooted at a
// skills directory, where each skill is a subdirectory (name/SKILL.md,
// name/reference/*.md).
type embeddedSkillSource struct {
	fsys  fs.FS
	allow []string
}

func (s embeddedSkillSource) allowed(name string) bool {
	for _, a := range s.allow {
		if a == name {
			return true
		}
	}
	return false
}

func (s embeddedSkillSource) list(context.Context) ([]*LLMSkill, error) {
	metas := make([]*LLMSkill, 0, len(s.allow))
	for _, name := range s.allow {
		content, err := fs.ReadFile(s.fsys, path.Join(name, "SKILL.md"))
		if err != nil {
			return nil, fmt.Errorf("read skill %q: %w", name, err)
		}
		desc, err := skillDescription(content)
		if err != nil {
			return nil, fmt.Errorf("skill %q: %w", name, err)
		}
		metas = append(metas, &LLMSkill{Name: name, Description: desc})
	}
	return metas, nil
}

func (s embeddedSkillSource) read(_ context.Context, name, rel string) (string, error) {
	if !s.allowed(name) {
		return "", errSkillNotFound
	}
	rel = skillFilePath(rel)
	content, err := fs.ReadFile(s.fsys, path.Join(name, rel))
	if err != nil {
		return "", fmt.Errorf("read %q from skill %q: %w", rel, name, err)
	}
	return string(content), nil
}

// skillFilePath normalizes a caller-supplied path relative to a skill directory,
// defaulting to SKILL.md and neutralizing any traversal out of the skill's
// subtree (the leading-slash Clean pins the result inside the directory).
func skillFilePath(rel string) string {
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	if rel == "" || rel == "." {
		return "SKILL.md"
	}
	return rel
}

// skillGlob finds skills anywhere in a tree — the bound workspace or an
// installed skill directory — matching the prior "glob **/SKILL.md" convention.
const skillGlob = "**/SKILL.md"

// discoveredSkill is a skill found by globbing a tree for SKILL.md files: its
// resolved name, the directory holding its SKILL.md, and its description.
type discoveredSkill struct {
	name        string
	dir         string
	description string
}

// resolveSkillManifest turns a SKILL.md path and its contents into a named
// skill: the name is the frontmatter name, falling back to the containing
// directory's base name. It reports false for a file without valid frontmatter,
// or one whose name cannot be resolved (a root-level SKILL.md with no
// frontmatter name has no directory to name it after).
func resolveSkillManifest(skillPath string, content []byte) (discoveredSkill, bool) {
	fm, err := parseSkillFrontmatter(content)
	if err != nil {
		return discoveredSkill{}, false
	}
	dir := path.Dir(skillPath)
	name := fm.Name
	if name == "" {
		name = path.Base(dir)
	}
	if name == "" || name == "." || name == "/" {
		return discoveredSkill{}, false
	}
	return discoveredSkill{name: name, dir: dir, description: fm.Description}, true
}

// enumerateSkillDir globs a directory for SKILL.md files and resolves each to a
// named skill. Files that are not well-formed skills are skipped rather than
// failing the whole listing. The first skill wins a name collision.
func enumerateSkillDir(ctx context.Context, srv *dagql.Server, dir dagql.ObjectResult[*Directory]) (map[string]discoveredSkill, error) {
	var paths []string
	if err := srv.Select(ctx, dir, &paths, dagql.Selector{
		Field: "glob",
		Args:  []dagql.NamedInput{{Name: "pattern", Value: dagql.NewString(skillGlob)}},
	}); err != nil {
		return nil, fmt.Errorf("glob skills: %w", err)
	}
	skills := map[string]discoveredSkill{}
	for _, p := range paths {
		content, err := readSkillFile(ctx, srv, dir, p)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", p, err)
		}
		sk, ok := resolveSkillManifest(p, []byte(content))
		if !ok {
			continue
		}
		if _, exists := skills[sk.name]; exists {
			continue
		}
		skills[sk.name] = sk
	}
	return skills, nil
}

// listDiscoveredSkills renders an enumeration as discovery metadata.
func listDiscoveredSkills(skills map[string]discoveredSkill) []*LLMSkill {
	metas := make([]*LLMSkill, 0, len(skills))
	for _, sk := range skills {
		metas = append(metas, &LLMSkill{Name: sk.name, Description: sk.description})
	}
	return metas
}

// directorySkillSource surfaces SKILL.md files from a skill directory installed
// via LLM.withSkills. Both enumeration and reads go through the directory
// itself: unlike the workspace source there is no host sync to scope, the
// directory is already content-addressed.
type directorySkillSource struct {
	m   *MCP
	dir dagql.ObjectResult[*Directory]
}

func (s directorySkillSource) enumerate(ctx context.Context) (map[string]discoveredSkill, error) {
	srv, err := s.m.Server(ctx)
	if err != nil {
		return nil, err
	}
	return enumerateSkillDir(ctx, srv, s.dir)
}

func (s directorySkillSource) list(ctx context.Context) ([]*LLMSkill, error) {
	skills, err := s.enumerate(ctx)
	if err != nil {
		return nil, err
	}
	return listDiscoveredSkills(skills), nil
}

func (s directorySkillSource) read(ctx context.Context, name, rel string) (string, error) {
	skills, err := s.enumerate(ctx)
	if err != nil {
		return "", err
	}
	sk, ok := skills[name]
	if !ok {
		return "", errSkillNotFound
	}
	rel = skillFilePath(rel)
	srv, err := s.m.Server(ctx)
	if err != nil {
		return "", err
	}
	return readSkillFile(ctx, srv, s.dir, path.Join(sk.dir, rel))
}

// workspaceSkillSource surfaces SKILL.md files shipped in the LLM's bound
// workspace. It enumerates them by globbing the workspace source directory and
// reads their contents through the workspace-scoped dagql server, so a workspace
// can carry its own task-specific guidance alongside the engine's skills.
type workspaceSkillSource struct {
	m *MCP
}

// enumerate globs the bound workspace for SKILL.md files and resolves each to a
// named skill (frontmatter name, else the containing directory's base name). It
// returns nil when no workspace is bound.
func (s workspaceSkillSource) enumerate(ctx context.Context) (map[string]discoveredSkill, error) {
	if s.m.workspace.Self() == nil {
		return nil, nil
	}
	srv, err := s.m.Server(ctx)
	if err != nil {
		return nil, err
	}
	srcDir, err := s.skillIndexDir(ctx, srv)
	if err != nil {
		return nil, err
	}
	return enumerateSkillDir(ctx, srv, srcDir)
}

func (s workspaceSkillSource) list(ctx context.Context) ([]*LLMSkill, error) {
	skills, err := s.enumerate(ctx)
	if err != nil {
		return nil, err
	}
	return listDiscoveredSkills(skills), nil
}

func (s workspaceSkillSource) read(ctx context.Context, name, rel string) (string, error) {
	skills, err := s.enumerate(ctx)
	if err != nil {
		return "", err
	}
	sk, ok := skills[name]
	if !ok {
		return "", errSkillNotFound
	}
	rel = skillFilePath(rel)
	srv, err := s.m.Server(ctx)
	if err != nil {
		return "", err
	}
	// Load just the requested file — Workspace.file scopes the host sync to that
	// single path rather than syncing the whole skill directory or workspace tree.
	return readSkillFile(ctx, srv, s.m.workspace, path.Join(sk.dir, rel))
}

// skillIndexDir resolves a directory containing only the workspace's SKILL.md
// manifests, via the workspace's own directory(...) resolver with an include
// filter. Resolving through the resolver (not the in-memory SourceDirectory,
// which is populated only for value workspaces) materializes a local
// host-backed workspace; the include filter keeps the host sync to the skill
// manifests instead of slurping the whole workspace tree.
func (s workspaceSkillSource) skillIndexDir(ctx context.Context, srv *dagql.Server) (dagql.ObjectResult[*Directory], error) {
	var dir dagql.ObjectResult[*Directory]
	err := srv.Select(ctx, s.m.workspace, &dir, dagql.Selector{
		Field: "directory",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(".")},
			{Name: "include", Value: dagql.ArrayInput[dagql.String]{dagql.String(skillGlob)}},
		},
	})
	return dir, err
}

// readSkillFile reads a file's contents from a skill-bearing receiver — a
// Directory or the Workspace itself — via recv.file(path).contents. On the
// Workspace the file(...) resolver scopes the host sync to that single path.
func readSkillFile(ctx context.Context, srv *dagql.Server, recv dagql.AnyObjectResult, p string) (string, error) {
	var contents string
	err := srv.Select(ctx, recv, &contents,
		dagql.Selector{
			Field: "file",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(p)}},
		},
		dagql.Selector{Field: "contents"},
	)
	if err != nil {
		return "", err
	}
	return contents, nil
}

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// skillDescription extracts the description from a SKILL.md's YAML frontmatter —
// the one line the model reads in list_skills to decide whether to open the skill.
func skillDescription(content []byte) (string, error) {
	fm, err := parseSkillFrontmatter(content)
	if err != nil {
		return "", err
	}
	if fm.Description == "" {
		return "", fmt.Errorf("no description in SKILL.md frontmatter")
	}
	return fm.Description, nil
}

// parseSkillFrontmatter reads the leading `---` ... `---` YAML block of a SKILL.md.
func parseSkillFrontmatter(content []byte) (skillFrontmatter, error) {
	var fm skillFrontmatter
	s := strings.TrimPrefix(string(content), "\ufeff")
	nl := strings.IndexByte(s, '\n')
	if nl < 0 || strings.TrimRight(s[:nl], "\r") != "---" {
		return fm, fmt.Errorf("missing frontmatter")
	}
	body := s[nl+1:]
	end := strings.Index(body, "\n---")
	if end < 0 {
		return fm, fmt.Errorf("unterminated frontmatter")
	}
	if err := yaml.Unmarshal([]byte(body[:end]), &fm); err != nil {
		return fm, fmt.Errorf("parse frontmatter: %w", err)
	}
	return fm, nil
}

// listSkills gathers discovery metadata across all sources, earlier sources
// winning on a name collision, sorted by name for stable output.
func listSkills(ctx context.Context, sources []skillSource) ([]*LLMSkill, error) {
	var all []*LLMSkill
	var firstErr error
	seen := map[string]bool{}
	for _, src := range sources {
		metas, err := src.list(ctx)
		if err != nil {
			// A failing source (e.g. an unreadable workspace) shouldn't hide the
			// skills the other sources can offer; only surface the error if nothing
			// lists at all.
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, meta := range metas {
			if seen[meta.Name] {
				continue
			}
			seen[meta.Name] = true
			all = append(all, meta)
		}
	}
	if len(all) == 0 && firstErr != nil {
		return nil, firstErr
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all, nil
}

// readSkill returns a skill file's contents from the first source that provides
// the skill.
func readSkill(ctx context.Context, sources []skillSource, name, file string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("skill name is required")
	}
	for _, src := range sources {
		content, err := src.read(ctx, name, file)
		if err == nil {
			return content, nil
		}
		if !errors.Is(err, errSkillNotFound) {
			return "", err
		}
	}
	return "", fmt.Errorf("unknown skill %q — call list_skills to see what is available", name)
}

// loadSkillTools registers list_skills and read_skill, the progressive skill
// discovery/reading mechanism. Both are read-only.
func (m *MCP) loadSkillTools(srv *dagql.Server, allTools *LLMToolSet) {
	sources := m.skillSources()

	allTools.Add(LLMTool{
		Name: "list_skills",
		Description: "List available skills: task-specific guidance you can load " +
			"with read_skill. Each entry is a name and a one-line description; read " +
			"the ones whose description fits your task.",
		ReadOnly: true,
		Schema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"required":             []string{},
			"additionalProperties": false,
		},
		Strict: true,
		Call: ToolFunc(srv, func(ctx context.Context, _ struct{}) (any, error) {
			return listSkills(ctx, sources)
		}),
	})

	allTools.Add(LLMTool{
		Name: "read_skill",
		Description: "Read a skill's guidance. With just a name, returns its " +
			"SKILL.md (start there); pass file to load a reference file the SKILL.md " +
			"points to, e.g. read_skill(\"dang-language\", \"reference/objects.md\").",
		ReadOnly: true,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The skill name, as shown by list_skills.",
				},
				"file": map[string]any{
					"type": "string",
					"description": "Optional file within the skill, relative to its " +
						"directory. Defaults to SKILL.md.",
				},
			},
			"required":             []string{"name"},
			"additionalProperties": false,
		},
		Call: ToolFunc(srv, func(ctx context.Context, args struct {
			Name string
			File string `default:""`
		}) (any, error) {
			return readSkill(ctx, sources, args.Name, args.File)
		}),
	})
}
