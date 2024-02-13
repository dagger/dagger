package main

type Dagger struct {
	// XXX: do we need this extra layer of indirection? could we just add this
	// onto Util directory?
	Repo *UtilRepository // +private
}

func New(source *Directory) *Dagger {
	return &Dagger{
		Repo: dag.Util().Repository(source),
	}
}
