package idtui

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
)

// Heads: the commit an agent's workspace last moved to, and the traced call
// that produced it as a GitRef. When that call's recipe rebuilds from the
// trace alone -- every frame arrived, and nothing in it reads the client's
// host or workspace, a secret, or a model -- any engine can evaluate it and
// reproduce the exact commit and its history. Recovering an agent's committed
// work is then a checkout of that ID, not a replay of its steps.

// consoleWorkspaceMoved matches the trailer a workspace-changing tool result
// ends with, e.g.
//
//	Workspace moved: github.com/vito/agents @0003909… -> @6333e49…
//	Workspace replaced: github.com/a/b @f3aa… [git-ref://…] -> github.com/c/d @49e7… (main) [git-ref://…]
//
// Group 1 is the repository before the arrow, group 2 the one after it (only
// when it changed), group 3 the commit the workspace moved to.
var consoleWorkspaceMoved = regexp.MustCompile(`Workspace (?:moved|replaced): (\S+) @[0-9a-f]+[^\n]*? -> (?:(\S+) )?@([0-9a-f]{40})\b`)

// consoleHeadCandidates bounds how many traced GitRef calls for one commit are
// gathered and checked: each check walks the call's whole recipe.
const consoleHeadCandidates = 8

type consoleHead struct {
	Agent string `json:"agent"`
	Name  string `json:"name"`
	// Commit is empty when no tool result recorded the workspace moving.
	Commit string `json:"commit,omitempty"`
	Repo   string `json:"repo,omitempty"`
	// Step and Tool name the salvage step whose result reported Commit.
	Step int    `json:"step,omitempty"`
	Tool string `json:"tool,omitempty"`
	// Later counts the workspace changes after Step, which the head lacks:
	// edits still uncommitted when the trace ended.
	Later int `json:"later,omitempty"`
	// Digest is the traced call producing Commit as a GitRef, and Field its
	// Type.field; empty when no such call rebuilds from this trace.
	Digest string `json:"digest,omitempty"`
	Field  string `json:"field,omitempty"`
	// Calls is the number of distinct calls in Digest's recipe.
	Calls int `json:"calls,omitempty"`
	// Recoverable reports that Digest's recipe can be evaluated by any engine:
	// it rebuilds and has no Problems.
	Recoverable bool     `json:"recoverable"`
	Problems    []string `json:"problems,omitempty"`
	// Fetches lists the remote repositories the recipe clones.
	Fetches []string `json:"fetches,omitempty"`
	// Notes are things evaluating the recipe does that don't stop it
	// reproducing the commit, e.g. running module functions.
	Notes []string `json:"notes,omitempty"`

	// recorded is the immutable recorded-trace DB Digest was found in; nil
	// means the frontend's loaded telemetry, read under consoleMu.
	recorded *dagui.DB
}

// consoleHeadFromSteps finds the last commit a successful step's result
// reported the workspace moving to.
func consoleHeadFromSteps(steps []consoleSalvageStep) consoleHead {
	var head consoleHead
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		if step.Errored || step.Result == nil {
			continue
		}
		matches := consoleWorkspaceMoved.FindAllStringSubmatch(*step.Result, -1)
		if len(matches) == 0 {
			continue
		}
		m := matches[len(matches)-1]
		head.Commit, head.Repo = m[3], m[1]
		if m[2] != "" {
			head.Repo = m[2]
		}
		head.Step, head.Tool = step.Seq, step.Tool
		for _, later := range steps[i+1:] {
			if !later.Errored && later.Result != nil {
				head.Later++
			}
		}
		break
	}
	return head
}

// locateConsoleHead finds the traced GitRef call for head.Commit in db and
// checks whether its recipe is recoverable. It prefers a recoverable recipe,
// then any that rebuilds; with none, the first rebuild error is a Problem.
func locateConsoleHead(db *dagui.DB, head *consoleHead) {
	type candidate struct {
		digest string
		rank   int
	}
	var candidates []candidate
	for dig := range db.Calls {
		frame := db.Call(dig)
		if frame == nil || frame.GetDigest() != dig || frame.GetType().GetNamedType() != "GitRef" {
			continue
		}
		if !slices.ContainsFunc(frame.GetArgs(), func(arg *callpbv1.Argument) bool {
			return arg.GetValue().GetString_() == head.Commit
		}) {
			continue
		}
		rank := 2
		switch frame.GetField() {
		case "ref":
			rank = 0
		case "__resolvedRef":
			rank = 1
		}
		candidates = append(candidates, candidate{dig, rank})
	}
	sort.Slice(candidates, func(a, b int) bool {
		if candidates[a].rank != candidates[b].rank {
			return candidates[a].rank < candidates[b].rank
		}
		return candidates[a].digest < candidates[b].digest
	})
	if len(candidates) > consoleHeadCandidates {
		candidates = candidates[:consoleHeadCandidates]
	}
	var firstErr error
	var fallback *consoleHead
	for _, c := range candidates {
		recipe, err := db.CallRecipe(c.digest)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		found := *head
		found.Digest = recipe.GetRootDigest()
		found.Field = consoleFrameTypeField(recipe, recipe.GetRootDigest())
		found.Calls = len(recipe.GetCallsByDigest())
		found.Problems, found.Fetches, found.Notes = consoleRecipeHazards(recipe)
		found.Recoverable = len(found.Problems) == 0
		if found.Recoverable {
			*head = found
			return
		}
		if fallback == nil {
			fallback = &found
		}
	}
	switch {
	case fallback != nil:
		*head = *fallback
	case firstErr != nil:
		head.Problems = []string{firstErr.Error()}
	case len(candidates) == 0:
		head.Problems = []string{"no GitRef call for this commit reached this client"}
	}
}

