package core

type ModuleObject struct {
	Identified

	Type  string
	Value any
}

func (obj *ModuleObject) Clone() *ModuleObject {
	cp := *obj
	cp.Identified.Reset()
	return &cp
}
