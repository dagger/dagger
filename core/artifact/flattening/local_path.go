package flattening

type PathStep struct {
	Field          string
	ModuleRoot     bool
	CollectionItem bool
}

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
