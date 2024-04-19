package build

import (
	"encoding/csv"
	"strconv"
	"strings"

	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/pkg/errors"
)

type authTLSContextEntry struct {
	Host     string
	CA       string
	Cert     string
	Key      string
	Insecure bool
}

func parseRegistryAuthTLSContextCSV(s string) (authTLSContextEntry, error) {
	authTLSContext := authTLSContextEntry{}
	csvReader := csv.NewReader(strings.NewReader(s))
	fields, err := csvReader.Read()
	if err != nil {
		return authTLSContext, err
	}
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return authTLSContext, errors.Errorf("invalid value %s", field)
		}
		key = strings.ToLower(key)
		switch key {
		case "host":
			authTLSContext.Host = value
		case "ca":
			authTLSContext.CA = value
		case "cert":
			authTLSContext.Cert = value
		case "key":
			authTLSContext.Key = value
		case "insecure":
			authTLSContext.Insecure, _ = strconv.ParseBool(value)
		}
	}
	if authTLSContext.Host == "" {
		return authTLSContext, errors.New("--registry-auth-tlscontext requires host=<host>")
	}
	if authTLSContext.CA == "" {
		if !authTLSContext.Insecure {
			if authTLSContext.Cert == "" || authTLSContext.Key == "" {
				return authTLSContext, errors.New("--registry-auth-tlscontext requires ca=<ca> or cert=<cert>,key=<key> or insecure=true")
			}
		}
	} else {
		if (authTLSContext.Cert != "" && authTLSContext.Key == "") || (authTLSContext.Cert == "" && authTLSContext.Key != "") {
			return authTLSContext, errors.New("--registry-auth-tlscontext requires cert=<cert>,key=<key>")
		}
	}
	return authTLSContext, nil
}

func ParseRegistryAuthTLSContext(registryAuthTLSContext []string) (map[string]*authprovider.AuthTLSConfig, error) {
	var tlsContexts []authTLSContextEntry
	for _, c := range registryAuthTLSContext {
		authTLSContext, err := parseRegistryAuthTLSContextCSV(c)
		if err != nil {
			return nil, err
		}
		tlsContexts = append(tlsContexts, authTLSContext)
	}

	authConfigs := make(map[string]*authprovider.AuthTLSConfig)
	for _, c := range tlsContexts {
		_, ok := authConfigs[c.Host]
		if !ok {
			authConfigs[c.Host] = &authprovider.AuthTLSConfig{}
		}
		if c.Insecure {
			authConfigs[c.Host].Insecure = true
		}
		if c.CA != "" {
			authConfigs[c.Host].RootCAs = append(authConfigs[c.Host].RootCAs, c.CA)
		}
		if c.Cert != "" && c.Key != "" {
			authConfigs[c.Host].KeyPairs = append(authConfigs[c.Host].KeyPairs, authprovider.TLSKeyPair{
				Key:         c.Key,
				Certificate: c.Cert,
			})
		}
	}
	return authConfigs, nil
}
