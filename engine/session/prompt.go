package session

import (
	context "context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/adrg/xdg"
	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

var (
	configRoot              = filepath.Join(xdg.ConfigHome, "dagger")
	promptConfirmationsFile = filepath.Join(configRoot, "prompt-confirmations.json")
)

// PromptResponses manages the list of LLM modules that the user has allowed
type PromptResponses struct {
	// only keyed like this for json marshalling forwards-compatibility
	Responses map[string]struct{} `json:"responses"`
}

var promptMutex sync.Mutex

type PromptAttachable struct {
	UnimplementedPromptServer

	persistence   *PromptResponses
	promptHandler PromptHandler
}

type PromptHandler interface {
	HandlePrompt(ctx context.Context, prompt string, dest any) error
}

func NewPromptAttachable(promptHandler PromptHandler) PromptAttachable {
	return PromptAttachable{
		persistence:   &PromptResponses{},
		promptHandler: promptHandler,
	}
}

func (p PromptAttachable) Register(srv *grpc.Server) {
	RegisterPromptServer(srv, p)
}

func (p PromptAttachable) PromptBool(ctx context.Context, req *BoolRequest) (*BoolResponse, error) {
	if req.Prompt == "" {
		return nil, status.Errorf(codes.InvalidArgument, "Invalid input: Prompt required")
	}

	promptMutex.Lock()
	defer promptMutex.Unlock()

	if req.PersistentKey != "" {
		allowed, err := p.persistence.contains(req.PersistentKey)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Failed to check allowed LLM modules: %v", err)
		}
		if allowed {
			return &BoolResponse{
				Response: true,
			}, nil
		}
	}

	var confirm bool = req.GetDefault()
	if p.promptHandler != nil {
		if err := p.promptHandler.HandlePrompt(ctx, req.GetPrompt(), &confirm); err != nil {
			return nil, status.Errorf(codes.Internal, "Failed to handle prompt: %v", err)
		}
	}

	if confirm && req.PersistentKey != "" {
		if err := p.persistence.persistResponse(req.PersistentKey); err != nil {
			return nil, status.Errorf(codes.Internal, "Failed to persist response: %v", err)
		}
	}

	return &BoolResponse{
		Response: confirm,
	}, nil
}

// not threadsafe, must be holding promptMutex
func (a *PromptResponses) load() error {
	if err := a.ensureFileExists(); err != nil {
		return err
	}

	data, err := os.ReadFile(promptConfirmationsFile)
	if err != nil {
		return err
	}

	if len(data) == 0 {
		a.Responses = make(map[string]struct{})
		return nil
	}

	if err := json.Unmarshal(data, a); err != nil {
		return err
	}

	if a.Responses == nil {
		a.Responses = make(map[string]struct{})
	}

	return nil
}

func (a *PromptResponses) ensureFileExists() error {
	if err := os.MkdirAll(configRoot, 0755); err != nil {
		return err
	}

	_, err := os.Stat(promptConfirmationsFile)
	if os.IsNotExist(err) {
		return a.persist()
	}
	return err
}

func (a *PromptResponses) contains(key string) (bool, error) {
	if err := a.load(); err != nil {
		return false, err
	}
	_, exists := a.Responses[key]
	return exists, nil
}

func (a *PromptResponses) persistResponse(key string) error {
	if err := a.load(); err != nil {
		return err
	}

	a.Responses[key] = struct{}{}

	return a.persist()
}

func (a *PromptResponses) persist() error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(promptConfirmationsFile, data, 0644)
}
