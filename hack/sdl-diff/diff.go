package main

import (
	"cmp"
	"slices"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// semanticDiff emits additions as SDL and removals/replacements as comments:
// GraphQL extensions cannot remove or replace existing declarations.
func semanticDiff(old, new *ast.SchemaDocument) string {
	extensionOnly := map[string]bool{}
	for _, ext := range new.Extensions {
		extensionOnly[ext.Name] = new.Definitions.ForName(ext.Name) == nil
	}
	mergeExtensions(old)
	mergeExtensions(new)
	canonicalOld := canonicalize(old)
	canonicalNew := canonicalize(new)
	var out strings.Builder
	emit := func(label, s string) {
		if s == "" {
			return
		}
		if label != "" {
			out.WriteString("# " + label + "\n")
			for line := range strings.SplitSeq(strings.TrimSpace(s), "\n") {
				out.WriteString("# " + line + "\n")
			}
		} else {
			out.WriteString(s)
		}
		out.WriteString("\n")
	}
	compare := func(before, after string) {
		switch {
		case before == after:
		case before == "":
			emit("", after)
		case after == "":
			emit("Removed:", before)
		default:
			emit("Changed (before):", before)
			emit("Changed (after):", after)
		}
	}
	if format(&ast.SchemaDocument{Schema: canonicalOld.Schema}) != format(&ast.SchemaDocument{Schema: canonicalNew.Schema}) {
		compare(format(&ast.SchemaDocument{Schema: old.Schema}), format(&ast.SchemaDocument{Schema: new.Schema}))
	}
	for _, name := range names(old.Directives, new.Directives, func(d *ast.DirectiveDefinition) string { return d.Name }) {
		render := func(d *ast.DirectiveDefinition) string {
			if d == nil {
				return ""
			}
			return format(&ast.SchemaDocument{Directives: ast.DirectiveDefinitionList{d}})
		}
		if render(canonicalOld.Directives.ForName(name)) != render(canonicalNew.Directives.ForName(name)) {
			compare(render(old.Directives.ForName(name)), render(new.Directives.ForName(name)))
		}
	}
	for _, name := range names(old.Definitions, new.Definitions, func(d *ast.Definition) string { return d.Name }) {
		before, after := old.Definitions.ForName(name), new.Definitions.ForName(name)
		if before == nil || after == nil {
			compare(renderDefinition(before, false), renderDefinition(after, extensionOnly[name]))
			continue
		}
		canonicalBefore, canonicalAfter := canonicalOld.Definitions.ForName(name), canonicalNew.Definitions.ForName(name)
		// Kind, type-level directives, and descriptions cannot be replaced with
		// an extension. Show the complete replacement when these change.
		header := func(d *ast.Definition) string {
			return renderDefinition(&ast.Definition{Kind: d.Kind, Name: d.Name, Description: d.Description, Directives: d.Directives}, false)
		}
		if header(canonicalBefore) != header(canonicalAfter) {
			compare(renderDefinition(before, false), renderDefinition(after, false))
			continue
		}
		added := &ast.Definition{Kind: after.Kind, Name: name}
		removed := &ast.Definition{Kind: before.Kind, Name: name}
		changedBefore := &ast.Definition{Kind: before.Kind, Name: name}
		changedAfter := &ast.Definition{Kind: after.Kind, Name: name}
		for _, field := range names(before.Fields, after.Fields, func(f *ast.FieldDefinition) string { return f.Name }) {
			a, b := before.Fields.ForName(field), after.Fields.ForName(field)
			switch {
			case a == nil:
				added.Fields = append(added.Fields, b)
			case b == nil:
				removed.Fields = append(removed.Fields, a)
			default:
				render := func(f *ast.FieldDefinition) string {
					return renderDefinition(&ast.Definition{Kind: after.Kind, Name: name, Fields: ast.FieldList{f}}, true)
				}
				if render(canonicalBefore.Fields.ForName(field)) != render(canonicalAfter.Fields.ForName(field)) {
					changedAfter.Fields = append(changedAfter.Fields, b)
				}
			}
		}
		for _, value := range names(before.EnumValues, after.EnumValues, func(v *ast.EnumValueDefinition) string { return v.Name }) {
			a, b := before.EnumValues.ForName(value), after.EnumValues.ForName(value)
			switch {
			case a == nil:
				added.EnumValues = append(added.EnumValues, b)
			case b == nil:
				removed.EnumValues = append(removed.EnumValues, a)
			default:
				render := func(v *ast.EnumValueDefinition) string {
					return renderDefinition(&ast.Definition{Kind: ast.Enum, Name: name, EnumValues: ast.EnumValueList{v}}, true)
				}
				if render(canonicalBefore.EnumValues.ForName(value)) != render(canonicalAfter.EnumValues.ForName(value)) {
					changedAfter.EnumValues = append(changedAfter.EnumValues, b)
				}
			}
		}
		for _, field := range before.Fields {
			if changedAfter.Fields.ForName(field.Name) != nil {
				changedBefore.Fields = append(changedBefore.Fields, field)
			}
		}
		for _, value := range before.EnumValues {
			if changedAfter.EnumValues.ForName(value.Name) != nil {
				changedBefore.EnumValues = append(changedBefore.EnumValues, value)
			}
		}
		added.Interfaces, removed.Interfaces = difference(before.Interfaces, after.Interfaces)
		added.Types, removed.Types = difference(before.Types, after.Types)
		renderChange := func(d *ast.Definition) string {
			if len(d.Fields)+len(d.EnumValues)+len(d.Interfaces)+len(d.Types) == 0 {
				return ""
			}
			return renderDefinition(d, true)
		}
		emit("", renderChange(added))
		emit("Removed:", renderChange(removed))
		if renderChange(changedBefore) != "" {
			emit("Changed (before):", renderChange(changedBefore))
			emit("Changed (after):", renderChange(changedAfter))
		}
	}
	return out.String()
}

func renderDefinition(d *ast.Definition, extension bool) string {
	if d == nil {
		return ""
	}
	doc := &ast.SchemaDocument{}
	if extension {
		doc.Extensions = ast.DefinitionList{d}
	} else {
		doc.Definitions = ast.DefinitionList{d}
	}
	return format(doc)
}

func names[T any](a, b []T, name func(T) string) []string {
	// Follow the new schema, then append removed names in old schema order.
	var result []string
	seen := map[string]bool{}
	for _, list := range [][]T{b, a} {
		for _, item := range list {
			key := name(item)
			if !seen[key] {
				seen[key] = true
				result = append(result, key)
			}
		}
	}
	return result
}

func difference(before, after []string) (added, removed []string) {
	for _, s := range after {
		if !slices.Contains(before, s) {
			added = append(added, s)
		}
	}
	for _, s := range before {
		if !slices.Contains(after, s) {
			removed = append(removed, s)
		}
	}
	return
}

func mergeExtensions(doc *ast.SchemaDocument) {
	for _, ext := range doc.Extensions {
		if def := doc.Definitions.ForName(ext.Name); def != nil {
			def.Fields = append(def.Fields, ext.Fields...)
			def.EnumValues = append(def.EnumValues, ext.EnumValues...)
			def.Interfaces = append(def.Interfaces, ext.Interfaces...)
			def.Types = append(def.Types, ext.Types...)
			def.Directives = append(def.Directives, ext.Directives...)
		} else {
			doc.Definitions = append(doc.Definitions, ext)
		}
	}
	doc.Extensions = nil
	doc.Schema = append(doc.Schema, doc.SchemaExtension...)
	doc.SchemaExtension = nil
	if len(doc.Schema) > 0 {
		first := doc.Schema[0]
		for _, ext := range doc.Schema[1:] {
			first.OperationTypes = append(first.OperationTypes, ext.OperationTypes...)
			first.Directives = append(first.Directives, ext.Directives...)
		}
		doc.Schema = doc.Schema[:1]
	}
}

// Canonicalize a copy for comparison, leaving declaration order intact for output.
// Directive application order and list value order are retained because they can matter.
func canonicalize(source *ast.SchemaDocument) *ast.SchemaDocument {
	doc := *source
	doc.Schema = cloneNodes(doc.Schema)
	doc.Directives = cloneNodes(doc.Directives)
	doc.Definitions = cloneNodes(doc.Definitions)
	for _, first := range doc.Schema {
		first.OperationTypes = slices.Clone(first.OperationTypes)
		slices.SortFunc(first.OperationTypes, func(a, b *ast.OperationTypeDefinition) int { return cmp.Compare(a.Operation, b.Operation) })
		first.Directives = canonicalDirectives(first.Directives)
	}
	for _, def := range doc.Directives {
		def.Arguments = canonicalArguments(def.Arguments)
		def.Locations = slices.Clone(def.Locations)
		slices.Sort(def.Locations)
	}
	for _, def := range doc.Definitions {
		def.Interfaces = slices.Clone(def.Interfaces)
		def.Types = slices.Clone(def.Types)
		def.Fields = cloneNodes(def.Fields)
		def.EnumValues = cloneNodes(def.EnumValues)
		slices.Sort(def.Interfaces)
		slices.Sort(def.Types)
		slices.SortFunc(def.Fields, func(a, b *ast.FieldDefinition) int { return cmp.Compare(a.Name, b.Name) })
		slices.SortFunc(def.EnumValues, func(a, b *ast.EnumValueDefinition) int { return cmp.Compare(a.Name, b.Name) })
		def.Directives = canonicalDirectives(def.Directives)
		for _, field := range def.Fields {
			field.Arguments = canonicalArguments(field.Arguments)
			field.DefaultValue = canonicalValue(field.DefaultValue)
			field.Directives = canonicalDirectives(field.Directives)
		}
		for _, value := range def.EnumValues {
			value.Directives = canonicalDirectives(value.Directives)
		}
	}
	return &doc
}

func cloneNodes[T any, S ~[]*T](nodes S) S {
	clones := make(S, len(nodes))
	for i, node := range nodes {
		clone := *node
		clones[i] = &clone
	}
	return clones
}

func canonicalArguments(args ast.ArgumentDefinitionList) ast.ArgumentDefinitionList {
	args = cloneNodes(args)
	slices.SortFunc(args, func(a, b *ast.ArgumentDefinition) int { return cmp.Compare(a.Name, b.Name) })
	for _, arg := range args {
		arg.DefaultValue = canonicalValue(arg.DefaultValue)
		arg.Directives = canonicalDirectives(arg.Directives)
	}
	return args
}

func canonicalDirectives(directives ast.DirectiveList) ast.DirectiveList {
	directives = cloneNodes(directives)
	for _, dir := range directives {
		dir.Arguments = cloneNodes(dir.Arguments)
		slices.SortFunc(dir.Arguments, func(a, b *ast.Argument) int { return cmp.Compare(a.Name, b.Name) })
		for _, arg := range dir.Arguments {
			arg.Value = canonicalValue(arg.Value)
		}
	}
	return directives
}

func canonicalValue(value *ast.Value) *ast.Value {
	if value == nil {
		return nil
	}
	clone := *value
	value = &clone
	value.Children = cloneNodes(value.Children)
	if value.Kind == ast.ObjectValue {
		slices.SortFunc(value.Children, func(a, b *ast.ChildValue) int { return cmp.Compare(a.Name, b.Name) })
	}
	for _, child := range value.Children {
		child.Value = canonicalValue(child.Value)
	}
	return value
}
