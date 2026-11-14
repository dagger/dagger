package artifact

import (
	"slices"
	"strings"
)

type Link struct {
	Module          bool
	TypeKey         string
	ExactDimensions bool
}

type Links struct {
	Typed, Untyped  *Link
	CompleteSchema  bool     `json:",omitempty"`
	TypedAbsent     []string `json:",omitempty"`
	UntypedAbsent   []string `json:",omitempty"`
	TypedPresence   []string `json:",omitempty"`
	UntypedPresence []string `json:",omitempty"`
	TypedModule     bool     `json:",omitempty"`
	UntypedModule   bool     `json:",omitempty"`
	Stale           *Links   `json:",omitempty"`
}

func PathAbsentDimensions(paths Paths, target Path, typed bool) []string {
	var dimensions []string
	for _, candidate := range paths {
		if candidate.Module != target.Module || candidate.Key != target.Key || typed && candidate.Type != target.Type {
			continue
		}
		if slices.ContainsFunc(target.Dimensions, func(dimension string) bool {
			return canonicalCollectionDimension(dimension) && !slices.Contains(candidate.Dimensions, dimension)
		}) {
			continue
		}
		for _, dimension := range candidate.Dimensions {
			if canonicalCollectionDimension(dimension) && !slices.Contains(target.Dimensions, dimension) {
				dimensions = append(dimensions, dimension)
			}
		}
	}
	slices.Sort(dimensions)
	return slices.Compact(dimensions)
}

func PathPresenceDimensions(paths Paths, target Path, typed bool) []string {
	var dimensions []string
	for _, candidate := range paths {
		if candidate.Module != target.Module || candidate.Key != target.Key || typed && candidate.Type != target.Type {
			continue
		}
		for _, dimension := range target.Dimensions {
			if canonicalCollectionDimension(dimension) && !slices.Contains(candidate.Dimensions, dimension) {
				dimensions = append(dimensions, dimension)
			}
		}
	}
	slices.Sort(dimensions)
	return slices.Compact(dimensions)
}

func canonicalCollectionDimension(dimension string) bool {
	return !IsStaticDimension(dimension) && !strings.Contains(dimension, "/")
}

func PathNeedsModule(paths Paths, target Path, typed bool) bool {
	return slices.ContainsFunc(paths, func(candidate Path) bool {
		return candidate.Module != target.Module && candidate.Key == target.Key &&
			(!typed || candidate.Type == target.Type)
	})
}

func PlanLink(paths Paths, target Path, complete, typed bool, cliName func(string) string) *Link {
	if target.Type == "" || target.Key == "" || target.Module == "" {
		return nil
	}
	typeName := cliName(target.Type)
	module := cliName(target.Module)
	var candidates Paths
	for _, path := range paths {
		if typed && cliName(path.Type) != typeName || slices.ContainsFunc(target.Dimensions, func(id string) bool {
			return !slices.Contains(path.Dimensions, id)
		}) {
			continue
		}
		candidates = append(candidates, path)
	}
	for _, option := range []struct{ module, field bool }{
		{}, {field: true}, {module: true}, {module: true, field: true},
	} {
		if !complete && len(target.Dimensions) == 0 && !option.module {
			continue
		}
		var matches Paths
		var fieldPaths []string
		for _, path := range candidates {
			if option.module && cliName(path.Module) != module {
				continue
			}
			if path.Type == target.Type {
				fieldPaths = append(fieldPaths, path.Key)
			}
			if option.field && (path.Type != target.Type || path.Key != target.Key) {
				continue
			}
			matches = append(matches, path)
		}
		if len(matches) != 1 || matches[0].Type != target.Type || matches[0].Key != target.Key ||
			matches[0].Module != target.Module || !slices.Equal(matches[0].Dimensions, target.Dimensions) {
			continue
		}
		link := &Link{Module: option.module, ExactDimensions: !complete}
		if option.field {
			slices.Sort(fieldPaths)
			link.TypeKey = ShortTypeKey(slices.Compact(fieldPaths), target.Key)
			if link.TypeKey == "/"+target.Key {
				link.TypeKey = target.Key
			}
		}
		return link
	}
	return nil
}
