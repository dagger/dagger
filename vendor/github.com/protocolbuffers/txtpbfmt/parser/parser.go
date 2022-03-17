// Package parser edits text proto files, applies standard formatting
// and preserves comments.
// See also: https://github.com/golang/protobuf/blob/master/proto/text_parser.go
//
// To disable a specific file from getting formatted, add '# txtpbfmt: disable'
// at the top of the file.
package parser

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strings"

	log "github.com/golang/glog"
	"github.com/protocolbuffers/txtpbfmt/ast"
)

// Config can be used to pass additional config parameters to the formatter at
// the time of the API call.
type Config struct {
	// Expand all children irrespective of the initial state.
	ExpandAllChildren bool

	// Skip colons whenever possible.
	SkipAllColons bool

	// Allow unnamed nodes everywhere.
	// Default is to allow only top-level nodes to be unnamed.
	AllowUnnamedNodesEverywhere bool

	// Sort fields by field name.
	SortFieldsByFieldName bool

	// Sort adjacent scalar fields of the same field name by their contents.
	SortRepeatedFieldsByContent bool

	// Remove lines that have the same field name and scalar value as another.
	RemoveDuplicateValuesForRepeatedFields bool

	// Permit usage of Python-style """ or ''' delimited strings.
	AllowTripleQuotedStrings bool
}

type parser struct {
	in     []byte
	index  int
	length int
	log    log.Verbose
	// Maps the index of '{' characters on 'in' that have the matching '}' on
	// the same line to 'true'.
	bracketSameLine map[int]bool
	config          Config
	line, column    int // current position, 1-based.
}

var defConfig = Config{}

const indentSpaces = "  "

func getMetaComments(in []byte) map[string]bool {
	metaComments := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(in))
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) == 0 {
			continue
		}
		if line[0] != byte('#') {
			break // only process the leading comment block
		}
		colon := strings.IndexByte(line, byte(':'))
		if colon > 1 && strings.TrimSpace(line[1:colon]) == "txtpbfmt" {
			for _, s := range strings.Split(line[colon+1:], ",") {
				metaComments[strings.TrimSpace(s)] = true
			}
		}
	}
	return metaComments
}

// Format formats a text proto file preserving comments.
func Format(in []byte) ([]byte, error) {
	return FormatWithConfig(in, defConfig)
}

// FormatWithConfig functions similar to format, but allows the user to pass in
// additional configuration options.
func FormatWithConfig(in []byte, c Config) ([]byte, error) {
	metaComments := getMetaComments(in)
	if metaComments["disable"] {
		log.Infoln("Ignored file with 'disable' comment.")
		return in, nil
	}
	nodes, err := parseWithConfig(in, c, metaComments)
	if err != nil {
		return nil, err
	}
	return out(nodes), nil
}

// Return the byte-positions of each bracket which has the corresponding close on the
// same line as a set.
func sameLineBrackets(in []byte, allowTripleQuotedStrings bool) (map[int]bool, error) {
	line := 1
	type bracket struct {
		index int
		line  int
	}
	open := []bracket{} // Stack.
	res := map[int]bool{}
	insideComment := false
	insideString := false
	insideTripleQuotedString := false
	var stringDelimiter string
	isEscapedChar := false
	for i, c := range in {
		switch c {
		case '\n':
			line++
			insideComment = false
		case '{', '<':
			if insideComment || insideString {
				continue
			}
			open = append(open, bracket{index: i, line: line})
		case '}', '>':
			if insideComment || insideString {
				continue
			}
			if len(open) == 0 {
				return nil, fmt.Errorf("too many '}' or '>' at index %d", i)
			}
			last := len(open) - 1
			br := open[last]
			open = open[:last]
			if br.line == line {
				res[br.index] = true
			}
		case '#':
			if insideString {
				continue
			}
			insideComment = true
		case '"', '\'':
			if insideComment {
				continue
			}
			delim := string(c)
			tripleQuoted := false
			if allowTripleQuotedStrings && i+3 <= len(in) {
				triple := string(in[i : i+3])
				if triple == `"""` || triple == `'''` {
					delim = triple
					tripleQuoted = true
				}
			}

			if insideString {
				if stringDelimiter == delim && (insideTripleQuotedString || !isEscapedChar) {
					insideString = false
					insideTripleQuotedString = false
				}
			} else {
				insideString = true
				if tripleQuoted {
					insideTripleQuotedString = true
				}
				stringDelimiter = delim
			}
		}

		if isEscapedChar {
			isEscapedChar = false
		} else if c == '\\' && insideString && !insideTripleQuotedString {
			isEscapedChar = true
		}
	}
	if insideString {
		return nil, fmt.Errorf("unterminated string literal")
	}
	return res, nil
}

