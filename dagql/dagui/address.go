package dagui

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/dagger/dagger/engine/naming"
)

// Address is a parsed DAG address, the name `dagger check` and `dagger
// generate` give the spans of the artifacts they run:
//
//	dag[+<type>...]://[<workspace>@<version>:][<path>][?<dimension>=<key>&...]
//
// It mirrors the engine's parser (core/dagaddress) closely enough to display
// an address; it does not validate one.
type Address struct {
	// Raw is the address as written.
	Raw string
	// Types lists the "+<type>" segments of the scheme.
	Types []string
	// Workspace and Version are set for an absolute address.
	Workspace string
	Version   string
	// Kind is the artifact's type in CLI case, such as "check" or
	// "generator", when the address says it.
	Kind string
	// Path is the artifact path with "/" separators, such as
	// "go/test-all". An address without a path holds it in its type
	// dimension key (dag://?check=go/test-all); Path holds it either way.
	Path string
	// Module is the module an address with a name column names its
	// artifact in (dagger/dagger#15177), the first segment of its Path.
	Module string
	// Dimensions lists the collection keys, one per dimension in first-seen
	// order, with repeated keys of a dimension gathered.
	Dimensions []AddressDimension
}

// AddressDimension is the keys an address selects in one collection
// dimension.
type AddressDimension struct {
	Name string
	Keys []string
	// Any reports that the dimension was given without a key, selecting
	// any key.
	Any bool
	// Absent reports that the dimension was given as "!<dimension>",
	// selecting artifacts with no key in it.
	Absent bool
}

// sameSelection reports whether two dimensions select the same keys.
func sameSelection(a, b AddressDimension) bool {
	return a.Any == b.Any && a.Absent == b.Absent && slices.Equal(a.Keys, b.Keys)
}

// Segments splits the artifact path at its separators.
func (addr *Address) Segments() []string {
	if addr.Path == "" {
		return nil
	}
	return strings.Split(addr.Path, "/")
}

// Dimension returns the address's dimension by name, if it has it. The
// module a name column's address folded into its Path is not one: see
// DimensionKeys.
func (addr *Address) Dimension(name string) (AddressDimension, bool) {
	for _, dim := range addr.Dimensions {
		if dim.Name == name {
			return dim, true
		}
	}
	return AddressDimension{}, false
}

// DimensionKeys returns the keys the address selects in a dimension,
// including the module a name column's address folded into its Path.
func (addr *Address) DimensionKeys(name string) ([]string, bool) {
	if dim, ok := addr.Dimension(name); ok {
		return dim.Keys, true
	}
	if name == "module" && addr.Module != "" {
		return []string{addr.Module}, true
	}
	return nil, false
}

// DimensionNames lists the address's dimensions, the folded module first.
func (addr *Address) DimensionNames() []string {
	var names []string
	if addr.Module != "" {
		names = append(names, "module")
	}
	for _, dim := range addr.Dimensions {
		names = append(names, dim.Name)
	}
	return names
}

// ElidedSegment stands for the segments ElideSegments leaves out.
const ElidedSegment = "…"

// ElideSegments shortens a path longer than maxLen to its first segment, an
// ellipsis (ElidedSegment), and as many trailing segments as fit (at least
// the last two), so the leaf and its parent stay readable.
func ElideSegments(segments []string, maxLen int) []string {
	if len(segments) <= 3 || len(strings.Join(segments, "/")) <= maxLen {
		return segments
	}
	tail := 2
	// first + "/…/" + the last two segments and the "/" between them
	length := len(segments[0]) + 3 + len(segments[len(segments)-2]) + 1 + len(segments[len(segments)-1])
	for tail < len(segments)-2 {
		next := len(segments[len(segments)-tail-1]) + 1
		if length+next > maxLen {
			break
		}
		length += next
		tail++
	}
	out := []string{segments[0], ElidedSegment}
	return append(out, segments[len(segments)-tail:]...)
}

// Compact prints the address without its scheme and type noise: the path,
// then each dimension as name=key, with a repeated dimension as name×count.
func (addr *Address) Compact() string {
	return addr.CompactWithout(nil)
}

// CompactWithout prints the address as Compact does, less the dimensions
// named in hide -- the ones every artifact listed beside it shares (see
// ConstantDimensions), which say nothing about it among them. Hiding
// "module" drops the module a name column's address folded into its path.
func (addr *Address) CompactWithout(hide []string) string {
	return addr.compact(hide, AddressDimension.Compact)
}

// CompactKeysWithout prints the address as CompactWithout does, but lists
// every key of a dimension holding several (see AddressDimension.CompactKeys),
// for artifacts that their counts alone would not tell apart.
func (addr *Address) CompactKeysWithout(hide []string) string {
	return addr.compact(hide, AddressDimension.CompactKeys)
}

