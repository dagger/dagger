package core

import (
	"context"
	"strings"
)

func collectionFilterValuesFromWorkspaceRoots(
	ctx context.Context,
	typeNames []string,
	include []string,
	exclude []string,
	roots []*ModTreeNode,
) ([]*CollectionFilterValues, error) {
	if len(roots) == 0 {
		return nil, nil
	}

	orderedTypeNames := append([]string(nil), typeNames...)
	seenTypes := make(map[string]string, len(orderedTypeNames))
	for _, typeName := range orderedTypeNames {
		seenTypes[gqlObjectName(typeName)] = typeName
	}

	valuesByType := make(map[string][]string, len(orderedTypeNames))
	seenValuesByType := make(map[string]map[string]struct{}, len(orderedTypeNames))
	for _, typeName := range orderedTypeNames {
		seenValuesByType[gqlObjectName(typeName)] = map[string]struct{}{}
	}

	for _, root := range roots {
		rootValues, err := root.CollectionFilterValues(
			ctx,
			typeNames,
			workspaceCompatibleCollectionPatterns(root.Name, include),
			workspaceCompatibleCollectionPatterns(root.Name, exclude),
		)
		if err != nil {
			return nil, err
		}
		for _, values := range rootValues {
			typeKey := gqlObjectName(values.TypeName)
			if _, ok := seenValuesByType[typeKey]; !ok {
				seenValuesByType[typeKey] = map[string]struct{}{}
				if _, ok := seenTypes[typeKey]; !ok {
					seenTypes[typeKey] = values.TypeName
					orderedTypeNames = append(orderedTypeNames, values.TypeName)
				}
			}
			for _, value := range values.Values {
				if _, ok := seenValuesByType[typeKey][value]; ok {
					continue
				}
				seenValuesByType[typeKey][value] = struct{}{}
				valuesByType[typeKey] = append(valuesByType[typeKey], value)
			}
		}
	}

	result := make([]*CollectionFilterValues, 0, len(orderedTypeNames))
	for _, typeName := range orderedTypeNames {
		typeKey := gqlObjectName(typeName)
		result = append(result, &CollectionFilterValues{
			TypeName: seenTypes[typeKey],
			Values:   valuesByType[typeKey],
		})
	}
	return result, nil
}

func workspaceCompatibleCollectionPatterns(rootName string, patterns []string) []string {
	if len(patterns) == 0 || rootName == "" {
		return patterns
	}

	expanded := make([]string, 0, len(patterns)*2)
	seen := make(map[string]struct{}, len(patterns)*2)
	for _, pattern := range patterns {
		if _, ok := seen[pattern]; !ok {
			seen[pattern] = struct{}{}
			expanded = append(expanded, pattern)
		}
		if strings.Contains(pattern, ":") {
			continue
		}
		prefixed := rootName + ":" + pattern
		if _, ok := seen[prefixed]; ok {
			continue
		}
		seen[prefixed] = struct{}{}
		expanded = append(expanded, prefixed)
	}
	return expanded
}

func workspaceCheckRoots(checks []*Check) []*ModTreeNode {
	return workspaceRoots(checks, func(check *Check) *ModTreeNode {
		return check.Node
	})
}

func workspaceGeneratorRoots(generators []*Generator) []*ModTreeNode {
	return workspaceRoots(generators, func(generator *Generator) *ModTreeNode {
		return generator.Node
	})
}

func workspaceRoots[T any](items []T, nodeFor func(T) *ModTreeNode) []*ModTreeNode {
	roots := make([]*ModTreeNode, 0, len(items))
	seen := make(map[*ModTreeNode]struct{}, len(items))
	for _, item := range items {
		root := workspaceRoot(nodeFor(item))
		if root == nil {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	return roots
}

func workspaceRoot(node *ModTreeNode) *ModTreeNode {
	if node == nil {
		return nil
	}
	root := node
	for root.Parent != nil && root.Parent.Module != nil {
		root = root.Parent
	}
	return root
}
