package naming

import (
	"fmt"
	"strings"
)

// Term is a dictionary entry: an acronym (HTTP) or a term with a fixed
// mixed-case spelling (GitHub, IPv6).
type Term struct {
	// Spelling is the standard spelling: HTTP, IPv6, GitHub, iOS, 3D.
	Spelling string
	// Capitalized is the spelling in the CAPITALIZED acronym style: Http,
	// Ipv6, GitHub, Ios, 3D. It can't be derived from Spelling (a brand like
	// GitHub never changes), so terms with lowercase letters or leading
	// digits must set it; plain acronyms default to the first letter
	// capitalized and the rest lowercase.
	Capitalized string
}

// Kind is the kind of word a match against this term produces.
func (t Term) Kind() WordKind {
	if hasLower(t.Spelling) {
		return KindTerm
	}
	return KindAcronym
}

// Dictionary is a set of acronyms and terms used to parse identifiers.
//
// Adding a term renames every identifier that contains it, so a released
// dictionary must never change: additions go into a new Dictionary that is
// selected by engine version, leaving older modules on the one they had.
type Dictionary struct {
	terms []Term
	// byFold maps a lowercased spelling to its index in terms.
	byFold map[string]int
}

// NewDictionary builds a dictionary from terms. It returns an error for an
// entry that is empty, not alphanumeric ASCII, duplicated (ignoring case), or
// missing a Capitalized form that can't be defaulted.
func NewDictionary(terms []Term) (*Dictionary, error) {
	d := &Dictionary{
		terms:  make([]Term, 0, len(terms)),
		byFold: make(map[string]int, len(terms)),
	}
	for _, t := range terms {
		if t.Spelling == "" {
			return nil, fmt.Errorf("naming: empty dictionary entry")
		}
		for i := 0; i < len(t.Spelling); i++ {
			if !isAlnum(t.Spelling[i]) {
				return nil, fmt.Errorf("naming: dictionary entry %q is not alphanumeric ASCII", t.Spelling)
			}
		}
		if !hasLetter(t.Spelling) {
			return nil, fmt.Errorf("naming: dictionary entry %q has no letters", t.Spelling)
		}
		if t.Capitalized == "" {
			if hasLower(t.Spelling) || isDigit(t.Spelling[0]) {
				return nil, fmt.Errorf("naming: dictionary entry %q needs an explicit capitalized form", t.Spelling)
			}
			t.Capitalized = upperFirst(strings.ToLower(t.Spelling))
		}
		if !strings.EqualFold(t.Capitalized, t.Spelling) {
			return nil, fmt.Errorf("naming: dictionary entry %q has mismatched capitalized form %q", t.Spelling, t.Capitalized)
		}
		fold := strings.ToLower(t.Spelling)
		if _, dup := d.byFold[fold]; dup {
			return nil, fmt.Errorf("naming: duplicate dictionary entry %q", t.Spelling)
		}
		d.byFold[fold] = len(d.terms)
		d.terms = append(d.terms, t)
	}
	return d, nil
}

// MustDictionary is NewDictionary, panicking on error.
func MustDictionary(terms []Term) *Dictionary {
	d, err := NewDictionary(terms)
	if err != nil {
		panic(err)
	}
	return d
}

// Terms returns the dictionary's entries in definition order.
func (d *Dictionary) Terms() []Term {
	return append([]Term(nil), d.terms...)
}

// lookup finds the entry spelled like fold, which must be lowercase.
func (d *Dictionary) lookup(fold string) (*Term, bool) {
	i, ok := d.byFold[fold]
	if !ok {
		return nil, false
	}
	return &d.terms[i], true
}

// Initial is the dictionary the engine first shipped identifier parsing
// with. Don't edit it; see Dictionary.
var Initial = MustDictionary(initialTerms)

// Latest is the newest dictionary, used by the package-level Parse.
var Latest = Initial

var initialTerms = []Term{
	// golint's commonInitialisms, which the Go SDK uses today.
	{Spelling: "ACL"},
	{Spelling: "API"},
	{Spelling: "ASCII"},
	{Spelling: "CPU"},
	{Spelling: "CSS"},
	{Spelling: "DNS"},
	{Spelling: "EOF"},
	{Spelling: "GUID"},
	{Spelling: "HTML"},
	{Spelling: "HTTP"},
	{Spelling: "HTTPS"},
	{Spelling: "ID"},
	{Spelling: "IP"},
	{Spelling: "JSON"},
	{Spelling: "LHS"},
	{Spelling: "QPS"},
	{Spelling: "RAM"},
	{Spelling: "RHS"},
	{Spelling: "RPC"},
	{Spelling: "SLA"},
	{Spelling: "SMTP"},
	{Spelling: "SQL"},
	{Spelling: "SSH"},
	{Spelling: "TCP"},
	{Spelling: "TLS"},
	{Spelling: "TTL"},
	{Spelling: "UDP"},
	{Spelling: "UI"},
	{Spelling: "UID"},
	{Spelling: "UUID"},
	{Spelling: "URI"},
	{Spelling: "URL"},
	{Spelling: "UTF8"},
	{Spelling: "VM"},
	{Spelling: "XML"},
	{Spelling: "XMPP"},
	{Spelling: "XSRF"},
	{Spelling: "XSS"},

	// The Go SDK's Dagger additions.
	{Spelling: "FS"},
	{Spelling: "SDK"},
	{Spelling: "LLM"},

	// Acronyms found in the core schema.
	{Spelling: "GPU"},
	{Spelling: "VCS"},
	{Spelling: "SHA"},
	{Spelling: "MCP"},
	{Spelling: "OCI"},
	// A lowercase word is never split, but all-caps input is: without this
	// entry, sshfsVolume would be sshfs · volume, and its SCREAMING_SNAKE
	// form SSHFS_VOLUME would parse as SSH · FS · volume.
	{Spelling: "SSHFS"},

	// Common in modules.
	{Spelling: "E2E"},
	{Spelling: "CLI"},
	{Spelling: "TUI"},
	{Spelling: "IO"},
	{Spelling: "PR"},
	{Spelling: "EC2"},
	{Spelling: "MD5"},

	// Terms the case heuristic or the digit rule would split.
	{Spelling: "GitHub", Capitalized: "GitHub"},
	{Spelling: "GitLab", Capitalized: "GitLab"},
	{Spelling: "OAuth", Capitalized: "Oauth"},
	{Spelling: "IPv4", Capitalized: "Ipv4"},
	{Spelling: "IPv6", Capitalized: "Ipv6"},
	{Spelling: "iOS", Capitalized: "Ios"},
	{Spelling: "macOS", Capitalized: "MacOS"},
	{Spelling: "gRPC", Capitalized: "Grpc"},
	{Spelling: "GraphQL", Capitalized: "GraphQL"},
	{Spelling: "3D", Capitalized: "3D"},
	{Spelling: "2D", Capitalized: "2D"},
}
