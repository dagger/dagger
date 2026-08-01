// Package skills embeds the Dagger-authored skills the engine serves to models
// via list_skills/read_skill (see core/llm_skills.go). Each skill is a
// subdirectory holding a SKILL.md plus optional reference files, mirroring the
// layout of the dang-language skill embedded from the upstream dang repo.
package skills

import "embed"

//go:embed all:dang-dagger-modules
var FS embed.FS
