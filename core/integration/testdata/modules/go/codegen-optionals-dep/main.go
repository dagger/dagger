package main

import "fmt"

type Dep struct{}

func (m *Dep) Ctl(
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