func removeDeleted(nodes []*ast.Node) []*ast.Node {
	res := []*ast.Node{}
	// When removing a node which has an empty line before it, we should keep
	// the empty line before the next non-removed node to maintain the visual separation.
	// Consider the following:
	// foo: { name: "foo1" }
	// foo: { name: "foo2" }
	//
	// bar: { name: "bar1" }
	// bar: { name: "bar2" }
	//
	// If we decide to remove both foo2 and bar1, the result should still have one empty
	// line between foo1 and bar2.
	addEmptyLine := false
	for _, node := range nodes {
		if node.Deleted {
			if len(node.PreComments) > 0 && node.PreComments[0] == "" {
				addEmptyLine = true
			}
			continue
		}
		if len(node.Children) > 0 {
			node.Children = removeDeleted(node.Children)
		}
		if addEmptyLine && (len(node.PreComments) == 0 || node.PreComments[0] != "") {
			node.PreComments = append([]string{""}, node.PreComments...)
		}
		addEmptyLine = false
		res = append(res, node)
	}
	return res
}

var (
	spaceSeparators = []byte(" \t\n")
	valueSeparators = []byte(" \t\n{}:,]<>;")
)

// Parse returns a tree representation of a textproto file.
func Parse(in []byte) ([]*ast.Node, error) {
	return ParseWithConfig(in, defConfig)
}

// ParseWithConfig functions similar to Parse, but allows the user to pass in
// additional configuration options.
func ParseWithConfig(in []byte, c Config) ([]*ast.Node, error) {
	return parseWithConfig(in, c, getMetaComments(in))
}

func parseWithConfig(in []byte, c Config, metaComments map[string]bool) ([]*ast.Node, error) {
	if metaComments["expand_all_children"] {
		c.ExpandAllChildren = true
	}
	if metaComments["skip_all_colons"] {
		c.SkipAllColons = true
	}
	if metaComments["allow_unnamed_nodes_everywhere"] {
		c.AllowUnnamedNodesEverywhere = true
	}
	if metaComments["sort_fields_by_field_name"] {
		c.SortFieldsByFieldName = true
	}
	if metaComments["sort_repeated_fields_by_content"] {
		c.SortRepeatedFieldsByContent = true
	}
	if metaComments["remove_duplicate_values_for_repeated_fields"] {
		c.RemoveDuplicateValuesForRepeatedFields = true
	}
	if metaComments["allow_triple_quoted_strings"] {
		c.AllowTripleQuotedStrings = true
	}
	p, err := newParser(in, c)
	if err != nil {
		return nil, err
	}
	if p.log {
		p.log.Infof("p.in: %q", string(p.in))
		p.log.Infof("p.length: %v", p.length)
	}
	// Although unnamed nodes aren't strictly allowed, some formats represent a
	// list of protos as a list of unnamed top-level nodes.
	nodes, _, err := p.parse( /*isRoot=*/ true)
	if err != nil {
		return nil, err
	}
	if p.index < p.length {
		return nil, fmt.Errorf("parser didn't consume all input. Stopped at %s", p.errorContext())
	}
	sortAndFilterNodes(nodes, nodeSortFunction(c.SortFieldsByFieldName, c.SortRepeatedFieldsByContent), c.RemoveDuplicateValuesForRepeatedFields)
	return nodes, err
}

