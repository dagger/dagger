// Package naming parses identifiers written in any casing into words, and
// formats those words in any other casing.
//
// Case conventions lose information: HTTPAPIClient doesn't say where HTTP
// ends, httpClient doesn't say http is an acronym, and IPv6Address looks like
// I + Pv6 + Address. Parsing recovers it from a dictionary of acronyms and
// terms first, and falls back to a case heuristic for everything else, so
// every casing of a name the dictionary covers parses to the same words.
//
// The algorithm and the initial dictionary are specified in
// hack/designs/identifier-casing.md.
package naming

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// WordKind classifies a word of an identifier.
type WordKind int

const (
	// KindWord is an ordinary word, stored lowercase: client.
	KindWord WordKind = iota
	// KindAcronym is a word written in capitals, from the dictionary or a
	// run of capitals in the input: HTTP, E2E, 3D.
	KindAcronym
	// KindTerm is a dictionary entry with a fixed mixed-case spelling:
	// GitHub, IPv6, iOS.
	KindTerm
)

func (k WordKind) String() string {
	switch k {
	case KindWord:
		return "WORD"
	case KindAcronym:
		return "ACRONYM"
	case KindTerm:
		return "TERM"
	default:
		return fmt.Sprintf("WordKind(%d)", int(k))
	}
}

// Word is one unit of an identifier.
type Word struct {
	Kind WordKind
	// Text is the standard spelling: client (WORD), HTTP (ACRONYM), GitHub
	// (TERM). A WORD's digits are part of its text: base64, v1.
	Text string
	// Suffix is a plural "s" and/or trailing digits on an acronym or term:
	// SHA+"s", OAuth+"2".
	Suffix string
	// Term is the dictionary entry the word matched, or nil for a word
	// found by the case heuristic. It is shared with the dictionary and must
	// not be modified.
	Term *Term
}

// Identifier is a name parsed into words.
type Identifier struct {
	// Name is the name as given to Parse.
	Name string
	// Words are the words of the name, in order.
	Words []Word
}

// ErrNonASCII is returned when parsing a name with non-ASCII characters.
var ErrNonASCII = errors.New("identifier must be ASCII")

// ErrNoWords is returned when parsing a name with no letters or digits.
var ErrNoWords = errors.New("identifier must contain a letter or digit")

// Parse parses name with the Latest dictionary.
func Parse(name string) (Identifier, error) {
	return Latest.Parse(name)
}

// Parse parses a name in any casing into words.
func (d *Dictionary) Parse(name string) (Identifier, error) {
	var hasUpperLetter, hasLowerLetter, hasAlnumChar bool
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 0x80:
			return Identifier{}, fmt.Errorf("parse %q: %w", name, ErrNonASCII)
		case isUpper(c):
			hasUpperLetter = true
		case isLower(c):
			hasLowerLetter = true
		}
		if isAlnum(c) {
			hasAlnumChar = true
		}
	}
	if !hasAlnumChar {
		return Identifier{}, fmt.Errorf("parse %q: %w", name, ErrNoWords)
	}

	// With only one case of letters (HTTP_CLIENT, http_client), case carries
	// no information; treating capitals as acronyms would make CLIENT one.
	cased := hasUpperLetter && hasLowerLetter

	var words []Word
	for _, chunk := range strings.FieldsFunc(name, func(r rune) bool {
		return r >= 0x80 || !isAlnum(byte(r))
	}) {
		var pieces []string
		if cased {
			pieces = splitPieces(chunk)
		} else {
			pieces = []string{strings.ToLower(chunk)}
		}
		words = append(words, d.segment(pieces)...)
	}
	return Identifier{Name: name, Words: glueDigits(words)}, nil
}

// splitPieces splits a chunk of a cased name at its case boundaries.
func splitPieces(chunk string) []string {
	var pieces []string
	start := 0
	for k := 1; k < len(chunk); k++ {
		if caseBoundary(chunk, k) {
			pieces = append(pieces, chunk[start:k])
			start = k
		}
	}
	return append(pieces, chunk[start:])
}

