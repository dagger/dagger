package main

import (
	"cmp"
	"slices"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// semanticDiff is the CLI's annotated after-SDL summary. PRs also include the
// selected before/after fragments as a collapsed line diff.
func semanticDiff(old, new *ast.SchemaDocument) string {
	return compareSchemas(old, new).summary
}

type schemaDiff struct {
	summary string
	details string
}

func compareSchemas(old, new *ast.SchemaDocument) schemaDiff {
	extensionOnly := map[string]bool{}
	for _, ext := range new.Extensions {
		extensionOnly[ext.Name] = new.Definitions.ForName(ext.Name) == nil
	}
	mergeExtensions(old)
	mergeExtensions(new)
	canonicalOld := canonicalize(old)
	canonicalNew := canonicalize(new)
	var summary, details strings.Builder
	emit := func(s, before, after string) {
		if s != "" {
			summary.WriteString(strings.TrimRight(s, "\n") + "\n\n")
			details.WriteString(lineDiff(before, after) + "\n")
		}
	}
	compare := func(before, after, canonicalBefore, canonicalAfter string) {
		if canonicalBefore != canonicalAfter {
			emit(annotatedChange(before, after), canonicalBefore, canonicalAfter)
		}
	}
	renderSchema := func(defs ast.SchemaDefinitionList) string {
		if len(defs) > 0 && len(defs[0].OperationTypes) == 0 {
			return format(&ast.SchemaDocument{SchemaExtension: defs})
		}
		return format(&ast.SchemaDocument{Schema: defs})
	}
	compare(renderSchema(old.Schema), renderSchema(new.Schema), renderSchema(canonicalOld.Schema), renderSchema(canonicalNew.Schema))
	for _, name := range names(old.Directives, new.Directives, func(d *ast.DirectiveDefinition) string { return d.Name }) {
		render := func(d *ast.DirectiveDefinition) string {
			if d == nil {
				return ""
			}
			return format(&ast.SchemaDocument{Directives: ast.DirectiveDefinitionList{d}})
		}
		compare(render(old.Directives.ForName(name)), render(new.Directives.ForName(name)),
			render(canonicalOld.Directives.ForName(name)), render(canonicalNew.Directives.ForName(name)))
	}
	for _, name := range names(old.Definitions, new.Definitions, func(d *ast.Definition) string { return d.Name }) {
		before, after := old.Definitions.ForName(name), new.Definitions.ForName(name)
		canonicalBefore, canonicalAfter := canonicalOld.Definitions.ForName(name), canonicalNew.Definitions.ForName(name)
		if before == nil || after == nil {
			compare(renderDefinition(before, false), renderDefinition(after, extensionOnly[name]),
				renderDefinition(canonicalBefore, false), renderDefinition(canonicalAfter, extensionOnly[name]))
			continue
		}
		// Select changed members only, even when the type's own metadata changes.
		// Both sides use the same selection so detailed context is emitted once.
		a, b := *before, *after
		a.Fields, b.Fields = nil, nil
		a.EnumValues, b.EnumValues = nil, nil
		headerChanged := definitionHeader(canonicalBefore) != definitionHeader(canonicalAfter)
		if !headerChanged {
			a.Description, b.Description = "", ""
			a.Directives, b.Directives = nil, nil
		}
		b.Interfaces, a.Interfaces = difference(before.Interfaces, after.Interfaces)
		b.Types, a.Types = difference(before.Types, after.Types)
		var members strings.Builder
		for _, field := range names(before.Fields, after.Fields, func(f *ast.FieldDefinition) string { return f.Name }) {
			oldField, newField := before.Fields.ForName(field), after.Fields.ForName(field)
			if renderField(canonicalBefore.Fields.ForName(field)) == renderField(canonicalAfter.Fields.ForName(field)) {
				continue
			}
			if oldField != nil {
				a.Fields = append(a.Fields, oldField)
			}
			if newField != nil {
				b.Fields = append(b.Fields, newField)
			}
			members.WriteString(indent(annotatedField(oldField, newField)))
		}
		for _, value := range names(before.EnumValues, after.EnumValues, func(v *ast.EnumValueDefinition) string { return v.Name }) {
			oldValue, newValue := before.EnumValues.ForName(value), after.EnumValues.ForName(value)
			if renderEnumValue(canonicalBefore.EnumValues.ForName(value)) == renderEnumValue(canonicalAfter.EnumValues.ForName(value)) {
				continue
			}
			if oldValue != nil {
				a.EnumValues = append(a.EnumValues, oldValue)
			}
			if newValue != nil {
				b.EnumValues = append(b.EnumValues, newValue)
			}
			members.WriteString(indent(annotatedChange(renderEnumValue(oldValue), renderEnumValue(newValue))))
		}
		if !headerChanged && members.Len() == 0 && len(a.Interfaces)+len(b.Interfaces)+len(a.Types)+len(b.Types) == 0 {
			continue
		}
		fragment := renderChangedDefinition(&a, &b, members.String(), headerChanged)
		sharedBody := hasMemberBody(a.Kind) && hasMemberBody(b.Kind) &&
			len(a.Fields)+len(b.Fields)+len(a.EnumValues)+len(b.EnumValues) > 0
		selected := func(d *ast.Definition) string {
			doc := canonicalize(&ast.SchemaDocument{Definitions: ast.DefinitionList{d}})
			s := renderDefinition(doc.Definitions[0], !headerChanged)
			// Keep the enclosing braces as shared diff context when all selected
			// members are added or removed. Empty fragments are not full schemas.
			if sharedBody && len(d.Fields)+len(d.EnumValues) == 0 {
				s = strings.TrimRight(s, "\n") + " {\n}\n"
			}
			return s
		}
		emit(fragment, selected(&a), selected(&b))
	}
	return schemaDiff{summary: summary.String(), details: details.String()}
}

func hasMemberBody(kind ast.DefinitionKind) bool {
	return kind == ast.Object || kind == ast.Interface || kind == ast.InputObject || kind == ast.Enum
}

func definitionHeader(d *ast.Definition) string {
	return renderDefinition(&ast.Definition{Kind: d.Kind, Name: d.Name, Description: d.Description, Directives: d.Directives}, false)
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
