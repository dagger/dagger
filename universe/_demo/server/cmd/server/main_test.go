package main

import "testing"

func TestGetMsg(t *testing.T) {
	got := getMsg("world")
	want := "Hello, world!"
	if got != want {
		t.Errorf("getMsg() = %q, want %q", got, want)
	}
}

func TestGetMsgAgain(t *testing.T) {
	got := getMsg("goodbye")
	want := "Hello, goodbye!"
	if got != want {
		t.Errorf("getMsg() = %q, want %q", got, want)
	}
}

// func TestGetMsgFails(t *testing.T) {
// 	got := getMsg("goodbye")
// 	want := "Hello, uh oh!"
// 	if got != want {
// 		t.Errorf("getMsg() = %q, want %q", got, want)
// 	}
// }
