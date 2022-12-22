package core

type Query struct {
	Context QueryContext
}

type QueryContext struct {
	// Group
	Group Group `json:"group"`
}
