package main

import "fmt"

type Test struct{}

func (m *Test) Foo(
	a string,
	// +optional
	b *string,
	// +default="foo"
	c string,
	// +default=null
	d *string,
	// +default="bar"
	e *string,
) string {
	return fmt.Sprintf("%+v, %+v, %+v, %+v, %+v", a, b, c, d, *e)
}