// consoleFrameTypeField renders a recipe frame as Type.field, its receiver's
// type standing in for the type the field is on.
func consoleFrameTypeField(recipe *callpbv1.RecipeDAG, digest string) string {
	frame := recipe.GetCallsByDigest()[digest]
	if frame == nil {
		return ""
	}
	parent := "Query"
	if recv := recipe.GetCallsByDigest()[frame.GetReceiverDigest()]; recv != nil {
		parent = recv.GetType().GetNamedType()
	}
	return parent + "." + frame.GetField()
}

// consoleRecipeHazards reports what keeps a recipe from reproducing its result
// on another engine (problems), the repositories it clones (fetches), and what
// else evaluating it does (notes).
func consoleRecipeHazards(recipe *callpbv1.RecipeDAG) (problems, fetches, notes []string) {
	counts := map[string]int{}
	var order []string
	problem := func(what string) {
		if counts[what] == 0 {
			order = append(order, what)
		}
		counts[what]++
	}
	modules := map[string]bool{}
	execs := 0
	for _, frame := range recipe.GetCallsByDigest() {
		if frame.GetReceiverDigest() == "" {
			switch frame.GetField() {
			case "host":
				problem("reads the client's host (Query.host)")
			case "currentWorkspace":
				problem("reads the client's workspace (Query.currentWorkspace)")
			case "secret", "setSecret":
				problem("uses a secret (Query." + frame.GetField() + "), which no recipe carries")
			}
		}
		if frame.GetType().GetNamedType() == "LLM" {
			problem("replays model calls (LLM), which won't produce the same result")
		}
		switch frame.GetField() {
		case "git", "__gitRepository":
			for _, arg := range frame.GetArgs() {
				if arg.GetName() == "url" {
					if url := arg.GetValue().GetString_(); url != "" && !slices.Contains(fetches, url) {
						fetches = append(fetches, url)
					}
				}
			}
		case "withExec":
			execs++
		}
		if name := frame.GetModule().GetName(); name != "" {
			modules[name] = true
		}
	}
	for _, what := range order {
		if n := counts[what]; n > 1 {
			what = fmt.Sprintf("%s, %d calls", what, n)
		}
		problems = append(problems, what)
	}
	sort.Strings(fetches)
	if len(modules) > 0 {
		names := make([]string, 0, len(modules))
		for name := range modules {
			names = append(names, name)
		}
		sort.Strings(names)
		notes = append(notes, "runs functions from modules: "+strings.Join(names, ", "))
	}
	if execs > 0 {
		notes = append(notes, fmt.Sprintf("runs %d container execs", execs))
	}
	return problems, fetches, notes
}

// resolveHead finds an agent's head from its salvage steps and locates its
// GitRef call, in the recorded trace when there is one, then in the telemetry
// rendering loaded. Results are remembered for /heads.
func (i *consoleTraceInspector) resolveHead(src consoleAgentSource, agent consoleAgent, steps []consoleSalvageStep) consoleHead {
	head := consoleHeadFromSteps(steps)
	head.Agent, head.Name = agent.ID, agent.Name
	if head.Commit == "" {
		return head
	}
	i.headsMu.Lock()
	cached, ok := i.knownHeads[agent.ID]
	i.headsMu.Unlock()
	if ok && cached.Commit == head.Commit && cached.Step == head.Step {
		// A live agent may have changed its workspace since; the head and
		// its recipe can't have.
		cached.Later = head.Later
		return cached
	}
	if src.db != nil {
		locateConsoleHead(src.db, &head)
		head.recorded = src.db
	}
	if !head.Recoverable {
		loaded := head
		loaded.Digest, loaded.Field, loaded.Calls = "", "", 0
		loaded.Problems, loaded.Fetches, loaded.Notes = nil, nil, nil
		loaded.recorded = nil
		fe := i.frontend
		fe.consoleMu.Lock()
		locateConsoleHead(fe.db, &loaded)
		fe.consoleMu.Unlock()
		if loaded.Recoverable || head.Digest == "" {
			head = loaded
		}
	}
	i.headsMu.Lock()
	if i.knownHeads == nil {
		i.knownHeads = map[string]consoleHead{}
	}
	i.knownHeads[agent.ID] = head
	i.headsMu.Unlock()
	return head
}

