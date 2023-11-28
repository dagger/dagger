package greeting

import "testing"

func TestGreeting(t *testing.T) {
	name := "Dagger Test"
	expect := "Hi Dagger Test!"
	value := Greeting(name)

	if expect != value {
		t.Fatalf("Hello(%s) = '%s', expected '%s'", name, value, expect)
	}
}
