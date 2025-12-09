package main

type Sub2 struct{}

func (m *Sub2) Fn(s string) *Sub2Obj {
	return &Sub2Obj{Bar: "2:" + s}
}

type Sub2Obj struct {
	Bar string `json:"bar"`
}

func (m *Sub2Obj) GetBar() (string, error) {
	return m.Bar, nil
}