// encodedHeadID encodes a head's GitRef ID from the DB it was found in.
func (i *consoleTraceInspector) encodedHeadID(head consoleHead) (string, error) {
	if head.Digest == "" {
		return "", fmt.Errorf("no traced GitRef call for %s rebuilds", head.Commit)
	}
	if head.recorded != nil {
		return encodedIDForCallDigest(head.recorded, head.Digest)
	}
	i.frontend.consoleMu.Lock()
	defer i.frontend.consoleMu.Unlock()
	return encodedIDForCallDigest(i.frontend.db, head.Digest)
}

// head serves GET /head?agent=<handle|name>[&format=id]: the agent's head as
// JSON, or with format=id the encoded GitRef ID of a recoverable head, ready
// to load into any engine.
func (i *consoleTraceInspector) head(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("agent") == "" {
		http.Error(w, "agent is required: use /agents, then select a handle or unique name", http.StatusBadRequest)
		return
	}
	src := i.agentSource(r.Context())
	agent, snapshot, ok := readConsoleTranscript(w, r, src.agents, src.withWarning())
	if !ok {
		return
	}
	head := i.resolveHead(src, agent, consoleSalvageSteps(snapshot.Snapshot.Messages))
	if r.URL.Query().Get("format") != "id" {
		consoleJSON(w, head)
		return
	}
	switch {
	case head.Commit == "":
		http.Error(w, "no tool result recorded the workspace moving to a commit; replay the agent's /salvage steps instead", http.StatusNotFound)
		return
	case !head.Recoverable:
		http.Error(w, fmt.Sprintf("head %s is not recoverable: %s", head.Commit, strings.Join(head.Problems, "; ")), http.StatusConflict)
		return
	}
	encoded, err := i.encodedHeadID(head)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(encoded))
}

// heads serves GET /heads: one line per head found so far by /salvage or
// /head, "<handle>\t<commit>\t<yes|no>\t<name>", the third column saying
// whether it is recoverable. Empty until an agent has been looked at.
func (i *consoleTraceInspector) heads(w http.ResponseWriter, _ *http.Request) {
	i.headsMu.Lock()
	heads := make([]consoleHead, 0, len(i.knownHeads))
	for _, head := range i.knownHeads {
		if head.Commit != "" {
			heads = append(heads, head)
		}
	}
	i.headsMu.Unlock()
	sort.Slice(heads, func(a, b int) bool { return heads[a].Agent < heads[b].Agent })
	var b strings.Builder
	for _, head := range heads {
		recoverable := "no"
		if head.Recoverable {
			recoverable = "yes"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", head.Agent, head.Commit, recoverable, head.Name)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// writeConsoleHeadIndex renders the head section of a salvage index.
func writeConsoleHeadIndex(index *strings.Builder, head consoleHead) {
	index.WriteString("\n## Head\n\n")
	if head.Commit == "" {
		index.WriteString("No tool result recorded the workspace moving to a commit, so there is no head to recover.\n")
		return
	}
	fmt.Fprintf(index, "- commit: %s", head.Commit)
	if head.Repo != "" {
		fmt.Fprintf(index, " (%s)", head.Repo)
	}
	fmt.Fprintf(index, ", reported by step %04d %s\n", head.Step, head.Tool)
	if head.Digest != "" {
		fmt.Fprintf(index, "- GitRef call: %s (%s), a recipe of %d calls\n", head.Digest, head.Field, head.Calls)
	}
	if len(head.Fetches) > 0 {
		fmt.Fprintf(index, "- fetches: %s\n", strings.Join(head.Fetches, ", "))
	}
	for _, note := range head.Notes {
		fmt.Fprintf(index, "- note: %s\n", note)
	}
	if head.Recoverable {
		index.WriteString("- recoverable: yes\n\n" +
			"The recipe rebuilds from this trace alone and reads nothing from the client, " +
			"so any engine can evaluate it and reproduce this exact commit and its history. " +
			"Prefer it to replaying the steps: GET /head?agent=" + head.Agent + "&format=id " +
			"serves the encoded GitRef ID, to check out, cherry-pick or push.\n")
	} else {
		fmt.Fprintf(index, "- recoverable: no -- %s\n\nReplay the steps below instead.\n", strings.Join(head.Problems, "; "))
	}
	if head.Later > 0 {
		fmt.Fprintf(index, "\n%d workspace change(s) after step %04d are not in the head (uncommitted when the trace ended): replay those from steps/.\n", head.Later, head.Step)
	}
}
