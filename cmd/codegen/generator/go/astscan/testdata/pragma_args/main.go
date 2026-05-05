package main

type Echo struct{}

// Say echoes a message, optionally in a specific language.
func (e *Echo) Say(
	// the message to echo
	msg string,
	// +optional
	// +default="en"
	language string,
) string {
	return language + ": " + msg
}

// Greet returns a greeting with both pragma styles applied.
func (e *Echo) Greet(
	// +optional
	name string,
	// +default=42
	count int,
) string {
	return name
}