func newParser(in []byte, c Config) (*parser, error) {
	var bracketSameLine map[int]bool
	if c.ExpandAllChildren {
		bracketSameLine = map[int]bool{}
	} else {
		var err error
		if bracketSameLine, err = sameLineBrackets(in, c.AllowTripleQuotedStrings); err != nil {
			return nil, err
		}
	}
	if len(in) > 0 && in[len(in)-1] != '\n' {
		in = append(in, '\n')
	}
	parser := &parser{
		in:              in,
		index:           0,
		length:          len(in),
		log:             log.V(2),
		bracketSameLine: bracketSameLine,
		config:          c,
		line:            1,
		column:          1,
	}
	return parser, nil
}

func (p *parser) nextInputIs(b byte) bool {
	return p.index < p.length && p.in[p.index] == b
}

func (p *parser) consume(b byte) bool {
	if !p.nextInputIs(b) {
		return false
	}
	p.index++
	p.column++
	if b == '\n' {
		p.line++
		p.column = 1
	}
	return true
}

// consumeString consumes the given string s, which should not have any newlines.
func (p *parser) consumeString(s string) bool {
	if p.index+len(s) > p.length {
		return false
	}
	if string(p.in[p.index:p.index+len(s)]) != s {
		return false
	}
	p.index += len(s)
	p.column += len(s)
	return true
}

// loopDetector detects if the parser is in an infinite loop (ie failing to
// make progress).
type loopDetector struct {
	lastIndex int
	count     int
	parser    *parser
}

func (p *parser) getLoopDetector() *loopDetector {
	return &loopDetector{lastIndex: p.index, parser: p}
}

func (l *loopDetector) iter() error {
	if l.parser.index == l.lastIndex {
		l.count++
		if l.count < 2 {
			return nil
		}
		return fmt.Errorf("parser failed to make progress at %s", l.parser.errorContext())
	}
	l.lastIndex = l.parser.index
	l.count = 0
	return nil
}

func (p parser) errorContext() string {
	index := p.index
	if index >= p.length {
		index = p.length - 1
	}
	// Provide the surrounding input as context.
	lastContentIndex := index + 20
	if lastContentIndex >= p.length {
		lastContentIndex = p.length - 1
	}
	previousContentIndex := index - 20
	if previousContentIndex < 0 {
		previousContentIndex = 0
	}
	before := string(p.in[previousContentIndex:index])
	after := string(p.in[index:lastContentIndex])
	return fmt.Sprintf("index %v\nposition %+v\nbefore: %q\nafter: %q\nbefore+after: %q", index, p.position(), before, after, before+after)
}

func (p *parser) position() ast.Position {
	return ast.Position{
		Byte:   uint32(p.index),
		Line:   int32(p.line),
		Column: int32(p.column),
	}
}

