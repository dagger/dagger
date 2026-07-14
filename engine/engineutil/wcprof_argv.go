package engineutil

import "fmt"

// Caps on the emitted user-exec argv. They bound the dump and
// OTel attribute size; the group key (argv[0..1]) is never dropped or truncated.
const (
	maxProfArgvTokens     = 64
	maxProfArgvTokenBytes = 256
	maxProfArgvTotalBytes = 4 << 10 // 4 KiB
)

// profArgvSentinel replaces dropped trailing tokens. It begins with an ellipsis so
// no boundary-aware prefix or contains rule can mistake it for a real argument.
const profArgvSentinel = "…(+%d more)"

// execProfArgv returns the scrubbed + bounded user command for the just-finished
// exec, or nil when there is nothing to emit (no execMD, no captured ProfArgs, or a
// scrub failure). It runs at most once per exec — only when a profile source is
// active — and feeds BOTH the native recorder and the OTel span the SAME slice, so
// the two sources carry a byte-identical argv (cross-source parity).
func execProfArgv(state *execState) []string {
	if state.execMD == nil || len(state.execMD.ProfArgs) == 0 {
		return nil
	}
	// Redact the SAME registered-secret set the stdout/stderr scrubbers use (the
	// resolved secret file paths were stashed by setupSecretScrubbing). On any scrub
	// error, omit argv entirely — never block the exec, never risk an unscrubbed value.
	scrubbed, err := ScrubStrings(state.spec.Process.Env, state.execMD.SecretEnvNames, state.profSecretFilePaths, state.execMD.ProfArgs)
	if err != nil {
		return nil
	}
	return boundProfArgv(scrubbed)
}

// boundProfArgv caps argv for emission without ever altering argv[0..1] (the group
// key): tokens past the key that exceed the per-token byte cap are truncated, and
// excess trailing tokens are dropped and replaced by a single sentinel. Caps apply
// to the raw scrubbed slice (the JSON encoding is marginally larger, well within
// OTel attribute limits).
func boundProfArgv(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	capped := make([]string, len(argv))
	for i, tok := range argv {
		// Never byte-truncate argv[0..1]: basename(argv[0]) and the subcommand drive
		// the class, and truncating their tail would corrupt the program name.
		if i >= 2 && len(tok) > maxProfArgvTokenBytes {
			tok = tok[:maxProfArgvTokenBytes]
		}
		capped[i] = tok
	}
	// argv[0..1] are always retained, even if alone they exceed the budget.
	keep := min(len(capped), 2)
	total := 0
	for i := 0; i < keep; i++ {
		total += len(capped[i])
	}
	for keep < len(capped) {
		if keep >= maxProfArgvTokens || total+len(capped[keep]) > maxProfArgvTotalBytes {
			break
		}
		total += len(capped[keep])
		keep++
	}
	if keep >= len(capped) {
		return capped
	}
	out := make([]string, 0, keep+1)
	out = append(out, capped[:keep]...)
	out = append(out, fmt.Sprintf(profArgvSentinel, len(capped)-keep))
	return out
}
