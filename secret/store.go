package secret

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dagger/cloak/router"
	"github.com/moby/buildkit/session/secrets"
)

func NewStore(r *router.Router) *Store {
	return &Store{
		r:       r,
		secrets: make(map[string][]byte),
	}
}

var _ secrets.SecretStore = &Store{}

type Store struct {
	r       *router.Router
	secrets map[string][]byte
}

func (p *Store) AddSecret(ctx context.Context, value []byte) string {
	hash := sha256.Sum256(value)
	id := hex.EncodeToString(hash[:])
	p.secrets[id] = value
	return id
}

func (p *Store) GetSecret(ctx context.Context, id string) ([]byte, error) {
	if secret, ok := p.secrets[id]; ok {
		return secret, nil
	}

	idParts := strings.SplitN(id, "://", 2)
	protocol, id := idParts[0], idParts[1]

	query := fmt.Sprintf(`
	query {
		core {
			secrets {
				plaintext: %s(name: %q)
			}
		}
	}
	`, protocol, id)
	fmt.Fprintf(os.Stderr, "QUERY: %s\n", query)
	result := p.r.Do(ctx, query, map[string]any{})
	if result.HasErrors() {
		fmt.Fprintf(os.Stderr, "%+v\n", result.Errors)
		return nil, secrets.ErrNotFound
	}

	resp := struct {
		Core struct {
			Secrets struct {
				Plaintext string
			}
		}
	}{}

	marshalled, err := json.Marshal(result.Data)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(marshalled, &resp); err != nil {
		return nil, err
	}

	return []byte(resp.Core.Secrets.Plaintext), nil
}
