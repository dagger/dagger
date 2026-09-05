package dagql

import (
	"sync"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
)

// RecipeClassification describes the structural replay properties of a recipe.
// A nil NotReplayable means every field reachable through the inputs that recipe
// loading evaluates is replayable.
type RecipeClassification struct {
	NotReplayable *NotReplayableCall
}

// NotReplayableCall identifies a field that makes a recipe non-replayable.
type NotReplayableCall struct {
	Field  string
	Digest digest.Digest
	Reason string
}

// ClassifyRecipe structurally classifies id without evaluating it. The first
// NotReplayable field is reported in recipe-loading traversal order. Lazy-ref
// arguments are carried by reference during replay, so recipes reachable only
// through those arguments do not affect the classification.
//
// Classification is best-effort. Handles, malformed recipes, and fields or
// parent types that are not present in the current schema are not themselves
// classified as non-replayable. Unknown fields' arguments are treated as
// non-lazy, matching recipe loading behavior.
func (s *Server) ClassifyRecipe(id *call.ID) RecipeClassification {
	if c := s.canonical; c != nil {
		return c.ClassifyRecipe(id)
	}
	classifier := newRecipeClassifier(s, func(_ *call.ID, fieldSpec FieldSpec) string {
		return fieldSpec.NotReplayable
	})
	return RecipeClassification{NotReplayable: classifier.classify(id)}
}

type recipeClassifier struct {
	srv   *Server
	match func(*call.ID, FieldSpec) string

	mu   sync.Mutex
	memo map[string]*NotReplayableCall
}

func newRecipeClassifier(srv *Server, match func(*call.ID, FieldSpec) string) *recipeClassifier {
	return &recipeClassifier{
		srv:   srv,
		match: match,
		memo:  make(map[string]*NotReplayableCall),
	}
}

func (classifier *recipeClassifier) classify(id *call.ID) *NotReplayableCall {
	if id == nil || id.IsHandle() {
		return nil
	}
	key := id.Digest().String()
	classifier.mu.Lock()
	if cached, ok := classifier.memo[key]; ok {
		classifier.mu.Unlock()
		return cached
	}
	// A provisional replayable entry guards against cycles in malformed recipes.
	classifier.memo[key] = nil
	classifier.mu.Unlock()

	var first *NotReplayableCall
	if fieldSpec, ok := classifier.srv.recipeFieldSpec(id); ok {
		if reason := classifier.match(id, fieldSpec); reason != "" {
			first = &NotReplayableCall{
				Field:  id.Field(),
				Digest: id.Digest(),
				Reason: reason,
			}
		}
	}

	lazyRefs := (&recipeLoadState{srv: classifier.srv}).lazyRefArgNames(id)
	for _, input := range (&recipeLoadState{srv: classifier.srv}).directRecipeInputIDs(id, lazyRefs) {
		inputMatch := classifier.classify(input)
		if first == nil && inputMatch != nil {
			first = inputMatch
		}
	}

	classifier.mu.Lock()
	classifier.memo[key] = first
	classifier.mu.Unlock()
	return first
}

func (s *Server) recipeFieldSpec(id *call.ID) (FieldSpec, bool) {
	if id == nil || id.IsHandle() {
		return FieldSpec{}, false
	}
	parentType := "Query"
	if receiver := id.Receiver(); receiver != nil {
		if t := receiver.Type(); t != nil {
			parentType = t.NamedType()
		} else {
			return FieldSpec{}, false
		}
	}
	if parentType == "" {
		return FieldSpec{}, false
	}
	objType, ok := s.ObjectType(parentType)
	if !ok {
		return FieldSpec{}, false
	}
	return objType.FieldSpec(id.Field(), id.View())
}
