package main

// Models smart enough to follow instructions like 'do X three times.'
var SmartModels = []string{
	"gpt-4o",
	"gpt-4.1",
	"gemini-2.5-flash",
	"claude-3-5-sonnet-latest",
	"claude-3-7-sonnet-latest",
	"claude-sonnet-4-0",
}

// Dagger's eval suite.
type Evals struct{}
