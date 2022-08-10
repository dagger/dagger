package router

type rootSchema struct {
}

func (r *rootSchema) Schema() string {
	return `
	type Query {
	}
	`
}

func (r *rootSchema) Operations() string {
	return ""
}

func (r *rootSchema) Resolvers() Resolvers {
	return Resolvers{}
}