func (addr *Address) compact(hide []string, dimension func(AddressDimension) string) string {
	parts := []string{}
	if addr.Workspace != "" {
		parts = append(parts, addr.Workspace+"@"+addr.Version)
	}
	path := addr.Path
	if addr.Module != "" && slices.Contains(hide, "module") {
		if rest, ok := strings.CutPrefix(path, addr.Module+"/"); ok && rest != "" {
			path = rest
		}
	}
	if path != "" {
		parts = append(parts, path)
	}
	for _, dim := range addr.Dimensions {
		if !slices.Contains(hide, dim.Name) {
			parts = append(parts, dimension(dim))
		}
	}
	return strings.Join(parts, " ")
}

// Compact prints the dimension as name=key, name×count when it holds
// several keys, name=* for any key, or !name for none.
func (dim AddressDimension) Compact() string {
	switch {
	case dim.Absent:
		return "!" + dim.Name
	case dim.Any || len(dim.Keys) == 0:
		return dim.Name + "=*"
	case len(dim.Keys) == 1:
		return dim.Name + "=" + dim.Keys[0]
	default:
		return dim.Name + "×" + strconv.Itoa(len(dim.Keys))
	}
}

// CompactKeys prints the dimension as Compact does, but one holding several
// keys as name=key,key,... rather than by their count.
func (dim AddressDimension) CompactKeys() string {
	if dim.Any || dim.Absent || len(dim.Keys) < 2 {
		return dim.Compact()
	}
	return dim.Name + "=" + strings.Join(dim.Keys, ",")
}

// Cell prints the dimension as a cell of a collection table (see
// CollectionTable), whose column heading names it: its key, "*" for any
// key, "–" for none, or name ×count when it holds several keys.
func (dim AddressDimension) Cell() string {
	switch {
	case dim.Absent:
		return "–"
	case dim.Any:
		return "*"
	case len(dim.Keys) == 0:
		return "–"
	case len(dim.Keys) == 1:
		return dim.Keys[0]
	default:
		return dim.Name + " ×" + strconv.Itoa(len(dim.Keys))
	}
}

// ConstantDimensions returns the dimensions all of the addresses among names
// have the same keys in, if there are several to compare: the columns a
// table of them would hide (dagger/dagger#15177). Names that are not
// addresses are left out.
func ConstantDimensions(names []string) []string {
	if len(names) < 2 {
		return nil
	}
	var addrs []*Address
	for _, name := range names {
		if addr, ok := ParseAddress(name); ok {
			addrs = append(addrs, addr)
		}
	}
	if len(addrs) < 2 {
		return nil
	}
	var constant []string
	for _, name := range addrs[0].DimensionNames() {
		first, _ := addrs[0].DimensionKeys(name)
		firstDim, _ := addrs[0].Dimension(name)
		same := true
		for _, addr := range addrs[1:] {
			keys, ok := addr.DimensionKeys(name)
			dim, _ := addr.Dimension(name)
			if !ok || !slices.Equal(keys, first) || dim.Any != firstDim.Any || dim.Absent != firstDim.Absent {
				same = false
				break
			}
		}
		if same {
			constant = append(constant, name)
		}
	}
	return constant
}

// ParseAddress parses name as a DAG address. It reports false when name is
// not one, or is malformed, so callers can show the name as it is.
//
// It understands both addresses that name their artifact by its type
// dimension (dag://?check=go/test-all) and ones that name it by module and
// name columns (dag+check://?module=go&name=test-all, dagger/dagger#15177).
func ParseAddress(name string) (*Address, bool) {
	// Keys can hold URLs: split the query off before the scheme.
	rest, query, _ := strings.Cut(name, "?")
	scheme, rest, ok := strings.Cut(rest, "://")
	if !ok || (scheme != "dag" && !strings.HasPrefix(scheme, "dag+")) {
		return nil, false
	}
	addr := &Address{Raw: name}
	for _, typ := range strings.Split(scheme, "+")[1:] {
		if typ == "" {
			return nil, false
		}
		addr.Types = append(addr.Types, typ)
	}
	pairs, ok := parseAddressQuery(query)
	if !ok {
		return nil, false
	}
	if workspace, after, ok := strings.Cut(rest, "@"); ok {
		version, path, ok := strings.Cut(after, ":")
		if !ok || workspace == "" || version == "" {
			return nil, false
		}
		addr.Workspace = workspace
		addr.Version = version
		rest = path
	}
	addr.Path = strings.ReplaceAll(rest, ":", "/")

	typeKey := -1
	if addr.Path == "" {
		typeKey = addressTypeKey(pairs, addr.Types)
	}
	if typeKey >= 0 {
		addr.Path = pairs[typeKey].key
		addr.Kind = addressTypeKind(pairs[typeKey].dimension)
		pairs = append(pairs[:typeKey:typeKey], pairs[typeKey+1:]...)
	} else if len(addr.Types) > 0 {
		addr.Kind = addr.Types[0]
	}
	if addr.Path == "" {
		// An address with a name column (dagger/dagger#15177) names its
		// artifact by the field path from its module's root, and its
		// module: together, the path older addresses spelled out.
		pairs = nameAddressPath(addr, pairs)
	}
	index := map[string]int{}
	for _, pair := range pairs {
		i, seen := index[pair.dimension]
		if !seen {
			i = len(addr.Dimensions)
			index[pair.dimension] = i
			addr.Dimensions = append(addr.Dimensions, AddressDimension{Name: pair.dimension})
		}
		dim := &addr.Dimensions[i]
		if pair.absent {
			dim.Absent = true
		} else if pair.hasKey {
			dim.Keys = append(dim.Keys, pair.key)
		} else {
			dim.Any = true
		}
	}
	if addr.Path == "" && len(addr.Dimensions) == 0 && addr.Workspace == "" {
		return nil, false
	}
	return addr, true
}

