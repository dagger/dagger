// A module that builds a skills directory INSIDE a module function, so the
// calls that build it are made from the module's own nested session rather
// than by the client that consumes it. Used by
// core/integration/agent_roster_skills_test.go to bind a skills directory
// into an agent's seed (LLM.withSkills) and ask whether the client can still
// rebuild the agent's ID from telemetry.
package main

import (
	"dagger/skiller/internal/dagger"
)

// SkillDoc is the one skill the directory holds. Asserted verbatim by
// core/integration/agent_roster_skills_test.go, so the frame that carries it
// is identifiable in a rebuilt chain.
const SkillDoc = `---
name: greeter
description: Greet somebody, from inside a module.
---

Say hello.
`

type Skiller struct{}

// Skills returns a skills directory built entirely inside this module.
func (m *Skiller) Skills() *dagger.Directory {
	return dag.Directory().WithNewFile("greeter/SKILL.md", SkillDoc)
}
