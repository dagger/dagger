package idtui

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/frioux/shellquote"
	"github.com/muesli/termenv"

	callpbv1 "github.com/dagger/dagger/dagql/call/callpbv1"
)

// DSL for Pretty-Printing GraphQL Field Calls
//
// This DSL allows you to define custom rendering for GraphQL field calls by creating
// structs with JSON tags that match the field arguments. The struct automatically
// unmarshals the call arguments and provides a simple Render method.
//
// To add a new field renderer:
//
// 1. Define a struct with JSON tags matching the GraphQL field arguments:
//    type WithCustomFieldArgs struct {
//        Path   string   `json:"path"`
//        Values []string `json:"values"`
//    }
//
// 2. Implement the FieldRenderer interface:
//    func (a WithCustomFieldArgs) Render(out TermOutput) (string, []string, bool) {
//        title := out.String("custom").Foreground(termenv.ANSIGreen).String() + " " + a.Path
//        return title, []string{"path", "values"}, true
//    }
//
// 3. Register it in the FieldRendererRegistry:
//    FieldRendererRegistry["withCustomField"] = func() FieldRenderer { return &WithCustomFieldArgs{} }
//
// The DSL automatically:
// - Converts protobuf call arguments to JSON
// - Unmarshals into your struct using JSON tags
// - Calls your Render method with the populated struct
// - Returns the elided arguments to hide from normal rendering

// FieldRenderer defines how to render a specific GraphQL field call
type FieldRenderer interface {
	// Render processes the call and returns title text and args to elide from normal rendering
	Render(out TermOutput) (title string, elidedArgs []string, specialTitle bool)
}

// WithExecArgs represents the arguments for withExec calls
type WithExecArgs struct {
	Args []string `json:"args"`
}

func (a WithExecArgs) Render(out TermOutput) (string, []string, bool) {
	if len(a.Args) == 0 {
		return "", nil, false
	}

	quoted, err := shellquote.Quote(a.Args)
	if err != nil {
		quoted = fmt.Sprintf("<quote error %q for %v>", err, a.Args)
	}

	// prevent multiline titles
	quoted = strings.ReplaceAll(quoted, "\n", `\n`)
	title := out.String("withExec").Foreground(termenv.ANSIBlue).String() + " " + quoted
	return title, []string{"args"}, true
}

// WithWorkdirArgs represents the arguments for withWorkdir calls
type WithWorkdirArgs struct {
	Path string `json:"path"`
}

func (a WithWorkdirArgs) Render(out TermOutput) (string, []string, bool) {
	if a.Path == "" {
		return "", nil, false
	}

	title := out.String("withWorkdir").Foreground(termenv.ANSIBlue).String() + " " + a.Path
	return title, []string{"path"}, true
}

// WithEnvVariableArgs represents the arguments for withEnvVariable calls
type WithEnvVariableArgs struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (a WithEnvVariableArgs) Render(out TermOutput) (string, []string, bool) {
	if a.Name == "" {
		return "", nil, false
	}

	title := out.String("withEnvVariable").Foreground(termenv.ANSIBlue).String() + " " + a.Name
	if a.Value != "" {
		title += "=" + a.Value
	}
	return title, []string{"name", "value"}, true
}

// WithMountedDirectoryArgs represents the arguments for withMountedDirectory calls
type WithMountedDirectoryArgs struct {
	Path   string `json:"path"`
	Source string `json:"source"`
}

func (a WithMountedDirectoryArgs) Render(out TermOutput) (string, []string, bool) {
	if a.Path == "" {
		return "", nil, false
	}

	title := out.String("withMountedDirectory").Foreground(termenv.ANSIBlue).String() + " " + a.Path
	title += " <- " + a.Source
	return title, []string{"path", "source"}, true
}

// WithMountedFileArgs represents the arguments for withMountedFile calls
type WithMountedFileArgs struct {
	Path   string `json:"path"`
	Source string `json:"source"`
}

func (a WithMountedFileArgs) Render(out TermOutput) (string, []string, bool) {
	if a.Path == "" {
		return "", nil, false
	}

	title := out.String("withMountedFile").Foreground(termenv.ANSIBlue).String() + " " + a.Path
	title += " <- " + a.Source
	return title, []string{"path", "source"}, true
}

// WithDirectoryArgs represents the arguments for withMountedDirectory calls
type WithDirectoryArgs struct {
	Path   string `json:"path"`
	Source string `json:"source"`
}

func (a WithDirectoryArgs) Render(out TermOutput) (string, []string, bool) {
	if a.Path == "" {
		return "", nil, false
	}

	title := out.String("withDirectory").Foreground(termenv.ANSIBlue).String() + " " + a.Path
	title += " <- " + a.Source
	return title, []string{"path", "source"}, true
}

// WithFileArgs represents the arguments for withFile calls
type WithFileArgs struct {
	Path   string `json:"path"`
	Source string `json:"source"`
}

func (a WithFileArgs) Render(out TermOutput) (string, []string, bool) {
	if a.Path == "" {
		return "", nil, false
	}

	title := out.String("withFile").Foreground(termenv.ANSIBlue).String() + " " + a.Path
	title += " <- " + a.Source
	return title, []string{"path", "source"}, true
}

