package gitutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var (
	// ErrInvalidRevision reports a revision expression that is malformed or
	// uses git revision syntax that is not supported.
	ErrInvalidRevision = errors.New("invalid revision")
	// ErrRevisionHistoryUnavailable reports that a commit needed to walk a
	// revision's suffixes is not available locally, e.g. because the
	// repository is shallow.
	ErrRevisionHistoryUnavailable = errors.New("commit history not available")
)

// supportedRevisionForms is appended to revision syntax errors, so callers
// learn what they can write instead.
const supportedRevisionForms = "supported forms are a ref name or commit SHA followed by any number of ~, ~N, ^ or ^N suffixes (e.g. HEAD~3, main^2, abc1234~2^2)"

// RevisionStep is a single revision suffix.
type RevisionStep struct {
	// Op is '~' (follow first parents N times) or '^' (select the Nth
	// parent, 1-based; ^0 is the commit itself).
	Op byte
	N  int
}

func (step RevisionStep) String() string {
	return string(step.Op) + strconv.Itoa(step.N)
}

// Revision is a ref name or commit followed by ancestry suffixes, e.g.
// HEAD~3 or main^2~1.
type Revision struct {
	// Expr is the full expression as written.
	Expr string
	// Base is the ref name or commit the suffixes apply to.
	Base string
	// Steps are the suffixes, applied left to right.
	Steps []RevisionStep
}

// HasSuffix reports whether the revision walks anything from its base.
func (rev Revision) HasSuffix() bool {
	return len(rev.Steps) > 0
}

// Generations returns how many parent links the walk follows at most, i.e.
// how deep history must be available below the base commit. It saturates at
// math.MaxInt.
func (rev Revision) Generations() int {
	total := 0
	for _, step := range rev.Steps {
		n := step.N
		if step.Op == '^' && n > 0 {
			n = 1
		}
		if n > math.MaxInt-total {
			return math.MaxInt
		}
		total += n
	}
	return total
}

func invalidRevision(expr, format string, args ...any) error {
	return fmt.Errorf("%w %q: %s; %s", ErrInvalidRevision, expr, fmt.Sprintf(format, args...), supportedRevisionForms)
}

// ParseRevision parses a ref name optionally followed by revision suffixes:
// ~, ~N, ^ and ^N, chained left to right (e.g. HEAD~2^2~1).
//
// Git forbids ~, ^ and : in ref names (see git check-ref-format), so any name
// containing ~ or ^ is unambiguously a revision expression. Other revision
// syntax understood by git rev-parse (^{...} peeling, @{...} reflog and
// upstream selectors, :path, ^!, ^@, ^- and ranges) is rejected with
// ErrInvalidRevision rather than being passed along as a ref name. A plain
// name returns a Revision without steps.
func ParseRevision(expr string) (Revision, error) {
	rev := Revision{Expr: expr, Base: expr}
	switch {
	case strings.Contains(expr, "@{"):
		return rev, invalidRevision(expr, "@{...} reflog and upstream selectors are not supported")
	case strings.Contains(expr, ".."):
		return rev, invalidRevision(expr, "revision ranges are not supported")
	case strings.Contains(expr, ":"):
		return rev, invalidRevision(expr, ":path and :/search expressions are not supported")
	}

	idx := strings.IndexAny(expr, "~^")
	if idx < 0 {
		if expr == "@" {
			return rev, invalidRevision(expr, "@ as a shorthand for HEAD is not supported; use HEAD")
		}
		return rev, nil
	}

	rev.Base = expr[:idx]
	switch rev.Base {
	case "":
		if expr[0] == '^' {
			return rev, invalidRevision(expr, "^<rev> exclusions are not supported")
		}
		return rev, invalidRevision(expr, "missing ref name or commit before %q", expr[idx:])
	case "@":
		return rev, invalidRevision(expr, "@ as a shorthand for HEAD is not supported; use HEAD")
	}

	for rest := expr[idx:]; rest != ""; {
		op := rest[0]
		if op != '~' && op != '^' {
			return rev, invalidRevision(expr, "unexpected %q after %q", rest, expr[:len(expr)-len(rest)])
		}
		rest = rest[1:]
		if op == '^' && rest != "" {
			switch rest[0] {
			case '{':
				return rev, invalidRevision(expr, "^{...} peeling is not supported")
			case '!', '@', '-':
				return rev, invalidRevision(expr, "^%c is not supported", rest[0])
			}
		}
		digits := 0
		for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
			digits++
		}
		n := 1
		if digits > 0 {
			var err error
			n, err = strconv.Atoi(rest[:digits])
			if err != nil {
				return rev, invalidRevision(expr, "invalid count %q after %c", rest[:digits], op)
			}
			rest = rest[digits:]
		}
		rev.Steps = append(rev.Steps, RevisionStep{Op: op, N: n})
	}
	return rev, nil
}