// parse parses a text proto.
// It assumes the text to be either conformant with the standard text proto
// (i.e. passes proto.UnmarshalText() without error) or the alternative textproto
// format (sequence of messages, each of which passes proto.UnmarshalText()).
// endPos is the position of the first character on the first line
// after parsed nodes: that's the position to append more children.
func (p *parser) parse(isRoot bool) (result []*ast.Node, endPos ast.Position, err error) {
	res := []*ast.Node{}
	for ld := p.getLoopDetector(); p.index < p.length; {
		if err := ld.iter(); err != nil {
			return nil, ast.Position{}, err
		}

		startPos := p.position()
		if p.nextInputIs('\n') {
			// p.parse is often invoked with the index pointing at the
			// newline character after the previous item.
			// We should still report that this item starts in the next line.
			startPos.Byte++
			startPos.Line++
			startPos.Column = 1
		}

		// Read PreComments.
		comments, blankLines := p.skipWhiteSpaceAndReadComments(true /* multiLine */)

		// Handle blank lines.
		if blankLines > 1 {
			if p.log {
				p.log.Infof("blankLines: %v", blankLines)
			}
			comments = append([]string{""}, comments...)
		}

		for p.nextInputIs('%') {
			comments = append(comments, p.readTemplate())
			c, _ := p.skipWhiteSpaceAndReadComments(false)
			comments = append(comments, c...)
		}

		if endPos := p.position(); p.consume('}') || p.consume('>') {
			// Handle comments after last child.

			if len(comments) > 0 {
				res = append(res, &ast.Node{Start: startPos, PreComments: comments})
			}

			// endPos points at the closing brace, but we should rather return the position
			// of the first character after the previous item. Therefore let's rewind a bit:
			for p.in[endPos.Byte-1] == ' ' {
				endPos.Byte--
				endPos.Column--
			}

			// Done parsing children.
			return res, endPos, nil
		}

		nd := &ast.Node{
			Start:       startPos,
			PreComments: comments,
		}
		if p.log {
			p.log.Infof("PreComments: %q", strings.Join(nd.PreComments, "\n"))
		}

		// Skip white-space other than '\n', which is handled below.
		for p.consume(' ') || p.consume('\t') {
		}

		// Handle multiple comment blocks.
		// <example>
		// # comment block 1
		// # comment block 1
		//
		// # comment block 2
		// # comment block 2
		// </example>
		// Each block that ends on an empty line (instead of a field) gets its own
		// 'empty' node.
		if p.nextInputIs('\n') {
			res = append(res, nd)
			continue
		}

		// Handle end of file.
		if p.index >= p.length {
			nd.End = p.position()
			if len(nd.PreComments) > 0 {
				res = append(res, nd)
			}
			break
		}

		if p.consume('[') {
			// Read Name (of proto extension).
			nd.Name = fmt.Sprintf("[%s]", p.readExtension())
			_ = p.consume(']') // Ignore the ']'.
		} else {
			// Read Name.
			nd.Name = p.readFieldName()
			if nd.Name == "" && !isRoot && !p.config.AllowUnnamedNodesEverywhere {
				return nil, ast.Position{}, fmt.Errorf("Failed to find a FieldName at %s", p.errorContext())
			}
		}
		if p.log {
			p.log.Infof("name: %q", nd.Name)
		}
		// Skip separator.
		_, _ = p.skipWhiteSpaceAndReadComments(true /* multiLine */)
		nd.SkipColon = !p.consume(':')
		previousPos := p.position()
		_, _ = p.skipWhiteSpaceAndReadComments(true /* multiLine */)

		if p.consume('{') || p.consume('<') {
			if p.config.SkipAllColons {
				nd.SkipColon = true
			}
			nd.ChildrenSameLine = p.bracketSameLine[p.index-1]
			// Recursive call to parse child nodes.
			nodes, lastPos, err := p.parse( /*isRoot=*/ false)
			if err != nil {
				return nil, ast.Position{}, err
			}
			nd.Children = nodes
			nd.End = lastPos

			nd.ClosingBraceComment = p.readInlineComment()
		} else if p.consume('[') {
			// Handle list of values.

			nd.ValuesAsList = true // We found values in list - keep it as list.
			openBracketLine := p.line

			// Skip separator.
			preComments, _ := p.skipWhiteSpaceAndReadComments(true /* multiLine */)

			for ld := p.getLoopDetector(); !p.consume(']') && p.index < p.length; {
				if err := ld.iter(); err != nil {
					if p.nextInputIs('{') {
						err = fmt.Errorf("\n\n[{}] notation not supported, see http://b/74558064.\n\nFull error: %s", err)
					}
					return nil, ast.Position{}, err
				}

				// Read each value in the list.
				vals, err := p.readValues()
				if err != nil {
					return nil, ast.Position{}, err
				}
				if len(vals) != 1 {
					return nil, ast.Position{}, fmt.Errorf("multiple-string value not supported (%v). Please add comma explcitily, see http://b/162070952", vals)
				}
				vals[0].PreComments = append(vals[0].PreComments, preComments...)

				// Skip separator.
				_, _ = p.skipWhiteSpaceAndReadComments(false /* multiLine */)
				if p.consume(',') {
					vals[0].InlineComment = p.readInlineComment()
				}

				nd.Values = append(nd.Values, vals...)

				preComments, _ = p.skipWhiteSpaceAndReadComments(true /* multiLine */)
			}
			nd.ChildrenSameLine = (openBracketLine == p.line)

			res = append(res, nd)

			// Handle comments after last line (or for empty list)
			nd.PostValuesComments = preComments
			nd.ClosingBraceComment = p.readInlineComment()

			_ = p.consume(';') // Ignore optional ';'.
			_ = p.consume(',') // Ignore optional ','.
			continue
		} else {
			// Rewind comments.
			p.index = int(previousPos.Byte)
			p.line = int(previousPos.Line)
			p.column = int(previousPos.Column)
			// Handle Values.
			nd.Values, err = p.readValues()
			if err != nil {
				return nil, ast.Position{}, err
			}
			_ = p.consume(';') // Ignore optional ';'.
			_ = p.consume(',') // Ignore optional ','.
		}
		if p.log && p.index < p.length {
			p.log.Infof("p.in[p.index]: %q", string(p.in[p.index]))
		}
		res = append(res, nd)
	}
	return res, p.position(), nil
}

