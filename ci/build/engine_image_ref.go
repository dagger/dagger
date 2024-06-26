package build

type EngineImageRef struct {
	Wolfi   bool
	GPU     bool
	Image   string
	Version string
}

func (e *EngineImageRef) String(explicitVersion string) string {
	ref := e.Image + ":"

	if explicitVersion != "" {
		ref += explicitVersion
	} else {
		ref += e.Version
	}

	if e.Wolfi {
		ref += "-wolfi"
	}

	if e.GPU {
		ref += "-gpu"
	}

	return ref
}
