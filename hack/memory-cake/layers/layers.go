// Package layers buckets every sample of a Go heap profile into exactly one
// "layer" of the memory cake, by ordered stack-frame rules: the first rule
// with a regexp matching any frame of a sample's stack takes the whole
// sample, and samples no rule matches are "other". So the layers always sum
// to the profile's total.
package layers

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/google/pprof/profile"
)

// Rule assigns the samples whose stack has a frame matching Match to Layer.
type Rule struct {
	Layer string
	Match *regexp.Regexp
}

// Other is the layer of samples no rule matches.
const Other = "other"

// Rules are tried in order: the first match wins. More specific rules come
// first, e.g. Dang prepared dirs before the arbitrary results they are
// cached as.
var Rules = []Rule{
	// clientdb's in-memory indexes of the session's telemetry (#15220, #15221).
	{"spans", regexp.MustCompile(`clientdb\.\(\*spanLookup\)|clientdb\.newSpanLookup|clientdb\.\(\*idTable\)`)},
	{"logs", regexp.MustCompile(`clientdb\.\(\*logLookup\)|clientdb\.newLogLookup`)},
	// The rest of the session's telemetry store: rows and streams (no PR).
	{"telemetry", regexp.MustCompile(`engine/clientdb\.`)},
	// Content-hash trees of snapshots (#15212, #15213).
	{"contenthash", regexp.MustCompile(`engine/contenthash\.|\(\*cacheMetadata\)\.SetExternal|\(\*snapshotManager\)\.LoadPersistentMetadata|contentHashFiles|loadContentHashFiles`)},
	// Snapshot owner leases and their bolt records (#15232).
	{"leases", regexp.MustCompile(`internal/fastbolt|\(\*snapshotManager\)\.(AttachLease|attachOwner\w*|addSnapshotOwner|recordOwner|createOwnerLease|recordAncestry\w*|attachPendingAncestry\w*|DeleteStaleDaggerOwnerLeases)|\(\*ownerAncestry\)`)},
	// Dang SDK prepared programs (#15223).
	{"prepared", regexp.MustCompile(`cachedPreparedDangDir|preparedDangPrograms|dang\.PrepareDir|\(\*PreparedDirectory\)`)},
	// Schema building (#15202, #15215, #15216). Before the arbitrary
	// results: schema memos may be kept as arbitrary values too.
	{"schema", regexp.MustCompile(`core\.buildSchema|schemaJSONFile|cachedDangSchema|loadDangSchema|core/sdk\.sessionModuleInstance|\(\*CoreMod\)\.Install|core\.\(\*SchemaBuilder|SchemaBuilderMemo|SchemaIntrospection|SchemaASTCache|schemaASTCache|dagql\.\(\*Server\)\.Fork|\(\*CoreSchemaBase\)\.Fork|core\.\(\*ModDeps\)|moduleServer|introspection\.`)},
	// Other session-owned arbitrary results: received Dang values (#15224),
	// converted module receivers (#15226), and the rest kept by key.
	{"arbitrary", regexp.MustCompile(`decodeReceived|receivedValues|receivedInterner|moduleFunctionReceiver|convertedReceivers|dagql\.\(\*Cache\)\.\w*[aA]rbitrary`)},
	// dagql's own result bookkeeping (no PR).
	{"dagql", regexp.MustCompile(`dagql\.\(\*Cache\)\.initCompletedResult|sessionResultInstall|dagql\.\(\*Cache\)\.(recordSession|attachSession|newShared|captureSession|trackSessionResult|acquireSessionResult)\w*`)},
}

// Layer is one layer's share of a profile.
type Layer struct {
	ID      string `json:"id"`
	Bytes   int64  `json:"bytes"`
	Objects int64  `json:"objects"`
	// Top lists the functions holding the most of the layer: each sample's
	// innermost frame outside the runtime and the standard library's
	// containers, to see what a rule caught (or what "other" holds).
	Top []Site `json:"top,omitempty"`
}

// Site is a function and the bytes of the samples it is the site of.
type Site struct {
	Func  string `json:"func"`
	Bytes int64  `json:"bytes"`
}

