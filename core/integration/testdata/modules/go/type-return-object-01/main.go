package main

type Test struct{}

type X struct {
	Message string `json:"message"`
	When    string `json:"Timestamp"`
	To      string `json:"recipient"`
	From    string
}

func (m *Test) MyFunction() X {
	return X{Message: "foo", When: "now", To: "user", From: "admin"}
}