func (p *parser) readFieldName() string {
	i := p.index
	for ; i < p.length && !p.isValueSep(i); i++ {
	}
	return p.advance(i)
}

func (p *parser) readExtension() string {
	i := p.index
	for ; i < p.length && (p.isBlankSep(i) || !p.isValueSep(i)); i++ {
	}
	return removeBlanks(p.advance(i))
}

func removeBlanks(in string) string {
	s := []byte(in)
	for _, b := range spaceSeparators {
		s = bytes.Replace(s, []byte{b}, nil, -1)
	}
	return string(s)
}

// skipWhiteSpaceAndReadComments has multiple cases:
// - (1) reading a block of comments followed by a blank line
// - (2) reading a block of comments followed by non-blank content
// - (3) reading the inline comments between the current char and the end of the
//     current line
// Lines of comments and number of blank lines will be returned.
func (p *parser) skipWhiteSpaceAndReadComments(multiLine bool) ([]string, int) {
	i := p.index
	var foundComment, insideComment bool
	commentBegin := 0
	var comments []string
	blankLines := 0
	for ; i < p.length; i++ {
		if p.in[i] == '#' && !insideComment {
			insideComment = true
			foundComment = true
			commentBegin = i
		} else if p.in[i] == '\n' {
			if insideComment {
				comments = append(comments, string(p.in[commentBegin:i])) // Exclude the '\n'.
				insideComment = false
			} else if foundComment {
				i-- // Put back the last '\n' so the caller can detect that we're on case (1).
				break
			} else {
				blankLines++
			}
			if !multiLine {
				break
			}
		}
		if !insideComment && !p.isBlankSep(i) {
			break
		}
	}
	sep := p.advance(i)
	if p.log {
		p.log.Infof("sep: %q\np.index: %v", string(sep), p.index)
		if p.index < p.length {
			p.log.Infof("p.in[p.index]: %q", string(p.in[p.index]))
		}
	}
	return comments, blankLines
}

func (p *parser) isBlankSep(i int) bool {
	return bytes.Contains(spaceSeparators, p.in[i:i+1])
}

func (p *parser) isValueSep(i int) bool {
	return bytes.Contains(valueSeparators, p.in[i:i+1])
}

func (p *parser) advance(i int) string {
	res := p.in[p.index:i]
	p.index = i
	strRes := string(res)
	newlines := strings.Count(strRes, "\n")
	if newlines == 0 {
		p.column += len(strRes)
	} else {
		p.column = len(strRes) - strings.LastIndex(strRes, "\n")
		p.line += newlines
	}
	return string(res)
}

