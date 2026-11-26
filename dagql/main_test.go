package dagql

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
)

// TestMain has every merge of the package's tests decided by both the
// narrowed cycle search and the whole one it replaced, and fails the run if
// any merge's two decisions differ. It also checks every blob-backed cache's
// kept measure of its pool against a walk as the cache closes
// (VerifyPoolMeasure), and every cache's kept uses of its classes against a
// walk (verifyEqClassUses), and every save into a database that held
// something against a full save at its hold (checkPersistFullSave), and every
// frame a merge installed against the recipe the merge landed it by
// (checkMergedRecipes), and fails the run if any differs. It has every share
// of a shared value, and every cache's close, check the values the caches
// share against their keys (verifySharedValues), and fails the run if a
// holder changed one.
func TestMain(m *testing.M) {
	var (
		saves         int
		saveDrifts    []string
		saveChecked   time.Duration
		mu            sync.Mutex
		judged        int
		disagreements []string
		pools         int
		poolDrifts    []string
		poolChecked   time.Duration
		classes       int
		classDrifts   []string
		classChecked  time.Duration
		noted         int
		unnoted       []string
		notedChecked  time.Duration
		recipes       int
		recipeDrifts  []string
		recipeChecked time.Duration
		sharedChecks  int
		sharedDrifts  []string
	)
	testPersistNotesAlways = true
	verifySharedValues = true
	testPersistNotes = func(_ *Cache, before, after *persistPrints) {
		at := time.Now()
		err := checkPersistNotes(before, after)
		took := time.Since(at)
		mu.Lock()
		defer mu.Unlock()
		noted++
		notedChecked += took
		if err != nil {
			unnoted = append(unnoted, err.Error())
		}
	}
	testCacheClosing = func(c *Cache) {
		sharedErr := errors.Join(lightStoredEnvelopes.verify(), sharedPlatforms.verify())
		mu.Lock()
		sharedChecks++
		if sharedErr != nil {
			sharedDrifts = append(sharedDrifts, sharedErr.Error())
		}
		mu.Unlock()
		at := time.Now()
		classErr := c.verifyEqClassUses()
		classTook := time.Since(at)
		mu.Lock()
		classes++
		classChecked += classTook
		if classErr != nil {
			classDrifts = append(classDrifts, classErr.Error())
		}
		mu.Unlock()
		if !c.blobBacked {
			return
		}
		at = time.Now()
		err := c.VerifyPoolMeasure()
		took := time.Since(at)
		mu.Lock()
		defer mu.Unlock()
		pools++
		poolChecked += took
		if err != nil {
			poolDrifts = append(poolDrifts, err.Error())
		}
	}
	JudgeMergeGraphs(func(_ context.Context, j MergeGraphJudgement) {
		mu.Lock()
		defer mu.Unlock()
		judged++
		if j.Disagrees() {
			disagreements = append(disagreements, fmt.Sprintf("merge from %s: narrowed %v, whole %v", j.From, j.Narrow, j.Whole))
		}
	})
	testPersistFullSave = func(ctx context.Context, c *Cache, full *persistStateSnapshot) {
		at := time.Now()
		err := checkPersistFullSave(context.WithoutCancel(ctx), c, full)
		took := time.Since(at)
		mu.Lock()
		defer mu.Unlock()
		saves++
		saveChecked += took
		if err != nil {
			saveDrifts = append(saveDrifts, err.Error())
		}
	}
	testMergedRecipes = func(c *Cache, landed []mergedRecipe) {
		at := time.Now()
		drifts := checkMergedRecipes(c, landed)
		took := time.Since(at)
		mu.Lock()
		defer mu.Unlock()
		recipes += len(landed)
		recipeChecked += took
		recipeDrifts = append(recipeDrifts, drifts...)
	}
	code := m.Run()
	mu.Lock()
	if len(recipeDrifts) > 0 {
		fmt.Fprintf(os.Stderr, "a merged frame's recipe differed from the one its merge landed it by in %d of %d installed records, checked in %s:\n", len(recipeDrifts), recipes, recipeChecked)
		for _, d := range recipeDrifts {
			fmt.Fprintln(os.Stderr, "  "+d)
		}
		code = 1
	} else {
		fmt.Fprintf(os.Stderr, "merged frames' recipes: %d installed records checked in %s, none differed\n", recipes, recipeChecked)
	}
	if len(saveDrifts) > 0 {
		fmt.Fprintf(os.Stderr, "a save left other rows than a full save in %d of %d saves, checked in %s:\n", len(saveDrifts), saves, saveChecked)
		for _, d := range saveDrifts {
			fmt.Fprintln(os.Stderr, "  "+d)
		}
		code = 1
	} else {
		fmt.Fprintf(os.Stderr, "saves against a full save: %d saves checked in %s, none differed\n", saves, saveChecked)
	}
	if len(unnoted) > 0 {
		fmt.Fprintf(os.Stderr, "a write changed what a save writes without noting it in %d of %d saves, checked in %s:\n", len(unnoted), noted, notedChecked)
		for _, d := range unnoted {
			fmt.Fprintln(os.Stderr, "  "+d)
		}
		code = 1
	} else {
		fmt.Fprintf(os.Stderr, "saves against the writers' notes: %d saves checked in %s, every change noted\n", noted, notedChecked)
	}
	if len(disagreements) > 0 {
		fmt.Fprintf(os.Stderr, "the narrowed and whole cycle searches disagreed on %d of %d merges:\n", len(disagreements), judged)
		for _, d := range disagreements {
			fmt.Fprintln(os.Stderr, "  "+d)
		}
		code = 1
	} else {
		fmt.Fprintf(os.Stderr, "merge graph judge: %d merges, no disagreement\n", judged)
	}
	if len(poolDrifts) > 0 {
		fmt.Fprintf(os.Stderr, "the kept pool measure differed from a walk in %d of %d blob-backed caches, checked in %s:\n", len(poolDrifts), pools, poolChecked)
		for _, d := range poolDrifts {
			fmt.Fprintln(os.Stderr, "  "+d)
		}
		code = 1
	} else {
		fmt.Fprintf(os.Stderr, "kept pool measure: %d blob-backed caches checked in %s, none differed\n", pools, poolChecked)
	}
	if len(classDrifts) > 0 {
		fmt.Fprintf(os.Stderr, "the kept uses of classes differed from a walk in %d of %d caches, checked in %s:\n", len(classDrifts), classes, classChecked)
		for _, d := range classDrifts {
			fmt.Fprintln(os.Stderr, "  "+d)
		}
		code = 1
	} else {
		fmt.Fprintf(os.Stderr, "kept class uses: %d caches checked in %s, none differed\n", classes, classChecked)
	}
	if len(sharedDrifts) > 0 {
		fmt.Fprintf(os.Stderr, "a shared value was changed by a holder, found in %d of %d checks:\n", len(sharedDrifts), sharedChecks)
		for _, d := range sharedDrifts {
			fmt.Fprintln(os.Stderr, "  "+d)
		}
		code = 1
	} else {
		fmt.Fprintf(os.Stderr, "shared values: %d checks, none changed\n", sharedChecks)
	}
	mu.Unlock()
	os.Exit(code)
}

