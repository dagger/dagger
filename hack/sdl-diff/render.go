package main

import (
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// Render members separately so annotations can be placed before descriptions,
// never inside a block string. The formatter still owns all SDL syntax.
func renderField(f *ast.FieldDefinition) string {
	if f == nil {
		return ""
	}
	return memberBody(renderDefinition(&ast.Definition{Kind: ast.Object, Name: "_", Fields: ast.FieldList{f}}, false))
}

func renderEnumValue(v *ast.EnumValueDefinition) string {
	if v == nil {
		return ""
	}
	return memberBody(renderDefinition(&ast.Definition{Kind: ast.Enum, Name: "_", EnumValues: ast.EnumValueList{v}}, false))
}

func memberBody(s string) string {
	_, s, _ = strings.Cut(s, "{\n")
	s = strings.TrimSuffix(s, "}\n")
	var out strings.Builder
	for line := range strings.SplitSeq(strings.TrimRight(s, "\n"), "\n") {
		out.WriteString(strings.TrimPrefix(line, "  ") + "\n")
	}
	return out.String()
}

func indent(s string) string {
	var out strings.Builder
	for line := range strings.SplitSeq(strings.TrimRight(s, "\n"), "\n") {
		if line != "" {
			out.WriteString("  " + line)
		}
		out.WriteString("\n")
	}
	return out.String()
}

// renderChangedDefinition renders the selected members and metadata, leaving
// schema comparison and canonical detailed-diff construction to the caller.
func renderChangedDefinition(before, after *ast.Definition, members string, headerChanged bool) string {
	var fragment strings.Builder
	removalsOnly := !headerChanged && len(after.Fields)+len(after.EnumValues)+len(after.Interfaces)+len(after.Types) == 0
	if headerChanged {
		fragment.WriteString(changeComment(definitionHeader(before), definitionHeader(after)))
	}
	for _, change := range []struct {
		label          string
		added, removed []string
	}{
		{"implements", after.Interfaces, before.Interfaces}, {"union member", after.Types, before.Types},
	} {
		for _, name := range change.added {
			fragment.WriteString("# Added: " + change.label + " " + name + "\n")
		}
		for _, removed := range change.removed {
			context := ""
			if removalsOnly {
				context = after.Name + " "
			}
			fragment.WriteString("# Removed: " + context + change.label + " " + removed + "\n")
		}
	}
	// A fragment is an after declaration, not a migration. Removals exist
	// only in comments, never as live SDL members or empty type bodies.
	if removalsOnly {
		for _, field := range before.Fields {
			fragment.WriteString("# Removed: " + after.Name + "." + signature(renderField(field)) + "\n")
		}
		for _, value := range before.EnumValues {
			fragment.WriteString("# Removed: " + after.Name + "." + signature(renderEnumValue(value)) + "\n")
		}
	} else {
		header := *after
		header.Fields, header.EnumValues = nil, nil
		if !hasMemberBody(after.Kind) || len(after.Fields)+len(after.EnumValues) == 0 {
			fragment.WriteString(members)
		}
		fragment.WriteString(strings.TrimSpace(renderDefinition(&header, !headerChanged)))
		if len(after.Fields)+len(after.EnumValues) > 0 && hasMemberBody(after.Kind) {
			fragment.WriteString(" {\n" + members + "}")
		}
		fragment.WriteString("\n")
	}
	return fragment.String()
}

func annotatedField(before, after *ast.FieldDefinition) string {
	a, b := renderField(before), renderField(after)
	comment := changeComment(a, b)
	if before != nil && after != nil && comment == "# Description changed\n" {
		var changed []string
		if before.Description != after.Description {
			changed = append(changed, "field")
		}
		for _, arg := range after.Arguments {
			if old := before.Arguments.ForName(arg.Name); old != nil && old.Description != arg.Description {
				changed = append(changed, "argument "+arg.Name)
			}
		}
		if len(changed) > 0 && (len(changed) != 1 || changed[0] != "field") {
			comment = "# Description changed: " + strings.Join(changed, ", ") + "\n"
		}
	}
	return comment + b
}

func annotatedChange(before, after string) string {
	return changeComment(before, after) + after
}

func changeComment(before, after string) string {
	switch {
	case before == "":
		return "# Added\n"
	case after == "":
		return "# Removed: " + signature(before) + "\n"
	case withoutDescriptions(before) == withoutDescriptions(after):
		return "# Description changed\n"
	default:
		return "# Changed: previously " + signature(before) + "\n"
	}
}

// These inputs are formatter-produced declarations or individual members.
// Parse instead of removing quoted lines: quotes can also occur in defaults
// and directive arguments, and descriptions can span arbitrary lines.
func descriptionless(s string) (*ast.SchemaDocument, string) {
	for _, wrapper := range []string{"", "type", "input", "enum"} {
		input := s
		if wrapper != "" {
			input = wrapper + " _ {\n" + s + "}\n"
		}
		if doc, err := parseDocument("fragment", input, false); err == nil {
			return doc, wrapper
		}
	}
	panic("invalid formatter-produced SDL fragment")
}

func withoutDescriptions(s string) string {
	doc, _ := descriptionless(s)
	return format(canonicalize(doc))
}

func signature(s string) string {
	doc, wrapper := descriptionless(s)
	switch wrapper {
	case "type", "input":
		s = renderField(doc.Definitions[0].Fields[0])
	case "enum":
		s = renderEnumValue(doc.Definitions[0].EnumValues[0])
	default:
		// Summarize removed/replaced types by their header, not every member.
		for _, defs := range []ast.DefinitionList{doc.Definitions, doc.Extensions} {
			for _, def := range defs {
				def.Fields, def.EnumValues = nil, nil
			}
		}
		s = format(doc)
	}
	// Only collapse layout whitespace, not whitespace inside string literals.
	var lines []string
	for line := range strings.SplitSeq(strings.TrimSpace(s), "\n") {
		lines = append(lines, strings.TrimSpace(line))
	}
	return strings.Join(lines, " ")
}

// Diff only selected, canonical fragments, never the full schema. Trim common
// edges first and cap the LCS matrix so even adversarially large descriptions
// cannot cause unbounded quadratic work or allocation. Above the cap, retain
// the common edges and emit the middle as an exact (non-minimal) replacement.
func lineDiff(before, after string) string {
	lines := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	}
	a, b := lines(before), lines(after)
	var out strings.Builder
	emit := func(prefix string, lines []string) {
		for _, line := range lines {
			out.WriteString(prefix + line + "\n")
		}
	}
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		emit(" ", a[:1])
		a, b = a[1:], b[1:]
	}
	suffix := 0
	for suffix < len(a) && suffix < len(b) && a[len(a)-suffix-1] == b[len(b)-suffix-1] {
		suffix++
	}
	tail := a[len(a)-suffix:]
	a, b = a[:len(a)-suffix], b[:len(b)-suffix]
	const maxCells = 1_000_000
	if len(a) > 0 && len(b) > 0 && len(a)+1 <= maxCells/(len(b)+1) {
		width := len(b) + 1
		lcs := make([]uint32, (len(a)+1)*width)
		for i := len(a) - 1; i >= 0; i-- {
			for j := len(b) - 1; j >= 0; j-- {
				if a[i] == b[j] {
					lcs[i*width+j] = lcs[(i+1)*width+j+1] + 1
				} else {
					lcs[i*width+j] = max(lcs[(i+1)*width+j], lcs[i*width+j+1])
				}
			}
		}
		i, j := 0, 0
		for i < len(a) && j < len(b) {
			switch {
			case a[i] == b[j]:
				emit(" ", a[i:i+1])
				i++
				j++
			case lcs[(i+1)*width+j] >= lcs[i*width+j+1]:
				emit("-", a[i:i+1])
				i++
			default:
				emit("+", b[j:j+1])
				j++
			}
		}
		a, b = a[i:], b[j:]
	}
	emit("-", a)
	emit("+", b)
	emit(" ", tail)
	return out.String()
}
