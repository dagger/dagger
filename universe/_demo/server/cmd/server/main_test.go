package main

import "testing"

func TestGetMsg(t *testing.T) {
	got, err := getMsg("127.0.0.1:12345")
	if err != nil {
		t.Errorf("getMsg() error = %v", err)
		return
	}
	want := "Hello, 127.0.0.1 from port 12345!"
	if got != want {
		t.Errorf("getMsg() = %q, want %q", got, want)
	}
}

func TestGetMsgError(t *testing.T) {
	_, err := getMsg("not an ip address")
	if err == nil {
		t.Errorf("getMsg() error = %v, wantErr %v", err, true)
		return
	}
}
