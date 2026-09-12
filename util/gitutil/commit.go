package gitutil

func IsCommitSHA(str string) bool {
	return len(str) == 40 && isLowerHex(str)
}

// IsCommitSHAPrefix reports whether str could be an abbreviated commit SHA: a
// lowercase hex string between 4 characters (git's minimum abbreviation
// length) and a full 40-character SHA.
//
// A positive result only means the string is a plausible object-name prefix;
// whether it actually resolves depends on the repository's objects (see
// GitCLI.ResolveShortSHA).
func IsCommitSHAPrefix(str string) bool {
	return len(str) >= 4 && len(str) <= 40 && isLowerHex(str)
}

func isLowerHex(str string) bool {
	for _, ch := range str {
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch >= 'a' && ch <= 'f' {
			continue
		}
		return false
	}
	return true
}
