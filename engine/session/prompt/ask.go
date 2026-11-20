package prompt

import (
	context "context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// promptAnswersFile remembers the answers to asks made with a persistent key.
// It is not prompt-confirmations.json: clients predating asks rewrite that
// file with every remembered key's value dropped (they only know keys).
var promptAnswersFile = filepath.Join(configRoot, "prompt-answers.json")

// AskHandler is implemented by frontends that can show an ask where the call
// that made it is rendered (see AskRequest). Frontends that only implement
// PromptHandler (e.g. plain and dots) get the question through HandlePrompt
// or HandleForm instead, on the terminal, one at a time.
//
// Like approvals, asks are not serialized by the attachable: a frontend may
// show several at once, each where its request came from.
type AskHandler interface {
	HandleAsk(ctx context.Context, req *AskRequest) (*AskResponse, error)
}

// PromptAsk asks the user a question. An answer remembered under the
// request's persistent key answers without asking, and an answer given to a
// request with one is remembered. A secret is never remembered, whatever the
// request says.
func (p PromptAttachable) PromptAsk(ctx context.Context, req *AskRequest) (*AskResponse, error) {
	if err := ValidateAsk(req); err != nil {
		return nil, err
	}
	key := req.GetPersistentKey()
	if req.GetSecret() != nil {
		key = ""
	}
	if key != "" {
		promptMutex.Lock()
		remembered, ok, err := loadAnswer(key)
		promptMutex.Unlock()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Failed to check remembered answers: %v", err)
		}
		if ok && ValidAskAnswer(req, remembered) == nil {
			return remembered, nil
		}
	}
	if p.promptHandler == nil {
		return nil, status.Error(codes.FailedPrecondition, "asking requires an interactive client")
	}
	var (
		resp *AskResponse
		err  error
	)
	if handler, ok := p.promptHandler.(AskHandler); ok {
		resp, err = handler.HandleAsk(ctx, req)
	} else {
		promptMutex.Lock()
		resp, err = p.askFallback(ctx, req)
		promptMutex.Unlock()
	}
	if err != nil {
		if _, ok := status.FromError(err); ok && status.Code(err) != codes.Unknown {
			return nil, err
		}
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "Failed to handle ask: %v", err)
	}
	if err := ValidAskAnswer(req, resp); err != nil {
		return nil, status.Errorf(codes.Internal, "invalid answer: %v", err)
	}
	if key != "" {
		promptMutex.Lock()
		err := storeAnswer(key, resp)
		promptMutex.Unlock()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Failed to remember answer: %v", err)
		}
	}
	return resp, nil
}

// askFallback asks through a frontend's plain prompts: yes/no and one-line
// text through HandlePrompt, the rest through a form. Must hold promptMutex.
func (p PromptAttachable) askFallback(ctx context.Context, req *AskRequest) (*AskResponse, error) {
	text := AskPromptText(req)
	if note := RememberedNote(req); note != "" {
		text += "\n" + note
	}
	switch input := req.GetInput().(type) {
	case *AskRequest_Boolean:
		confirm := input.Boolean.GetDefault()
		if err := p.promptHandler.HandlePrompt(ctx, req.GetTitle(), text, &confirm); err != nil {
			return nil, err
		}
		return &AskResponse{Answer: &AskResponse_Boolean{Boolean: confirm}}, nil
	case *AskRequest_Text:
		value := input.Text.GetDefault()
		if !input.Text.GetMultiline() {
			if err := p.promptHandler.HandlePrompt(ctx, req.GetTitle(), text, &value); err != nil {
				return nil, err
			}
			return &AskResponse{Answer: &AskResponse_Text{Text: value}}, nil
		}
		form := huh.NewForm(huh.NewGroup(huh.NewText().Title(req.GetTitle()).Description(text).Value(&value)))
		if err := p.runForm(ctx, form); err != nil {
			return nil, err
		}
		return &AskResponse{Answer: &AskResponse_Text{Text: value}}, nil
	case *AskRequest_Select:
		form, selected, err := SelectForm(&SelectRequest{
			Title:         req.GetTitle(),
			Prompt:        text,
			Choices:       input.Select.GetChoices(),
			DefaultChoice: input.Select.GetDefaultChoice(),
		})
		if err != nil {
			return nil, err
		}
		if err := p.runForm(ctx, form); err != nil {
			return nil, err
		}
		return &AskResponse{Answer: &AskResponse_Choice{Choice: *selected}}, nil
	case *AskRequest_Secret:
		// A form, never HandlePrompt: only a form masks what is typed. One
		// line even when several were asked for: a text area cannot mask.
		var value string
		form := huh.NewForm(huh.NewGroup(huh.NewInput().Title(req.GetTitle()).Description(text).
			EchoMode(huh.EchoModePassword).Value(&value)))
		if err := p.runForm(ctx, form); err != nil {
			return nil, err
		}
		return &AskResponse{Answer: &AskResponse_Secret{Secret: []byte(value)}}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "ask requires an input")
	}
}

func (p PromptAttachable) runForm(ctx context.Context, form *huh.Form) error {
	if err := p.promptHandler.HandleForm(ctx, form); err != nil {
		return err
	}
	if form.State == huh.StateAborted {
		return status.Error(codes.Canceled, "ask canceled")
	}
	return nil
}