// WalkRevision applies rev's suffixes starting from baseSHA, the commit its
// base resolved to, and returns the resulting commit SHA.
//
// Parents are read from the commit objects themselves, never through shallow
// grafts, so a walk cannot silently stop at a shallow boundary: a commit whose
// object is missing yields ErrRevisionHistoryUnavailable. baseSHA may name an
// annotated tag, which is peeled to its commit.
func (cli *GitCLI) WalkRevision(ctx context.Context, rev Revision, baseSHA string) (string, error) {
	peeled, err := cli.New(WithIgnoreError()).Run(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", baseSHA+"^{commit}")
	if err != nil {
		return "", err
	}
	cur := string(bytes.TrimSpace(peeled))
	if cur == "" {
		return "", fmt.Errorf("resolve %q: %w: commit %s for %q is not available locally", rev.Expr, ErrRevisionHistoryUnavailable, baseSHA, rev.Base)
	}

	at := rev.Base
	for _, step := range rev.Steps {
		switch step.Op {
		case '~':
			for i := range step.N {
				parents, err := cli.commitParents(ctx, rev, at, cur)
				if err != nil {
					return "", err
				}
				if len(parents) == 0 {
					return "", fmt.Errorf("resolve %q: %s (%s) is a root commit, cannot walk %d more first parent(s) past it",
						rev.Expr, revisionLabel(at, step.Op, i), cur, step.N-i)
				}
				cur = parents[0]
			}
		case '^':
			if step.N == 0 {
				break
			}
			parents, err := cli.commitParents(ctx, rev, at, cur)
			if err != nil {
				return "", err
			}
			if step.N > len(parents) {
				return "", fmt.Errorf("resolve %q: %s (%s) has %d parent(s), cannot select parent %d",
					rev.Expr, at, cur, len(parents), step.N)
			}
			cur = parents[step.N-1]
		default:
			return "", fmt.Errorf("resolve %q: unknown revision suffix %q", rev.Expr, step.Op)
		}
		at += step.String()
	}
	return cur, nil
}

// revisionLabel names the commit reached i generations into a ~ step.
func revisionLabel(at string, op byte, i int) string {
	if i == 0 {
		return at
	}
	return at + string(op) + strconv.Itoa(i)
}

// commitParents reads the parent SHAs recorded in a commit object. Unlike
// rev-list or log formats, cat-file reports the object's real parents even in
// a shallow repository.
func (cli *GitCLI) commitParents(ctx context.Context, rev Revision, at, sha string) ([]string, error) {
	out, err := cli.New(WithIgnoreError()).Run(ctx, "cat-file", "commit", sha)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("resolve %q: %w: commit %s (%s) is not available locally", rev.Expr, ErrRevisionHistoryUnavailable, sha, at)
	}
	headers, _, _ := bytes.Cut(out, []byte("\n\n"))
	var parents []string
	for _, line := range strings.Split(string(headers), "\n") {
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "tree":
			continue
		case "parent":
			parents = append(parents, value)
			continue
		}
		// parents always directly follow the tree header
		break
	}
	return parents, nil
}
