package greeting

import (
	"testing"

	"dagger.io/testgreet/internal/testutil"
)

func TestGreeting(t *testing.T) {
	name := "Dagger Test"
	expect := "Hi Dagger Test!"
	value := Greeting(name)

	if expect != value {
		t.Fatalf("Hello(%s) = '%s', expected '%s'", name, value, expect)
	}
	err := testutil.OKResultFile("/tmp/greeting_test.result")
	if err != nil {
		t.Fatalf("can not create test result file: %v", err)
	}
}
