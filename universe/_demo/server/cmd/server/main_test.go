package main

import "testing"

func TestGetMsg(t *testing.T) {
	got := getMsg("world")
	want := "Hello, world!"
	if got != want {
		t.Errorf("getMsg() = %q, want %q", got, want)
	}
}
