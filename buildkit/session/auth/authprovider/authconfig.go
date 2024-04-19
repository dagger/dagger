package authprovider

type AuthTLSConfig struct {
	RootCAs  []string
	Insecure bool
	KeyPairs []TLSKeyPair
}

type TLSKeyPair struct {
	Key         string
	Certificate string
}
