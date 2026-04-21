// Package llbtodagger converts BuildKit LLB definitions into Dagger call IDs.
//
// The conversion is intentionally strict: if an LLB operation cannot be mapped
// faithfully to Dagger API fields/arguments, conversion returns an error.
//
// Non-goals:
//   - Parsing Dockerfiles or any frontend input format directly.
//   - Supporting BuildOp, blob://, or custom-op conversion paths.
//   - Best-effort fallback when semantics are missing or lossy.
package llbtodagger