func (p *parser) readValues() ([]*ast.Value, error) {
	var values []*ast.Value
	var previousPos ast.Position
	preComments, _ := p.skipWhiteSpaceAndReadComments(true /* multiLine */)
	if p.nextInputIs('%') {
		values = append(values, p.populateValue(p.readTemplate(), nil))
		previousPos = p.position()
	}
	if p.config.AllowTripleQuotedStrings {
		v, err := p.readTripleQuotedString()
		if err != nil {
			return nil, err
		}
		if v != nil {
			values = append(values, v)
			previousPos = p.position()
		}
	}
	for p.consume('"') || p.consume('\'') {
		// Handle string value.
		stringBegin := p.index - 1 // Index of the quote.
		i := p.index
		for ; i < p.length; i++ {
			if p.in[i] == '\\' {
				i++ // Skip escaped char.
				continue
			}
			if p.in[i] == '\n' {
				p.index = i
				return nil, fmt.Errorf("found literal (unescaped) new line in string at %s", p.errorContext())
			}
			if p.in[i] == p.in[stringBegin] {
				vl := fixQuotes(p.advance(i))
				_ = p.advance(i + 1) // Skip the quote.
				values = append(values, p.populateValue(vl, preComments))

				previousPos = p.position()
				preComments, _ = p.skipWhiteSpaceAndReadComments(true /* multiLine */)
				break
			}
		}
		if i == p.length {
			p.index = i
			return nil, fmt.Errorf("unfinished string at %s", p.errorContext())
		}
	}
	if previousPos != (ast.Position{}) {
		// Rewind comments.
		p.index = int(previousPos.Byte)
		p.line = int(previousPos.Line)
		p.column = int(previousPos.Column)
	} else {
		i := p.index
		// Handle other values.
		for ; i < p.length; i++ {
			if p.isValueSep(i) {
				break
			}
		}
		vl := p.advance(i)
		values = append(values, p.populateValue(vl, nil))
	}
	if p.log {
		p.log.Infof("values: %v", values)
	}
	return values, nil
}

func (p *parser) readTripleQuotedString() (*ast.Value, error) {
	start := p.index
	stringBegin := p.index
	delimiter := `"""`
	if !p.consumeString(delimiter) {
		delimiter = `'''`
		if !p.consumeString(delimiter) {
			return nil, nil
		}
	}

	for {
		if p.consumeString(delimiter) {
			break
		}
		if p.index == p.length {
			p.index = start
			return nil, fmt.Errorf("unfinished string at %s", p.errorContext())
		}
		p.index++
	}

	v := p.populateValue(string(p.in[stringBegin:p.index]), nil)

	return v, nil
}

func (p *parser) populateValue(vl string, preComments []string) *ast.Value {
	if p.log {
		p.log.Infof("value: %q", vl)
	}
	return &ast.Value{
		Value:         vl,
		InlineComment: p.readInlineComment(),
		PreComments:   preComments,
	}
}

func (p *parser) readInlineComment() string {
	inlineComment, _ := p.skipWhiteSpaceAndReadComments(false /* multiLine */)
	if p.log {
		p.log.Infof("inlineComment: %q", strings.Join(inlineComment, "\n"))
	}
	if len(inlineComment) > 0 {
		return inlineComment[0]
	}
	return ""
}

func (p *parser) readTemplate() string {
	if !p.nextInputIs('%') {
		return ""
	}
	i := p.index + 1
	for ; i < p.length; i++ {
		if p.in[i] == '"' || p.in[i] == '\'' {
			stringBegin := i // Index of quote.
			i++
			for ; i < p.length; i++ {
				if p.in[i] == '\\' {
					i++ // Skip escaped char.
					continue
				}
				if p.in[i] == p.in[stringBegin] {
					i++ // Skip end quote.
					break
				}
			}
		}
		if p.in[i] == '%' {
			i++
			break
		}
	}
	return p.advance(i)
}

