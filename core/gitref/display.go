package gitref

import "strings"

// LooksRemote distinguishes a host/path reference from a missing local path.
// Callers must check for an existing local directory before using this hint.
func LooksRemote(ref string) bool {
	if FastKindCheck(ref, "") == KindLocal {
		return false
	}
	if Scheme(ref) != NoScheme {
		return true
	}
	host, _, hasPath := strings.Cut(ref, "/")
	return hasPath && strings.Contains(host, ".")
}

// DisplayRef keeps the user's spelling while removing URL credentials. HTTP
// usernames can themselves be tokens. SSH usernames are part of the address.
func DisplayRef(ref string) string {
	scheme := Scheme(ref)
	start := len(scheme.Prefix())
	end := strings.IndexByte(ref[start:], '/')
	if end < 0 {
		end = len(ref)
	} else {
		end += start
	}
	if at := strings.LastIndexByte(ref[start:end], '@'); at >= 0 {
		at += start
		user := ref[start:at]
		if scheme != SchemeSSH && scheme != SchemeSCPLike {
			ref = ref[:start] + "***" + ref[at:]
		} else if colon := strings.IndexByte(user, ':'); colon >= 0 {
			ref = ref[:start+colon+1] + "***" + ref[at:]
		}
	}
	if query := strings.IndexByte(ref, '?'); query >= 0 {
		_, fragment, hasFragment := strings.Cut(ref[query:], "#")
		ref = ref[:query] + "?***"
		if hasFragment {
			ref += "#" + fragment
		}
	}
	// A module path must not inject extra terminal lines or control sequences.
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '\uFFFD'
		}
		return r
	}, ref)
}
