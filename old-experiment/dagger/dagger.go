package dagger

type FS struct{}

func (fs *FS) ReadFile(path string) String {
	return NewString("")
}

func Scratch() *FS {
	return &FS{}
}

type String interface {
	Value() (string, error)
}

type staticString struct {
	v string
}

func (s staticString) Value() (string, error) {
	return s.v, nil
}

func NewString(v string) String {
	return staticString{
		v: v,
	}
}