type WithNewFileArgs struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

func (a WithNewFileArgs) Render(out TermOutput) (string, []string, bool) {
	if a.Path == "" {
		return "", nil, false
	}

	title := out.String("withNewFile").Foreground(termenv.ANSIBlue).String() + " " + a.Path
	return title, []string{"path"}, true
}

// WithUserArgs represents the arguments for withUser calls
type WithUserArgs struct {
	Name string `json:"name"`
}

func (a WithUserArgs) Render(out TermOutput) (string, []string, bool) {
	if a.Name == "" {
		return "", nil, false
	}

	title := out.String("user").Foreground(termenv.ANSIBlue).String() + " " + a.Name
	return title, []string{"name"}, true
}

// ContextDirectoryArgs represents the arguments for withUser calls
type ContextDirectoryArgs struct {
	Path    string   `json:"path"`
	Exclude []string `json:"exclude"`
	Module  string   `json:"module"`
	Digest  string   `json:"digest"`
}

func (a ContextDirectoryArgs) Render(out TermOutput) (string, []string, bool) {
	title := out.String("context").Foreground(termenv.ANSIBlue).String()
	title += " " + path.Join(a.Module, a.Path) // NB: i think this is right for git AND local paths?
	return title, []string{"path", "module", "digest"}, true
}

// FieldRendererRegistry holds all field renderers.
// Each entry maps a GraphQL field name to a factory function that creates a new renderer instance.
var FieldRendererRegistry = map[string]func() FieldRenderer{
	"withExec":             func() FieldRenderer { return &WithExecArgs{} },
	"withWorkdir":          func() FieldRenderer { return &WithWorkdirArgs{} },
	"withEnvVariable":      func() FieldRenderer { return &WithEnvVariableArgs{} },
	"withMountedDirectory": func() FieldRenderer { return &WithMountedDirectoryArgs{} },
	"withMountedFile":      func() FieldRenderer { return &WithMountedFileArgs{} },
	"withDirectory":        func() FieldRenderer { return &WithDirectoryArgs{} },
	"withFile":             func() FieldRenderer { return &WithFileArgs{} },
	"withNewFile":          func() FieldRenderer { return &WithNewFileArgs{} },
	"withUser":             func() FieldRenderer { return &WithUserArgs{} },
	"_contextDirectory":    func() FieldRenderer { return &ContextDirectoryArgs{} },
}

// RegisterFieldRenderer adds a new field renderer to the registry
func RegisterFieldRenderer(fieldName string, rendererFactory func() FieldRenderer) {
	FieldRendererRegistry[fieldName] = rendererFactory
}

// callArgsToJSON converts call arguments to JSON for unmarshaling
func (r *renderer) callArgsToJSON(call *callpbv1.Call, out TermOutput, prefix string, depth int) ([]byte, error) {
	argsMap := make(map[string]any)

	for _, arg := range call.Args {
		value := arg.Value
		switch {
		case value.GetString_() != "":
			argsMap[arg.Name] = value.GetString_()
		case value.GetInt() != 0:
			argsMap[arg.Name] = value.GetInt()
		case value.GetBool():
			argsMap[arg.Name] = value.GetBool()
		case value.GetList() != nil:
			var list []string
			for _, v := range value.GetList().Values {
				list = append(list, v.GetString_())
			}
			argsMap[arg.Name] = list
		case value.GetCallDigest() != "":
			argDig := value.GetCallDigest()
			argSpan := r.db.MostInterestingSpan(argDig)
			argCall := r.db.Simplify(r.db.MustCall(argDig), true)
			buf := new(strings.Builder)
			argOut := termenv.NewOutput(buf, termenv.WithProfile(out.ColorProfile()))
			if err := r.renderCall(argOut, argSpan, argCall, prefix, false, depth, false, nil); err != nil {
				return nil, err
			}
			argsMap[arg.Name] = argOut.String()
		default:
			// Handle other types as needed
			argsMap[arg.Name] = value.String()
		}
	}

	return json.Marshal(argsMap)
}

// renderFieldCall renders a field call using the registered renderer
func (r *renderer) renderFieldCall(call *callpbv1.Call, out TermOutput, prefix string, depth int) (title string, elidedArgs map[string]struct{}, specialTitle bool) {
	rendererFactory, exists := FieldRendererRegistry[call.Field]
	if !exists {
		return "", nil, false
	}

	renderer := rendererFactory()

	// Convert call args to JSON and unmarshal into the renderer struct
	jsonData, err := r.callArgsToJSON(call, out, prefix, depth)
	if err != nil {
		return "", nil, false
	}

	if err := json.Unmarshal(jsonData, renderer); err != nil {
		return "", nil, false
	}

	title, elidedArgsList, specialTitle := renderer.Render(out)

	// Convert slice to map for compatibility with existing code
	elidedArgsMap := make(map[string]struct{})
	for _, arg := range elidedArgsList {
		elidedArgsMap[arg] = struct{}{}
	}

	return title, elidedArgsMap, specialTitle
}
