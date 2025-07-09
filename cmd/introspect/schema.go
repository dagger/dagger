package main

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/spf13/cobra"
)

// Schema generates and outputs a graphqls schema file for dagger.
//
// The actual implementation is heavily inspired by
// https://github.com/graphql/graphql-js/blob/v14.2.1/src/utilities/schemaPrinter.js,
// which is what our previous implementation, `graphql-json-to-sdl`, was using.
func Schema(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	resp, err := getIntrospection(ctx)
	if err != nil {
		return err
	}

	var result strings.Builder
	for _, tp := range resp.Schema.Directives {
		if slices.Contains([]string{"deprecated"}, tp.Name) {
			// builtin graphql directives - these need to be special cased
			continue
		}
		formatDirective(&result, tp)
		fmt.Fprintln(&result)
	}
	for _, tp := range resp.Schema.Types {
		if slices.Contains([]string{"String", "Int", "Float", "Boolean", "ID"}, tp.Name) {
			// builtin graphql types - these need to be special cased
			continue
		}
		formatType(&result, tp)
		fmt.Fprintln(&result)
	}
	fmt.Println(result.String())

	return nil
}

func formatDirective(w io.Writer, d *introspection.DirectiveDef) {
	if d == nil {
		return
	}

	if d.Description != "" {
		formatDescription(w, "", d.Description)
	}
	fmt.Fprintf(w, "directive @%s", d.Name)
	formatArgs(w, "", d.Args)

	if len(d.Locations) > 0 {
		fmt.Fprintf(w, " on %s", strings.Join(d.Locations, " | "))
	}

	fmt.Fprintln(w)
}

func formatType(w io.Writer, t *introspection.Type) {
	if t == nil {
		return
	}

	if t.Description != "" {
		formatDescription(w, "", t.Description)
	}

	switch t.Kind {
	case introspection.TypeKindScalar:
		fmt.Fprintf(w, "scalar %s", t.Name)

	case introspection.TypeKindEnum:
		fmt.Fprintf(w, "enum %s {\n", t.Name)
		formatDescribed(w, t.EnumValues, func(value introspection.EnumValue) string { return value.Description }, formatEnumValue)
		fmt.Fprint(w, "}")

	case introspection.TypeKindInputObject:
		fmt.Fprintf(w, "input %s {\n", t.Name)
		formatDescribed(w, t.InputFields, func(value introspection.InputValue) string { return value.Description }, formatInput)
		fmt.Fprint(w, "}")

	case introspection.TypeKindObject:
		fmt.Fprintf(w, "type %s", t.Name)

		// add interfaces if present
		// if len(t.Interfaces) > 0 {
		// 	interfaces := make([]string, len(t.Interfaces))
		// 	for i, iface := range t.Interfaces {
		// 		interfaces[i] = iface.Name
		// 	}
		// 	fmt.Fprintf(w, " implements %s", strings.Join(interfaces, " & "))
		// }

		fmt.Fprint(w, " {\n")
		formatDescribed(w, t.Fields, func(field *introspection.Field) string { return field.Description }, formatField)
		fmt.Fprint(w, "}")

	case introspection.TypeKindInterface:
		fmt.Fprintf(w, "interface %s {\n", t.Name)
		formatDescribed(w, t.Fields, func(field *introspection.Field) string { return field.Description }, formatField)
		fmt.Fprint(w, "}")

	case introspection.TypeKindUnion:
		fmt.Fprintf(w, "union %s = %s", t.Name, "???")
		panic("unimplemented union handler")

	default:
		panic(fmt.Sprintf("unknown kind %q", t.Kind))
	}

	fmt.Fprintln(w)

	if len(t.Directives) > 0 {
		formatDirectiveApplications(w, t.Directives)
	}
}

func formatDescription(w io.Writer, indent string, description string) {
	if description == "" {
		return
	}

	lines := descriptionLines(description, 120-len(indent))
	for i, line := range lines {
		if len(line) == 0 {
			// avoid indenting empty lines
			continue
		}
		lines[i] = indent + line
	}

	text := strings.Join(lines, "\n") + "\n"
	if len(lines) > 1 || preferMultipleLines(text) {
		fmt.Fprint(w, indent+`"""`+"\n"+text+indent+`"""`+"\n")
	} else {
		fmt.Fprint(w, indent+`"""`+strings.TrimSpace(text)+`"""`+"\n")
	}
}

