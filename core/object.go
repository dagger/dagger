package core

type ModuleObject struct {
	*Identified

	Type  string
	Value any
}

func (obj ModuleObject) Clone() *ModuleObject {
	obj.Identified = obj.Identified.Clone()
	return &obj
}