// Result is a bucketed profile.
type Result struct {
	TotalBytes   int64   `json:"totalBytes"`
	TotalObjects int64   `json:"totalObjects"`
	Layers       []Layer `json:"layers"`
}

// Bucket assigns every sample of p's in-use space to exactly one layer.
func Bucket(p *profile.Profile, topN int) (*Result, error) {
	spaceIdx, objIdx := -1, -1
	for i, st := range p.SampleType {
		switch st.Type {
		case "inuse_space":
			spaceIdx = i
		case "inuse_objects":
			objIdx = i
		}
	}
	if spaceIdx < 0 || objIdx < 0 {
		return nil, fmt.Errorf("profile has no inuse_space/inuse_objects samples (types: %v)", p.SampleType)
	}

	type acc struct {
		bytes, objects int64
		sites          map[string]int64
	}
	accs := map[string]*acc{}
	get := func(id string) *acc {
		a := accs[id]
		if a == nil {
			a = &acc{sites: map[string]int64{}}
			accs[id] = a
		}
		return a
	}
	res := &Result{}
	for _, s := range p.Sample {
		b, o := s.Value[spaceIdx], s.Value[objIdx]
		if b == 0 && o == 0 {
			continue
		}
		frames := funcs(s)
		id := classify(frames)
		a := get(id)
		a.bytes += b
		a.objects += o
		a.sites[site(frames)] += b
		res.TotalBytes += b
		res.TotalObjects += o
	}
	for _, id := range IDs() {
		a := get(id)
		l := Layer{ID: id, Bytes: a.bytes, Objects: a.objects}
		for f, b := range a.sites {
			l.Top = append(l.Top, Site{Func: f, Bytes: b})
		}
		sort.Slice(l.Top, func(i, j int) bool {
			if l.Top[i].Bytes != l.Top[j].Bytes {
				return l.Top[i].Bytes > l.Top[j].Bytes
			}
			return l.Top[i].Func < l.Top[j].Func
		})
		if len(l.Top) > topN {
			l.Top = l.Top[:topN]
		}
		res.Layers = append(res.Layers, l)
	}
	var sum int64
	for _, l := range res.Layers {
		sum += l.Bytes
	}
	if sum != res.TotalBytes {
		return nil, fmt.Errorf("layers sum to %d bytes, profile total is %d", sum, res.TotalBytes)
	}
	return res, nil
}

// IDs are the layers' IDs: the rules' layers, in rule order, then Other.
func IDs() []string {
	var ids []string
	seen := map[string]bool{}
	for _, r := range Rules {
		if !seen[r.Layer] {
			seen[r.Layer] = true
			ids = append(ids, r.Layer)
		}
	}
	return append(ids, Other)
}

// Classify is the layer of a sample with these frames, innermost first.
func Classify(frames []string) string { return classify(frames) }

func classify(frames []string) string {
	for _, r := range Rules {
		for _, f := range frames {
			if r.Match.MatchString(f) {
				return r.Layer
			}
		}
	}
	return Other
}

// funcs is a sample's stack, innermost frame first, as function names
// (inlined frames included).
func funcs(s *profile.Sample) []string {
	var out []string
	for _, loc := range s.Location {
		for _, line := range loc.Line {
			if line.Function != nil {
				out = append(out, line.Function.Name)
			}
		}
	}
	return out
}

// boring frames are skipped when naming a sample's site: allocation
// plumbing rather than the code that wanted the memory.
var boring = regexp.MustCompile(`^(runtime\.|internal/|sync\.|reflect\.|strings\.|bytes\.|encoding/json\.|slices\.|maps\.|fmt\.|strconv\.|sort\.|unicode/|errors\.|container/|github\.com/hashicorp/golang-lru|github\.com/vektah/gqlparser/v2/ast\.)`)

func site(frames []string) string {
	for i, f := range frames {
		if !boring.MatchString(f) {
			if i+1 < len(frames) {
				return f + " < " + shorten(frames[i+1])
			}
			return f
		}
	}
	if len(frames) > 0 {
		return frames[0]
	}
	return "?"
}

func shorten(f string) string {
	if i := strings.LastIndex(f, "/"); i >= 0 {
		return f[i+1:]
	}
	return f
}
