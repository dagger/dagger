package main

type Foobar struct{}

func (m *Foobar) Exclaim(
	message string,
	// +default=1
	count int,
) string {
	result := message
	for i := 0; i < count; i++ {
		result += "!"
	}
	return result
}
