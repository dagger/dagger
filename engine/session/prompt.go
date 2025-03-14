package session

import (
	context "context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/adrg/xdg"
	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

var (
	configRoot          = filepath.Join(xdg.ConfigHome, "dagger")
	allowLLMModulesFile = filepath.Join(configRoot, "allowed-llm-modules.json")
)

// LLMAllowedModuleHistory manages the list of LLM modules that the user has allowed
type LLMAllowedModuleHistory struct {
	// only keyed like this for json marshalling forwards-compatibility
	Modules map[string]struct{} `json:"allowed_llm_modules"`
}

var promptMutex sync.Mutex

type PromptAttachable struct {
	rootCtx context.Context

	UnimplementedGitCredentialServer
	llmAllowedModuleHistory *LLMAllowedModuleHistory
}

func NewPromptAttachable(rootCtx context.Context) PromptAttachable {
	return PromptAttachable{
		rootCtx:                 rootCtx,
		llmAllowedModuleHistory: &LLMAllowedModuleHistory{},
	}
}

func (p PromptAttachable) Register(srv *grpc.Server) {
	RegisterPromptServer(srv, p)
}

// right now this is hardcoded to allow llm prompts, but could easily be extended to other prompt use cases
func (p PromptAttachable) Prompt(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	if req.Prompt == "" {
		return nil, status.Errorf(codes.InvalidArgument, "Invalid input: Prompt required")
	}

	if req.ModuleRepoURL == "" {
		return nil, status.Errorf(codes.InvalidArgument, "Invalid input: ModuleRepoURL required")
	}

	promptMutex.Lock()
	defer promptMutex.Unlock()

	allowed, err := p.llmAllowedModuleHistory.contains(req.ModuleRepoURL)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to check allowed LLM modules: %v", err)
	}

	if allowed {
		return &PromptResponse{
			Response: "yes",
		}, nil
	}

	// TODO: @vito: actually prompt the user via the frontend
	userResponse := "no"

	// only persist affirmatives so we reprompt for negatives
	if userResponse == "yes" {
		if err := p.llmAllowedModuleHistory.persistResponse(req.ModuleRepoURL); err != nil {
			return nil, status.Errorf(codes.Internal, "Failed to persist response: %v", err)
		}
	}

	return &PromptResponse{
		Response: userResponse,
	}, nil
}

// not threadsafe, must be holding promptMutex
func (a *LLMAllowedModuleHistory) load() error {
	if err := a.ensureFileExists(); err != nil {
		return err
	}

	data, err := os.ReadFile(allowLLMModulesFile)
	if err != nil {
		return err
	}

	if len(data) == 0 {
		a.Modules = make(map[string]struct{})
		return nil
	}

	if err := json.Unmarshal(data, a); err != nil {
		return err
	}

	if a.Modules == nil {
		a.Modules = make(map[string]struct{})
	}

	return nil
}

func (a *LLMAllowedModuleHistory) ensureFileExists() error {
	if err := os.MkdirAll(configRoot, 0755); err != nil {
		return err
	}

	_, err := os.Stat(allowLLMModulesFile)
	if os.IsNotExist(err) {
		return a.persist()
	}
	return err
}

func (a *LLMAllowedModuleHistory) contains(allowLLMModule string) (bool, error) {
	if err := a.load(); err != nil {
		return false, err
	}
	_, exists := a.Modules[allowLLMModule]
	return exists, nil
}

func (a *LLMAllowedModuleHistory) persistResponse(allowLLMModule string) error {
	if err := a.load(); err != nil {
		return err
	}

	a.Modules[allowLLMModule] = struct{}{}

	return a.persist()
}

func (a *LLMAllowedModuleHistory) persist() error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(allowLLMModulesFile, data, 0644)
}
