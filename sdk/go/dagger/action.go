package dagger

import (
	"context"
	"errors"
	"flag"
	"os"
)

func New() *Package {
	return &Package{
		actions: make(map[string]ActionFunc),
	}
}

type Package struct {
	actions map[string]ActionFunc
}

type ActionFunc func(ctx context.Context, input []byte) ([]byte, error)

func (p *Package) Action(name string, fn ActionFunc) {
	p.actions[name] = fn
}

func (p *Package) Serve() error {
	// TODO: switch to env var
	actionName := flag.String("a", "", "name of action to invoke")
	flag.Parse()
	if *actionName == "" {
		return errors.New("action name required")
	}
	fn, ok := p.actions[*actionName]
	if !ok {
		return errors.New("action not found: " + *actionName)
	}

	inputBytes, err := os.ReadFile("/inputs/dagger.json")
	if err != nil {
		return err
	}

	outputBytes, err := fn(WithUnixSocketAPIClient(context.Background(), "/dagger.sock"), inputBytes)
	if err != nil {
		return err
	}
	err = os.WriteFile("/outputs/dagger.json", outputBytes, 0644)
	if err != nil {
		return err
	}

	return nil
}
