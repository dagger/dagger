package empty

import (
	"github.com/dagger/dagger/core/bbi"
	"github.com/dagger/dagger/dagql"
)

func init() {
	bbi.Register("empty", Driver{})
}

type Driver struct{}

func (Driver) NewSession(self dagql.Object, srv *dagql.Server) bbi.Session {
	return Session{
		self: self,
	}
}

type Session struct {
	self dagql.Object
}

func (Session) Tools() []bbi.Tool {
	return nil
}

func (s Session) Self() dagql.Object {
	return s.self
}
