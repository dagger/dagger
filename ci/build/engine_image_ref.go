package build

type EngineImageRef struct {
	OS      string
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

	// Wolfi is the default image, skip adding the OS
	if e.OS != "wolfi" {
		ref += "-" + e.OS
	}

	if e.GPU {
		ref += "-gpu"
	}

	return ref
}