// caseBoundary reports whether a new piece starts at s[k].
func caseBoundary(s string, k int) bool {
	if !isUpper(s[k]) {
		return false
	}
	// (a) A capital after lowercase, looking past digits: httpClient,
	// Get3DModel, Base64URLEncode.
	for j := k - 1; j >= 0; j-- {
		if isDigit(s[j]) {
			continue
		}
		if isLower(s[j]) {
			return true
		}
		break
	}
	// (b) The last capital of a run starts the next word: HTTPClient,
	// E2ETest. A lone "s" after the run is a plural instead: SHAs, IDsFoo.
	if k+1 < len(s) && isLower(s[k+1]) && (isUpper(s[k-1]) || isDigit(s[k-1])) {
		if s[k+1] == 's' && (k+2 == len(s) || isUpper(s[k+2]) || isDigit(s[k+2])) {
			return false
		}
		return true
	}
	return false
}

// isCapsPiece reports whether a piece has no lowercase letters, ignoring a
// trailing plural "s" on a piece of three or more characters.
func isCapsPiece(p string) bool {
	if len(p) >= 3 && p[len(p)-1] == 's' {
		p = p[:len(p)-1]
	}
	return !hasLower(p)
}

// cut is a run of only letters or only digits within one piece. Dictionary
// matching compares whole cuts, never substrings of one, which keeps
// Capital from containing API.
type cut struct {
	text  string
	piece int
}

type pieceInfo struct {
	text       string
	start, end int // cut range
	caps       bool
}

// score ranks a division of a chunk into words; lower is better.
type score struct {
	uncovered int // letters not covered by a dictionary entry
	words     int // words, not counting digit-only words
	plurals   int // plural dictionary matches
	total     int // all words, so heuristic words keep their digits
}

func (s score) add(o score) score {
	return score{s.uncovered + o.uncovered, s.words + o.words, s.plurals + o.plurals, s.total + o.total}
}

func (s score) less(o score) bool {
	switch {
	case s.uncovered != o.uncovered:
		return s.uncovered < o.uncovered
	case s.words != o.words:
		return s.words < o.words
	case s.plurals != o.plurals:
		return s.plurals < o.plurals
	default:
		return s.total < o.total
	}
}

// segment chooses the best division of one chunk's pieces into words.
func (d *Dictionary) segment(pieces []string) []Word {
	var cuts []cut
	infos := make([]pieceInfo, len(pieces))
	for i, p := range pieces {
		infos[i] = pieceInfo{text: p, start: len(cuts), caps: isCapsPiece(p)}
		start := 0
		for k := 1; k <= len(p); k++ {
			if k == len(p) || isDigit(p[k]) != isDigit(p[k-1]) {
				cuts = append(cuts, cut{text: p[start:k], piece: i})
				start = k
			}
		}
		infos[i].end = len(cuts)
	}

	type state struct {
		ok    bool
		score score
		from  int
		words []Word
	}
	n := len(cuts)
	best := make([]state, n+1)
	best[0].ok = true
	relax := func(i, j int, sc score, words []Word) {
		sc = best[i].score.add(sc)
		if !best[j].ok || sc.less(best[j].score) {
			best[j] = state{ok: true, score: sc, from: i, words: words}
		}
	}

	for i := 0; i < n; i++ {
		if !best[i].ok {
			continue
		}

		// Dictionary matches over whole cuts, possibly crossing pieces.
		var fold strings.Builder
		for j := i + 1; j <= n; j++ {
			fold.WriteString(strings.ToLower(cuts[j-1].text))
			f := fold.String()
			if t, ok := d.lookup(f); ok {
				relax(i, j, score{words: 1, total: 1}, []Word{termWord(t, "")})
			}
			if base, ok := strings.CutSuffix(f, "s"); ok {
				if t, ok := d.lookup(base); ok {
					relax(i, j, score{words: 1, plurals: 1, total: 1}, []Word{termWord(t, "s")})
				}
			}
		}

		p := cuts[i].piece
		info := infos[p]

		// A run of capitals has no internal boundaries, so it is the one
		// place the dictionary looks inside a cut: HTTPAPI is HTTP + API.
		if info.caps && i == info.start {
			if words, sc, ok := d.coverCaps(info.text); ok {
				relax(i, info.end, sc, words)
			}
		}

		// Heuristic words stay within one piece.
		var text strings.Builder
		for j := i + 1; j <= info.end; j++ {
			text.WriteString(cuts[j-1].text)
			w, sc := heuristicWord(text.String(), info.caps, j == info.end)
			relax(i, j, sc, []Word{w})
		}
	}

	var steps [][]Word
	for j := n; j > 0; j = best[j].from {
		steps = append(steps, best[j].words)
	}
	var out []Word
	for i := len(steps) - 1; i >= 0; i-- {
		out = append(out, steps[i]...)
	}
	return out
}