func preferMultipleLines(text string) bool {
	text = strings.TrimSpace(text)

	// long text
	if len(text) > 70 {
		return true
	}

	// trailing quotes or slashes forces trailing new line
	if strings.HasSuffix(text, `"`) && !strings.HasSuffix(text, `"""`) {
		return true
	} else if strings.HasSuffix(text, `'`) {
		return true
	} else if strings.HasSuffix(text, `\`) {
		return true
	}

	return false
}

func formatInput(w io.Writer, input introspection.InputValue) {
	fmt.Fprintf(w, "  %s: %s\n", input.Name, typeRefToString(input.TypeRef))
}

func formatEnumValue(w io.Writer, enumVal introspection.EnumValue) {
	fmt.Fprintf(w, "  %s\n", enumVal.Name)
}

func formatField(w io.Writer, field *introspection.Field) {
	fmt.Fprintf(w, "  %s", field.Name)
	formatArgs(w, "  ", field.Args)
	if field.TypeRef != nil {
		fmt.Fprintf(w, ": %s", typeRefToString(field.TypeRef))
	}
	if len(field.Directives) > 0 {
		formatDirectiveApplications(w, field.Directives)
	}

	fmt.Fprintln(w)
}

func formatArgs(w io.Writer, indent string, args introspection.InputValues) {
	if len(args) == 0 {
		return
	}

	multiline := false
	for _, arg := range args {
		if arg.Description != "" {
			multiline = true
			break
		}
	}

	fmt.Fprint(w, "(")
	for i, arg := range args {
		if i > 0 {
			if multiline {
				fmt.Fprintln(w)
			} else {
				fmt.Fprint(w, ", ")
			}
		}

		// Add argument description if present
		if multiline {
			fmt.Fprintln(w)
			formatDescription(w, indent+"  ", arg.Description)
			fmt.Fprint(w, indent+"  ")
		}
		fmt.Fprintf(w, "%s: %s", arg.Name, typeRefToString(arg.TypeRef))

		// Add default value if present
		if arg.DefaultValue != nil {
			fmt.Fprintf(w, " = %v", *arg.DefaultValue)
		}
	}
	if multiline {
		fmt.Fprint(w, "\n"+indent)
	}
	fmt.Fprint(w, ")")
}

func formatDirectiveApplications(w io.Writer, directives introspection.Directives) {
	if len(directives) == 0 {
		return
	}

	for _, directive := range directives {
		fmt.Fprintf(w, " @%s", directive.Name)

		// Add arguments if present
		if len(directive.Args) > 0 {
			fmt.Fprint(w, "(")
			args := make([]string, 0, len(directive.Args))
			for _, arg := range directive.Args {
				args = append(args, fmt.Sprintf("%s: %v", arg.Name, *arg.Value))
			}
			fmt.Fprint(w, strings.Join(args, ", "))
			fmt.Fprint(w, ")")
		}
	}
}

func formatDescribed[T any](w io.Writer, values []T, describe func(T) string, fn func(io.Writer, T)) {
	multiline := false
	descriptions := make([]string, len(values))
	for i, f := range values {
		description := describe(f)
		descriptions[i] = description
		if description != "" {
			multiline = true
		}
	}

	for i, value := range values {
		if description := descriptions[i]; description != "" {
			formatDescription(w, "  ", description)
		}
		fn(w, value)
		if multiline && i < len(values)-1 {
			fmt.Fprintln(w)
		}
	}
}

func typeRefToString(t *introspection.TypeRef) string {
	if t == nil {
		return "Unknown"
	}

	switch t.Kind {
	case introspection.TypeKindNonNull:
		if t.OfType != nil {
			return typeRefToString(t.OfType) + "!"
		}
		return t.Name + "!"
	case introspection.TypeKindList:
		if t.OfType != nil {
			return "[" + typeRefToString(t.OfType) + "]"
		}
		return "[" + t.Name + "]"
	default:
		return t.Name
	}
}

func descriptionLines(description string, maxLen int) []string {
	rawLines := strings.Split(description, "\n")
	var result []string

	for _, line := range rawLines {
		if len(line) < maxLen+5 {
			result = append(result, line)
		} else {
			// For > maxLen character long lines, cut at space boundaries
			// into sublines of ~80 chars
			subLines := breakLine(line, maxLen)
			result = append(result, subLines...)
		}
	}

	return result
}

func breakLine(line string, maxLen int) []string {
	minSize, maxSize := 15, maxLen-40
	if len(line) <= maxSize {
		return []string{line}
	}

	var chunks []string
	remaining := line

	for len(remaining) > 0 {
		// Find ideal break point - a space between min and max size
		end := min(len(remaining), maxSize)

		// If remaining text fits, add it and we're done
		if end == len(remaining) {
			chunks = append(chunks, remaining)
			break
		}

		// Look for space to break at, starting from end and working backwards
		if idx := strings.LastIndex(remaining[:end+1], " "); idx >= minSize {
			chunks = append(chunks, remaining[:idx])
			remaining = remaining[idx+1:] // Skip the space
		} else {
			// No suitable space found, force break at max size
			chunks = append(chunks, remaining[:end])
			remaining = remaining[end:]
		}
	}

	return chunks
}
