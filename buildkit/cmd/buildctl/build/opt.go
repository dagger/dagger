package build

func ParseOpt(opts []string) (map[string]string, error) {
	m := loadOptEnv()
	m2, err := attrMap(opts)
	if err != nil {
		return nil, err
	}
	for k, v := range m2 {
		m[k] = v
	}
	return m, nil
}