// coverCaps writes a whole caps piece as a sequence of dictionary entries and
// digit runs, with the fewest entries, then the fewest plurals. It fails if
// any letter is left over (HTTPX).
func (d *Dictionary) coverCaps(p string) ([]Word, score, bool) {
	type state struct {
		ok    bool
		score score
		from  int
		word  Word
	}
	n := len(p)
	best := make([]state, n+1)
	best[0].ok = true
	relax := func(i, j int, sc score, w Word) {
		sc = best[i].score.add(sc)
		if !best[j].ok || sc.less(best[j].score) {
			best[j] = state{ok: true, score: sc, from: i, word: w}
		}
	}
	for i := 0; i < n; i++ {
		if !best[i].ok {
			continue
		}
		for j := i + 1; j <= n && isDigit(p[j-1]); j++ {
			relax(i, j, score{total: 1}, Word{Kind: KindWord, Text: p[i:j]})
		}
		for j := i + 1; j <= n; j++ {
			t, ok := d.lookup(strings.ToLower(p[i:j]))
			if !ok {
				continue
			}
			relax(i, j, score{words: 1, total: 1}, termWord(t, ""))
			if j == n-1 && p[j] == 's' {
				relax(i, n, score{words: 1, plurals: 1, total: 1}, termWord(t, "s"))
			}
		}
	}
	if !best[n].ok {
		return nil, score{}, false
	}
	var words []Word
	for j := n; j > 0; j = best[j].from {
		words = append(words, best[j].word)
	}
	slices.Reverse(words)
	return words, best[n].score, true
}

func termWord(t *Term, suffix string) Word {
	return Word{Kind: t.Kind(), Text: t.Spelling, Suffix: suffix, Term: t}
}

// heuristicWord makes a word from text found by the case heuristic, and
// scores it. atPieceEnd reports whether text ends its piece, where a caps
// piece may carry a plural "s".
func heuristicWord(text string, caps, atPieceEnd bool) (Word, score) {
	letters := 0
	for i := 0; i < len(text); i++ {
		if !isDigit(text[i]) {
			letters++
		}
	}
	if letters == 0 {
		return Word{Kind: KindWord, Text: text}, score{total: 1}
	}
	sc := score{uncovered: letters, words: 1, total: 1}
	if caps {
		body, suffix := text, ""
		if atPieceEnd && len(text) >= 2 && text[len(text)-1] == 's' {
			body, suffix = text[:len(text)-1], "s"
		}
		if !hasLower(body) && countLetters(body) >= 2 {
			return Word{Kind: KindAcronym, Text: body, Suffix: suffix}, sc
		}
	}
	return Word{Kind: KindWord, Text: strings.ToLower(text)}, sc
}

// glueDigits attaches each digit-only word to the word before it: OAuth·2
// becomes OAuth+2, and v·1 becomes v1. Digits that start the name stay a word
// of their own.
func glueDigits(words []Word) []Word {
	out := words[:0]
	for _, w := range words {
		if len(out) > 0 && w.Kind == KindWord && w.Term == nil && isDigits(w.Text) {
			prev := &out[len(out)-1]
			if prev.Kind == KindWord {
				prev.Text += w.Text
			} else {
				prev.Suffix += w.Text
			}
			continue
		}
		out = append(out, w)
	}
	return out
}

func isUpper(c byte) bool { return 'A' <= c && c <= 'Z' }
func isLower(c byte) bool { return 'a' <= c && c <= 'z' }
func isDigit(c byte) bool { return '0' <= c && c <= '9' }
func isAlnum(c byte) bool { return isUpper(c) || isLower(c) || isDigit(c) }

func hasLower(s string) bool {
	for i := 0; i < len(s); i++ {
		if isLower(s[i]) {
			return true
		}
	}
	return false
}

func hasLetter(s string) bool {
	return countLetters(s) > 0
}

func countLetters(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if isUpper(s[i]) || isLower(s[i]) {
			n++
		}
	}
	return n
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return s != ""
}
