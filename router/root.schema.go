package router

type rootSchema struct {
}

func (r *rootSchema) Name() string {
	return "root"
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

func (r *rootSchema) Dependencies() []ExecutableSchema {
	return nil
}