// nameAddressPath sets the path of an address that has a single-keyed name
// column -- a field path such as modules.packages.tests.test -- to it, after
// its module's name if it has a single-keyed module column too, and returns
// the pairs left. It leaves an address without a name column as it is: its
// collection keys alone identify it.
//
// The name column spells fields as the schema does (generateFile.stale); the
// path spells them in CLI case, as a path address and `dagger check -l`'s
// links do (generate-file/stale).
func nameAddressPath(addr *Address, pairs []addressPair) []addressPair {
	single := func(dimension string) (int, bool) {
		at := -1
		for i, pair := range pairs {
			if pair.dimension != dimension {
				continue
			}
			if at >= 0 || !pair.hasKey || pair.key == "" {
				return -1, false
			}
			at = i
		}
		return at, at >= 0
	}
	nameAt, ok := single("name")
	if !ok {
		return pairs
	}
	segments := strings.Split(pairs[nameAt].key, ".")
	for i, segment := range segments {
		segments[i] = cliFieldName(segment)
	}
	path := strings.Join(segments, "/")
	drop := []int{nameAt}
	if moduleAt, ok := single("module"); ok {
		addr.Module = pairs[moduleAt].key
		path = addr.Module + "/" + path
		drop = append(drop, moduleAt)
	}
	addr.Path = path
	rest := make([]addressPair, 0, len(pairs)-len(drop))
	for i, pair := range pairs {
		if !slices.Contains(drop, i) {
			rest = append(rest, pair)
		}
	}
	return rest
}

type addressPair struct {
	dimension string
	key       string
	hasKey    bool
	absent    bool
}

func parseAddressQuery(query string) ([]addressPair, bool) {
	var pairs []addressPair
	for raw := range strings.SplitSeq(query, "&") {
		if raw == "" {
			continue
		}
		name, key, hasKey := strings.Cut(raw, "=")
		dimension, err := url.PathUnescape(name)
		if err != nil {
			return nil, false
		}
		// Like the engine's parser: "!<dimension>" selects artifacts with
		// no key in it.
		dimension, absent := strings.CutPrefix(dimension, "!")
		if dimension == "" {
			return nil, false
		}
		pair := addressPair{dimension: dimension, hasKey: hasKey, absent: absent}
		if hasKey {
			if pair.key, err = url.PathUnescape(key); err != nil {
				return nil, false
			}
		}
		if absent {
			if pair.key != "" {
				return nil, false
			}
			pair.hasKey = false
		}
		pairs = append(pairs, pair)
	}
	return pairs, true
}

// addressTypeKey finds the type dimension pair of an address without a path,
// whose key is the artifact path: the qualified "type:<Type>" or
// "artifact-<type>" forms first, then the plain check and generator types
// and the scheme's own types. It returns -1 when there is none.
//
// A collection can share a type's plain name (go-test), so an address with a
// name column, which names its artifact that way instead, matches the
// scheme's own types only in their qualified forms.
func addressTypeKey(pairs []addressPair, types []string) int {
	for i, pair := range pairs {
		if pair.hasKey && (strings.HasPrefix(pair.dimension, "type:") || strings.HasPrefix(pair.dimension, "artifact-")) {
			return i
		}
	}
	if slices.ContainsFunc(pairs, func(pair addressPair) bool { return pair.dimension == "name" }) {
		types = nil
	}
	for i, pair := range pairs {
		if !pair.hasKey {
			continue
		}
		if pair.dimension == "check" || pair.dimension == "generator" {
			return i
		}
		if slices.Contains(types, pair.dimension) {
			return i
		}
	}
	return -1
}

// addressTypeKind names the type of a type dimension in CLI case.
func addressTypeKind(dimension string) string {
	if typ, ok := strings.CutPrefix(dimension, "type:"); ok {
		return kebabCase(typ)
	}
	if typ, ok := strings.CutPrefix(dimension, "artifact-"); ok {
		return typ
	}
	return dimension
}

// cliFieldName spells a schema field name in CLI case (generateFile as
// generate-file, checkGraphQLSchema as check-graphql-schema), by the
// engine's naming rules, as core's Namer.CLIName does for a module of the
// latest engine version.
func cliFieldName(name string) string {
	id, err := naming.Parse(name)
	if err != nil {
		return kebabCase(name)
	}
	return id.Format(naming.Kebab, naming.Uppercase)
}

// kebabCase turns a GraphQL type name like GoTest into go-test.
func kebabCase(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]) ||
				(i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(runes[i-1]))) {
				b.WriteByte('-')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
