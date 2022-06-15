package dagger

type Context struct {
}

type PipelineFunc func(ctx *Context)

func Pipeline(PipelineFunc) {

}

type ActionFunc func()

func (ctx *Context) Action(string, ActionFunc) {

}

func (ctx *Context) Export(string, String) {}

func (ctx *Context) Client() *Client {
	return &Client{}
}
