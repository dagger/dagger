package integration

func officialImages(names ...string) map[string]string {
	m := map[string]string{}
	for _, name := range names {
		// select available refs from the mirror map
		// so that we mirror only those needed for the tests
		if ref, ok := windowsImagesMirrorMap[name]; ok {
			m["library/"+name] = ref
		}
	}
	return m
}
