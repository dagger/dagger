package main

type Echo struct{}

// Say returns a greeting, optionally in a specific language.
func (e *Echo) Say(msg string, language *string) string {
	if language != nil {
		return *language + ": " + msg
	}
	return msg
}