// ValidateAsk checks a request is well formed: it asks something, for one
// kind of answer, and a selection offers choices with an explicit default.
func ValidateAsk(req *AskRequest) error {
	if req.GetTitle() == "" && req.GetMessage() == "" {
		return status.Error(codes.InvalidArgument, "Invalid input: a title or message is required")
	}
	if req.GetBoolean() == nil && req.GetText() == nil && req.GetSelect() == nil && req.GetSecret() == nil {
		return status.Error(codes.InvalidArgument, "Invalid input: an input is required")
	}
	if sel := req.GetSelect(); sel != nil {
		if _, _, err := SelectForm(&SelectRequest{
			Title:         req.GetTitle(),
			Prompt:        req.GetMessage(),
			Choices:       sel.GetChoices(),
			DefaultChoice: sel.GetDefaultChoice(),
		}); err != nil {
			return err
		}
	}
	return nil
}

// ValidAskAnswer checks that resp answers req: an answer of the kind asked
// for, and for a selection one of its choices.
func ValidAskAnswer(req *AskRequest, resp *AskResponse) error {
	var ok bool
	switch req.GetInput().(type) {
	case *AskRequest_Boolean:
		_, ok = resp.GetAnswer().(*AskResponse_Boolean)
	case *AskRequest_Text:
		_, ok = resp.GetAnswer().(*AskResponse_Text)
	case *AskRequest_Secret:
		_, ok = resp.GetAnswer().(*AskResponse_Secret)
	case *AskRequest_Select:
		var choice *AskResponse_Choice
		if choice, ok = resp.GetAnswer().(*AskResponse_Choice); ok {
			ok = false
			for _, c := range req.GetSelect().GetChoices() {
				if c.GetId() == choice.Choice {
					ok = true
					break
				}
			}
		}
	}
	if !ok {
		return errors.New("the answer does not match the question")
	}
	return nil
}

// AskPromptText is the question as one text for frontends that show only
// text: the message, and who asks. The requester's fields arrive escaped.
func AskPromptText(req *AskRequest) string {
	text := req.GetMessage()
	if text == "" {
		text = req.GetTitle()
	}
	if by := RequesterText(req.GetRequester()); by != "" {
		text += "\n\n(requested by " + by + ")"
	}
	return text
}

// RememberedNote tells the user their answer to req will be remembered
// beyond the session, and where, so they can forget it; "" when it will not
// be. Client-side only: it names this client's configuration.
func RememberedNote(req *AskRequest) string {
	if req.GetPersistentKey() == "" || req.GetSecret() != nil {
		return ""
	}
	return "Your answer will be remembered in " + promptAnswersFile + "."
}

// RequesterText names who asks, e.g. `agent "worker", module "keeper",
// function "Keeper.delegate"`, or "" when nobody is named.
func RequesterText(r *ApprovalRequester) string {
	var by []string
	agent := r.GetAgentName()
	if agent == "" {
		agent = r.GetAgentHandle()
	}
	if agent != "" {
		by = append(by, "agent "+strconv.Quote(agent))
	}
	if mod := r.GetModule(); mod != "" {
		by = append(by, "module "+strconv.Quote(mod))
	}
	if fn := r.GetFunction(); fn != "" {
		by = append(by, "function "+strconv.Quote(fn))
	}
	return strings.Join(by, ", ")
}

// rememberedAnswer is how an answer is remembered: typed, so an answer of
// one kind never answers a question of another.
type rememberedAnswer struct {
	Boolean *bool   `json:"boolean,omitempty"`
	Text    *string `json:"text,omitempty"`
	Choice  *string `json:"choice,omitempty"`
}

type rememberedAnswers struct {
	Answers map[string]rememberedAnswer `json:"answers"`
}

// not threadsafe, must be holding promptMutex
func loadAnswers() (*rememberedAnswers, error) {
	answers := &rememberedAnswers{}
	data, err := os.ReadFile(promptAnswersFile)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, answers); err != nil {
			return nil, err
		}
	}
	if answers.Answers == nil {
		answers.Answers = map[string]rememberedAnswer{}
	}
	return answers, nil
}

// not threadsafe, must be holding promptMutex
func loadAnswer(key string) (*AskResponse, bool, error) {
	answers, err := loadAnswers()
	if err != nil {
		return nil, false, err
	}
	a, ok := answers.Answers[key]
	if !ok {
		return nil, false, nil
	}
	switch {
	case a.Boolean != nil:
		return &AskResponse{Answer: &AskResponse_Boolean{Boolean: *a.Boolean}}, true, nil
	case a.Text != nil:
		return &AskResponse{Answer: &AskResponse_Text{Text: *a.Text}}, true, nil
	case a.Choice != nil:
		return &AskResponse{Answer: &AskResponse_Choice{Choice: *a.Choice}}, true, nil
	}
	return nil, false, nil
}

// not threadsafe, must be holding promptMutex
func storeAnswer(key string, resp *AskResponse) error {
	var a rememberedAnswer
	switch answer := resp.GetAnswer().(type) {
	case *AskResponse_Boolean:
		a.Boolean = &answer.Boolean
	case *AskResponse_Text:
		a.Text = &answer.Text
	case *AskResponse_Choice:
		a.Choice = &answer.Choice
	default:
		// Secrets, and anything unknown, are never remembered.
		return nil
	}
	answers, err := loadAnswers()
	if err != nil {
		return err
	}
	answers.Answers[key] = a
	data, err := json.MarshalIndent(answers, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(configRoot, 0755); err != nil {
		return err
	}
	// Answers may be personal: readable by the user alone.
	return os.WriteFile(promptAnswersFile, data, 0600)
}