func sortAndFilterNodes(nodes []*ast.Node, sortFunction func([]*ast.Node), removeDuplicates bool) {
	if len(nodes) == 0 {
		return
	}
	type nameAndValue struct {
		name, value string
	}
	var seen map[nameAndValue]bool
	if removeDuplicates {
		seen = make(map[nameAndValue]bool)
	}
	for _, nd := range nodes {
		if seen != nil && len(nd.Values) == 1 {
			key := nameAndValue{nd.Name, nd.Values[0].Value}
			if _, value := seen[key]; value {
				// Name-Value pair found in the same nesting level, deleting.
				nd.Deleted = true
			} else {
				seen[key] = true
			}
		}
		sortAndFilterNodes(nd.Children, sortFunction, removeDuplicates)
	}
	if sortFunction != nil {
		sortFunction(nodes)
	}
}

func fixQuotes(s string) string {
	res := make([]byte, 0, len(s))
	res = append(res, '"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			res = append(res, '\\')
		} else if s[i] == '\\' {
			res = append(res, s[i])
			i++
		}
		res = append(res, s[i])
	}
	res = append(res, '"')
	return string(res)
}

// DebugFormat returns a textual representation of the specified nodes for
// consumption by humans when debugging (e.g. in test failures). No guarantees
// are made about the specific output.
func DebugFormat(nodes []*ast.Node, depth int) string {
	res := []string{""}
	prefix := strings.Repeat(".", depth)
	for _, nd := range nodes {
		var value string
		if nd.Deleted {
			res = append(res, "DELETED")
		}
		if nd.Children != nil { // Also for 0 children.
			value = fmt.Sprintf("children:%s", DebugFormat(nd.Children, depth+1))
		} else {
			value = fmt.Sprintf("values: %v\n", nd.Values)
		}
		res = append(res,
			fmt.Sprintf("name: %q", nd.Name),
			fmt.Sprintf("PreComments: %q (len %d)", strings.Join(nd.PreComments, "\n"), len(nd.PreComments)),
			value)
	}
	return strings.Join(res, fmt.Sprintf("\n%s ", prefix))
}

// Pretty formats the nodes at the given indentation depth (0 = top-level).
func Pretty(nodes []*ast.Node, depth int) string {
	var result strings.Builder
	formatter{&result}.writeNodes(removeDeleted(nodes), depth, false /* isSameLine */)
	return result.String()
}

func out(nodes []*ast.Node) []byte {
	var result bytes.Buffer
	formatter{&result}.writeNodes(removeDeleted(nodes), 0, false /* isSameLine */)
	return result.Bytes()
}

func nodeSortFunction(sortByFieldName, sortByFieldValue bool) func([]*ast.Node) {
	switch {
	case sortByFieldName && sortByFieldValue:
		return func(ns []*ast.Node) { sort.Stable(ast.ByFieldNameAndValue(ns)) }
	case sortByFieldName:
		return func(ns []*ast.Node) { sort.Stable(ast.ByFieldName(ns)) }
	case sortByFieldValue:
		return func(ns []*ast.Node) { sort.Stable(ast.ByFieldValue(ns)) }
	default:
		return nil
	}
}

// stringWriter abstracts over bytes.Buffer and strings.Builder
type stringWriter interface {
	WriteString(s string) (int, error)
}

// formatter accumulates pretty-printed textproto contents into a stringWriter.
type formatter struct {
	stringWriter
}

