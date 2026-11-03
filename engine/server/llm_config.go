package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
)

// LLMConfigMetadata snapshots the two configuration owners together. Live
// overrides never mutate bootstrap metadata: requests continue authenticating
// against the original, sealed identity even after configuration is replaced.
func (srv *Server) LLMConfigMetadata(ctx context.Context) (main, parent *engine.ClientMetadata, revision string, err error) {
	client, err := srv.nonModuleParentClient(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	sess := client.daggerSession
	mainRecord, err := srv.clientRecordFromIDs(sess.sessionID, sess.mainClientCallerID)
	if err != nil {
		return nil, nil, "", err
	}
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()
	main, err = llmConfigMetadataLocked(mainRecord)
	if err != nil {
		return nil, nil, "", err
	}
	parent, err = llmConfigMetadataLocked(client.clientRecord)
	if err != nil {
		return nil, nil, "", err
	}
	revision = fmt.Sprintf("%s:%d/%s:%d", mainRecord.clientID, mainRecord.llmConfigRevision, client.clientID, client.llmConfigRevision)
	return main, parent, revision, nil
}

// The caller holds record.daggerSession.scopeMu.
func llmConfigMetadataLocked(record *clientRecord) (*engine.ClientMetadata, error) {
	md, err := cloneClientMetadata(record.clientMetadata)
	if err != nil {
		return nil, err
	}
	if record.llmConfigRevision != 0 {
		md.LLMConfig = record.llmConfig.Clone()
	}
	return md, nil
}

func (sess *daggerSession) replaceLLMConfig(record *clientRecord, cfg *engine.LLMConfig) {
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()
	record.llmConfig = cfg.Clone()
	record.llmConfigRevision++
}

// serveLLMConfig is an authenticated session control endpoint, not a GraphQL
// field. It can only replace the requesting client's configuration; it cannot
// choose another owner or return credentials inherited from another client.
func (srv *Server) serveLLMConfig(w http.ResponseWriter, r *http.Request, client *clientRuntime) error {
	switch r.Method {
	case http.MethodPut:
		var cfg *engine.LLMConfig
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		if err := dec.Decode(&cfg); err != nil {
			return httpErr(fmt.Errorf("decode LLM config: %w", err), http.StatusBadRequest)
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return httpErr(fmt.Errorf("expected one LLM config document"), http.StatusBadRequest)
		}
		if _, err := core.NewLLMRouter(cfg, client.clientMetadata); err != nil {
			return httpErr(err, http.StatusBadRequest)
		}
		client.daggerSession.replaceLLMConfig(client.clientRecord, cfg)
		w.WriteHeader(http.StatusNoContent)
		return nil
	case http.MethodGet:
		main, parent, _, err := srv.LLMConfigMetadata(r.Context())
		if err != nil {
			return err
		}
		router, err := core.NewLLMRouter(main.LLMConfig, main)
		if err != nil {
			return err
		}
		if main.ClientID != parent.ClientID {
			if err := router.Apply(parent.LLMConfig, parent); err != nil {
				return err
			}
		}
		models := &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{}}
		for _, md := range []*engine.ClientMetadata{main, parent} {
			if md.LLMConfig == nil {
				continue
			}
			if md.LLMConfig.DefaultProvider != "" {
				models.DefaultProvider = md.LLMConfig.DefaultProvider
			}
			if md.LLMConfig.DefaultModel != "" {
				models.DefaultModel = md.LLMConfig.DefaultModel
			}
		}
		for name, provider := range router.Providers {
			configured := false
			switch name {
			case core.OpenAI:
				configured = provider.HasCredential() || provider.BaseURL != ""
			case core.OpenAICodex, core.OpenRouter, core.Anthropic, core.Google:
				configured = provider.HasCredential()
			case core.Local:
				configured = provider.BaseURL != ""
			}
			if configured {
				models.Providers[string(name)] = &engine.LLMProviderConfig{
					Model: provider.Model, SmallModel: provider.SmallModel,
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(engine.LLMConfigStatus{
			Configured: len(models.Providers) > 0,
			Models:     models,
		})
	default:
		w.Header().Set("Allow", "GET, PUT")
		return httpErr(fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
	}
}
