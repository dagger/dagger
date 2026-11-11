package flattening

// PathStep describes one position on a route from the module root to an
// artifact. Field retains the literal schema spelling. A module root or a
// collection item starts a new local root; its Field is not part of the path.
type PathStep struct {
	Field          string
	ModuleRoot     bool
	CollectionItem bool
}

// LocalFieldPath retains every field after the last local root on route.
// A collection field does not start a local root until the route enters an
// item. Thus a function on a collection stays relative to its enclosing item,
// or to the module root when there is no enclosing item.
func LocalFieldPath(route []PathStep) []string {
	var fields []string
	for _, step := range route {
		if step.ModuleRoot || step.CollectionItem {
			fields = nil
		} else {
			fields = append(fields, step.Field)
		}
	}
	return fields
}
