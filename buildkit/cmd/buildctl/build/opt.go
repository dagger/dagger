package build

import "maps"

func ParseOpt(opts []string) (map[string]string, error) {
	m := loadOptEnv()
	m2, err := attrMap(opts)
	if err != nil {
		return nil, err
	}
	maps.Copy(m, m2)
	return m, nil
}
