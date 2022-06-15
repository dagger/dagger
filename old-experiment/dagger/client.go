package dagger

type Client struct {
}

func (c *Client) Filesystem() *Filesystem {
	return &Filesystem{}
}

type Filesystem struct {
}

func (fs *Filesystem) Directory(string) *FS {
	return Scratch()
}
