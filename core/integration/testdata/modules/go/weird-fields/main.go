package main

type Z string

type Minimal struct {
	// field with single (normal) name
	W string

	// field with multiple names
	X, Y string

	// field with no names
	Z
}

func New() Minimal {
	return Minimal{
		W: "-",
		X: "-",
		Y: "-",
		Z: Z("-"),
	}
}

// struct with no fields
type Bar struct{}

func (m *Minimal) Say(
	// field with single (normal) name
	a string,
	// field with multiple names
	b, c string,
	// field with no names (not included, mixed names not allowed)
	// string
) string {
	return a + " " + b + " " + c
}

func (m *Minimal) Hello(
	// field with no names
	string,
) string {
	return "hello"
}

func (m *Minimal) SayOpts(opts struct {
	// field with single (normal) name
	A string
	// field with multiple names
	B, C string
	// field with no names (not included because of above)
	// string
}) string {
	return opts.A + " " + opts.B + " " + opts.C
}

func (m *Minimal) HelloOpts(opts struct {
	// field with no names
	string
}) string {
	return "hello"
}
