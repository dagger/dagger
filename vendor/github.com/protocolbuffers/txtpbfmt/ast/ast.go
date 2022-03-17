// Package ast provides data structure representing textproto syntax tree.
package ast

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Position describes a position of a token in the input.
// Both byte-based and line/column-based positions are maintained
// because different downstream consumers need different formats
// and we don't want to keep the entire input in memory to be able
// to convert between the two.
// Fields Byte, Line and Column should be interpreted as
// ByteRange.start_byte, TextRange.start_line, and TextRange.start_column
// of devtools.api.Range proto.
type Position struct {
	Byte   uint32
	Line   int32
	Column int32
}

// Node represents a field with a value in a proto message, or a comment unattached to a field.
type Node struct {
	// Start describes the start position of the node.
	// For nodes that span entire lines, this is the first character
	// of the first line attributed to the node; possible a whitespace if the node is indented.
	// For nodes that are members of one-line message literals,
	// this is the first non-whitespace character encountered.
	Start Position
	// Lines of comments appearing before the field.
	// Each non-empty line starts with a # and does not contain the trailing newline.
	PreComments []string
	// Name of proto field (eg 'presubmit'). Will be an empty string for comment-only
	// nodes and unqualified messages, e.g.
	//     { name: "first_msg" }
	//     { name: "second_msg" }
	Name string
	// Values, for nodes that don't have children.
	Values []*Value
	// Children for nodes that have children.
	Children []*Node
	// Whether or not this node was deleted by edits.
	Deleted bool
	// Should the colon after the field name be omitted?
	// (e.g. "presubmit: {" vs "presubmit {")
	SkipColon bool
	// Whether or not all children are in the same line.
	// (eg "base { id: "id" }")
	ChildrenSameLine bool
	// Comment in the same line as the "}".
	ClosingBraceComment string
	// End holds the position suitable for inserting new items.
	// For multi-line nodes, this is the first character on the line with the closing brace.
	// For single-line nodes, this is the first character after the last item (usually a space).
	// For non-message nodes, this is Position zero value.
	End Position
	// Keep values in list (e.g "list: [1, 2]").
	ValuesAsList bool
	// Lines of comments appearing after last value inside list.
	// Each non-empty line starts with a # and does not contain the trailing newline.
	// e.g
	// field: [
	//   value
	//   # Comment
	// ]
	PostValuesComments []string
}

func sortableNodes(ns []*Node) sortable {
	return sortable(ns)
}

type sortable []*Node

func (ns sortable) Len() int {
	return len(ns)
}

func (ns sortable) Swap(i, j int) {
	ns[i], ns[j] = ns[j], ns[i]
}

// ByFieldName constructs a sort.Interface that sorts nodes by their field name.
func ByFieldName(ns []*Node) sort.Interface {
	return byFieldName{sortableNodes(ns)}
}

type byFieldName struct{ sortable }

func (ns byFieldName) Less(i, j int) bool {
	ni, nj := ns.sortable[i], ns.sortable[j]
	return ni.Name < nj.Name
}

// ByFieldValue constructs a sort.Interface that sorts adjacent scalar nodes with the same name by
// their value.
func ByFieldValue(ns []*Node) sort.Interface {
	return byFieldValue{sortableNodes(ns)}
}

type byFieldValue struct{ sortable }

func (ns byFieldValue) Less(i, j int) bool {
	ni, nj := ns.sortable[i], ns.sortable[j]
	if ni.Name != nj.Name || len(ni.Values) != 1 || len(nj.Values) != 1 {
		return false
	}
	return ni.Values[0].Value < nj.Values[0].Value
}

// ByFieldNameAndValue constructs a sort.Interface that sorts nodes by their field name and scalar
// value.
func ByFieldNameAndValue(ns []*Node) sort.Interface {
	return byFieldNameAndValue{sortableNodes(ns)}
}

type byFieldNameAndValue struct{ sortable }

func (ns byFieldNameAndValue) Less(i, j int) bool {
	ni, nj := ns.sortable[i], ns.sortable[j]
	if ni.Name != nj.Name {
		return ni.Name < nj.Name
	}
	if len(ni.Values) != 1 || len(nj.Values) != 1 {
		return false
	}
	return ni.Values[0].Value < nj.Values[0].Value
}

// IsCommentOnly returns true if this is a comment-only node.
func (n *Node) IsCommentOnly() bool {
	return n.Name == "" && n.Children == nil
}

type fixData struct {
	inline bool
}

// Fix fixes inconsistencies that may arise after manipulation.
//
// For example if a node is ChildrenSameLine but has non-inline children, or
// children with comments ChildrenSameLine will be set to false.
func (n *Node) Fix() {
	n.fix()
}

func (n *Node) fix() fixData {
	d := fixData{
		// ChildrenSameLine may be false for cases with no children such as a
		// value `foo: false`. We don't want these to trigger expansion.
		inline: n.ChildrenSameLine || len(n.Children) == 0,
	}

	for _, c := range n.Children {
		if c.Deleted {
			continue
		}

		cd := c.fix()
		if !cd.inline {
			d.inline = false
		}
	}

	for _, v := range n.Values {
		vd := v.fix()
		if !vd.inline {
			d.inline = false
		}
	}

	n.ChildrenSameLine = d.inline

	// textproto comments go until the end of the line, so we must force parents
	// to be multiline otherwise we will partially comment them out.
	if len(n.PreComments) > 0 || len(n.ClosingBraceComment) > 0 {
		d.inline = false
	}

	return d
}

// StringNode is a helper for constructing simple string nodes.
func StringNode(name, unquoted string) *Node {
	return &Node{Name: name, Values: []*Value{{Value: strconv.Quote(unquoted)}}}
}

// Value represents a field value in a proto message.
type Value struct {
	// Lines of comments appearing before the value (for multi-line strings).
	// Each non-empty line starts with a # and does not contain the trailing newline.
	PreComments []string
	// Node value (eg 'ERROR').
	Value string
	// Comment in the same line as the value.
	InlineComment string
}

func (v *Value) String() string {
	return fmt.Sprintf("{Value: %q, PreComments: %q, InlineComment: %q}", v.Value, strings.Join(v.PreComments, "\n"), v.InlineComment)
}

func (v *Value) fix() fixData {
	return fixData{
		inline: len(v.PreComments) == 0 && v.InlineComment == "",
	}
}

// GetFromPath returns all nodes with a given string path in the parse tree. See ast_test.go for examples.
func GetFromPath(nodes []*Node, path []string) []*Node {
	if len(path) == 0 {
		return nil
	}
	res := []*Node{}
	for _, node := range nodes {
		if node.Name == path[0] {
			if len(path) == 1 {
				res = append(res, node)
			} else {
				res = append(res, GetFromPath(node.Children, path[1:])...)
			}
		}
	}
	return res
}
