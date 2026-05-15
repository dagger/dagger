package main

// Minimal is a thing
type Minimal struct {
	// X is this
	X, Y string // Y is not this

	// +private
	Z string
}

// some docs
func (m *Minimal) Hello(foo string, bar string,
	// hello
	baz string, qux string, x string, // lol
) string {
	return foo + bar
}

func (m *Minimal) HelloMore(
	// foo here
	foo,
	// bar here
	bar string,
) string {
	return foo + bar
}

func (m *Minimal) HelloMoreInline(opts struct {
	// foo here
	foo, bar string
}) string {
	return opts.foo + opts.bar
}

func (m *Minimal) HelloAgain( // docs for helloagain
	foo string,
	bar string, // docs for bar
	baz string,
) string {
	return foo + bar
}

func (m *Minimal) HelloFinal(
	foo string) string { // woops
	return foo
}