func (f formatter) writeNodes(nodes []*ast.Node, depth int, isSameLine bool) {
	indent := " "
	if !isSameLine {
		indent = strings.Repeat(indentSpaces, depth)
	}
	for index, nd := range nodes {
		for _, comment := range nd.PreComments {
			if len(comment) == 0 {
				if !(depth == 0 && index == 0) {
					f.WriteString("\n")
				}
				continue
			}
			f.WriteString(indent)
			f.WriteString(comment)
			f.WriteString("\n")
		}

		if nd.IsCommentOnly() {
			// The comments have been printed already, no more work to do.
			continue
		}
		f.WriteString(indent)
		// Node name may be empty in alternative-style textproto files, because they
		// contain a sequence of proto messages of the same type:
		//   { name: "first_msg" }
		//   { name: "second_msg" }
		// In all other cases, nd.Name is not empty and should be printed.
		if nd.Name != "" {
			f.WriteString(nd.Name)
			if !nd.SkipColon {
				f.WriteString(":")
			}

			// The space after the name is required for one-liners and message fields:
			//   title: "there was a space here"
			//   metadata: { ... }
			// In other cases, there is a newline right after the colon, so no space required.
			if nd.Children != nil || (len(nd.Values) == 1 && len(nd.Values[0].PreComments) == 0) || nd.ValuesAsList {
				f.WriteString(" ")
			}
		}

		if nd.ValuesAsList { // For ValuesAsList option we will preserve even empty list  `field: []`
			f.writeValuesAsList(nd, nd.Values, indent+indentSpaces)
		} else if len(nd.Values) > 0 {
			f.writeValues(nd.Values, indent+indentSpaces)
		}
		if nd.Children != nil { // Also for 0 Children.
			f.writeChildren(nd.Children, depth+1, (isSameLine || nd.ChildrenSameLine))
		}
		if (nd.Children != nil || nd.ValuesAsList) && len(nd.ClosingBraceComment) > 0 {
			f.WriteString(indentSpaces)
			f.WriteString(nd.ClosingBraceComment)
		}

		if !isSameLine {
			f.WriteString("\n")
		}
	}
}

func (f formatter) writeValues(vals []*ast.Value, indent string) {
	if len(vals) == 0 {
		// This should never happen: formatValues can be called only if there are some values.
		return
	}
	sep := "\n" + indent
	if len(vals) == 1 && len(vals[0].PreComments) == 0 {
		sep = ""
	}
	for _, v := range vals {
		f.WriteString(sep)
		for _, comment := range v.PreComments {
			f.WriteString(comment)
			f.WriteString(sep)
		}
		f.WriteString(v.Value)
		if len(v.InlineComment) > 0 {
			f.WriteString(indentSpaces)
			f.WriteString(v.InlineComment)
		}
	}
}

func (f formatter) writeValuesAsList(nd *ast.Node, vals []*ast.Value, indent string) {
	// Checks if it's possible to put whole list in a single line.
	sameLine := nd.ChildrenSameLine && len(nd.PostValuesComments) == 0
	if sameLine {
		// Parser found all children on a same line, but we need to check again.
		// It's possible that AST was modified after parsing.
		for _, val := range vals {
			if len(val.PreComments) > 0 || len(vals[0].InlineComment) > 0 {
				sameLine = false
				break
			}
		}
	}
	sep := ""
	if !sameLine {
		sep = "\n" + indent
	}
	f.WriteString("[")

	for idx, v := range vals {
		for _, comment := range v.PreComments {
			f.WriteString(sep)
			f.WriteString(comment)
		}
		f.WriteString(sep)
		f.WriteString(v.Value)
		if idx < len(vals)-1 { // Don't put trailing comma that fails Python parser.
			f.WriteString(",")
			if sameLine {
				f.WriteString(" ")
			}
		}
		if len(v.InlineComment) > 0 {
			f.WriteString(indentSpaces)
			f.WriteString(v.InlineComment)
		}
	}
	for _, comment := range nd.PostValuesComments {
		f.WriteString(sep)
		f.WriteString(comment)
	}
	f.WriteString(strings.Replace(sep, indentSpaces, "", 1))
	f.WriteString("]")
}

// writeChildren writes the child nodes. The result always ends with a closing brace.
func (f formatter) writeChildren(children []*ast.Node, depth int, sameLine bool) {
	switch {
	case sameLine && len(children) == 0:
		f.WriteString("{}")
	case sameLine:
		f.WriteString("{")
		f.writeNodes(children, depth, sameLine)
		f.WriteString(" }")
	default:
		f.WriteString("{\n")
		f.writeNodes(children, depth, sameLine)
		f.WriteString(strings.Repeat(indentSpaces, depth-1))
		f.WriteString("}")
	}
}