// checkMergedRecipes checks each frame a merge installed, under the commit's
// hold of egraphMu (testMergedRecipes): that it keeps the recipe its merge
// landed its record by, and that deriving the frame again, from the entries
// its references name as the commit leaves them, gives that recipe. It
// derives without the frames' memos and without taking egraphMu, and calls
// none of the cache's test hooks, so what other tests observe of a merge
// stays theirs. An input held in a stored body gives the recipe the body
// keeps, as it does to a derivation. Requires egraphMu.
func checkMergedRecipes(c *Cache, landed []mergedRecipe) []string {
	// The walk's own memo, shared across the batch: a frame's inputs form a
	// graph, and an input reached along two paths is hashed once.
	derived := map[*ResultCall]digest.Digest{}
	visiting := map[*ResultCall]bool{}
	var derive func(frame *ResultCall) (digest.Digest, error)
	derive = func(frame *ResultCall) (digest.Digest, error) {
		if recipe, ok := derived[frame]; ok {
			return recipe, nil
		}
		if visiting[frame] {
			return "", fmt.Errorf("a cycle through field %q", frame.Field)
		}
		visiting[frame] = true
		defer delete(visiting, frame)
		recipe, err := frame.digestWithInputs(func(ref *ResultCallRef) (digest.Digest, error) {
			if err := ref.Validate(); err != nil {
				return "", err
			}
			if ref.Call != nil {
				return derive(ref.Call)
			}
			if frame := ref.loadSharedCall(); frame != nil {
				return derive(frame)
			}
			res := c.resultsByID[sharedResultID(ref.ResultID)]
			if res == nil {
				return "", fmt.Errorf("missing entry %d", ref.ResultID)
			}
			held := res.entryFrameLocked()
			switch {
			case held.call != nil:
				return derive(held.call)
			case held.body != nil:
				return held.body.recipe, nil
			}
			return "", fmt.Errorf("entry %d has no frame", ref.ResultID)
		})
		if err != nil {
			return "", err
		}
		derived[frame] = recipe
		return recipe, nil
	}
	var drifts []string
	for _, l := range landed {
		if l.frame == nil {
			continue
		}
		kept, derived, err := l.frame.derivedRecipeDigest()
		if !derived || err != nil || kept != l.recipe {
			drifts = append(drifts, fmt.Sprintf("field %q keeps recipe %q (derived %v, error %v), landed by %s", l.frame.Field, kept, derived, err, l.recipe))
			continue
		}
		fresh, err := derive(l.frame)
		if err != nil || fresh != l.recipe {
			drifts = append(drifts, fmt.Sprintf("field %q derives recipe %q (error %v), landed by %s", l.frame.Field, fresh, err, l.recipe))
		}
	}
	return drifts
}
