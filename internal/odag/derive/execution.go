package derive

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/internal/odag/store"
	"github.com/dagger/dagger/internal/odag/transform"
)

const (
	engineClientScopeName = "dagger.io/engine.client"
	engineClientConnect   = "connect"
)

type ScopeIndex struct {
	TraceID          string
	Clients          []Client
	Sessions         []Session
	FallbackSession  string
	SpanClientIDs    map[string]string
	SpanSessionIDs   map[string]string
	ClientByID       map[string]Client
	SessionByID      map[string]Session
	SessionByClient  map[string]string
	RootClientByID   map[string]string
	clientBySpanID   map[string]Client
	sessionByTraceID map[string]string
}

type Client struct {
	ID                string
	TraceID           string
	SessionID         string
	ParentClientID    string
	RootClientID      string
	SpanID            string
	Name              string
	CommandArgs       []string
	ServiceName       string
	ServiceVersion    string
	ScopeName         string
	ClientVersion     string
	ClientOS          string
	ClientArch        string
	ClientMachineID   string
	FirstSeenUnixNano int64
	LastSeenUnixNano  int64
}

type Session struct {
	ID                string
	TraceID           string
	RootClientID      string
	Fallback          bool
	FirstSeenUnixNano int64
	LastSeenUnixNano  int64
}

type spanEnvelope struct {
	Resource   map[string]any `json:"resource"`
	Scope      map[string]any `json:"scope"`
	Attributes map[string]any `json:"attributes"`
}

type rawSpan struct {
	Record      store.SpanRecord
	Resource    map[string]any
	Scope       map[string]any
	Attributes  map[string]any
	ScopeName   string
	ServiceName string
	Internal    bool
}

type clientState struct {
	Client
}

func BuildScopeIndex(traceID string, spans []store.SpanRecord, proj *transform.TraceProjection) *ScopeIndex {
	rawSpans := parseRawSpans(spans)
	bySpanID := make(map[string]rawSpan, len(rawSpans))
	for _, sp := range rawSpans {
		bySpanID[sp.Record.SpanID] = sp
	}

	clientStates := detectClients(traceID, rawSpans)
	preliminaryOwners := assignPreliminarySpanOwners(rawSpans, clientStates, bySpanID)
	assignParentClients(clientStates, preliminaryOwners, bySpanID)
	finalizeSessions(clientStates)

	idx := &ScopeIndex{
		TraceID:          traceID,
		Clients:          make([]Client, 0, len(clientStates)),
		Sessions:         nil,
		SpanClientIDs:    make(map[string]string, len(rawSpans)),
		SpanSessionIDs:   make(map[string]string, len(rawSpans)),
		ClientByID:       make(map[string]Client, len(clientStates)),
		SessionByID:      map[string]Session{},
		SessionByClient:  map[string]string{},
		RootClientByID:   map[string]string{},
		clientBySpanID:   map[string]Client{},
		sessionByTraceID: map[string]string{},
	}
	for _, cs := range clientStates {
		idx.Clients = append(idx.Clients, cs.Client)
		idx.ClientByID[cs.ID] = cs.Client
		idx.clientBySpanID[cs.SpanID] = cs.Client
		idx.SessionByClient[cs.ID] = cs.SessionID
		idx.RootClientByID[cs.ID] = cs.RootClientID
	}

	idx.Sessions = buildSessions(traceID, clientStates)
	for _, session := range idx.Sessions {
		idx.SessionByID[session.ID] = session
		idx.sessionByTraceID[traceID] = session.ID
	}

	if len(idx.Sessions) == 0 && projectionHasCalls(proj) {
		idx.FallbackSession = FallbackSessionID(traceID)
		session := Session{
			ID:                idx.FallbackSession,
			TraceID:           traceID,
			Fallback:          true,
			FirstSeenUnixNano: proj.StartUnixNano,
			LastSeenUnixNano:  proj.EndUnixNano,
		}
		idx.Sessions = []Session{session}
		idx.SessionByID[session.ID] = session
		idx.sessionByTraceID[traceID] = session.ID
	}

	assignFinalSpanOwnership(idx, rawSpans, bySpanID)
	return idx
}

func ClientID(traceID, spanID string) string {
	if traceID == "" || spanID == "" {
		return ""
	}
	return fmt.Sprintf("client:%s/%s", traceID, spanID)
}

func SessionID(rootClientID string) string {
	if rootClientID == "" {
		return ""
	}
	return "session:" + rootClientID
}

func FallbackSessionID(traceID string) string {
	if traceID == "" {
		return ""
	}
	return "session:trace:" + traceID
}

func TraceIDFromClientID(clientID string) string {
	rest, ok := strings.CutPrefix(clientID, "client:")
	if !ok {
		return ""
	}
	traceID, _, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	return traceID
}

func TraceIDFromSessionID(sessionID string) string {
	rest, ok := strings.CutPrefix(sessionID, "session:")
	if !ok {
		return ""
	}
	if traceID := TraceIDFromClientID(rest); traceID != "" {
		return traceID
	}
	traceID, ok := strings.CutPrefix(rest, "trace:")
	if !ok {
		return ""
	}
	return traceID
}

func (idx *ScopeIndex) SessionIDForSpan(spanID string) string {
	if idx == nil {
		return ""
	}
	return idx.SpanSessionIDs[spanID]
}

func (idx *ScopeIndex) ClientIDForSpan(spanID string) string {
	if idx == nil {
		return ""
	}
	return idx.SpanClientIDs[spanID]
}

func (idx *ScopeIndex) SessionIDForClient(clientID string) string {
	if idx == nil {
		return ""
	}
	return idx.SessionByClient[clientID]
}

func parseRawSpans(spans []store.SpanRecord) []rawSpan {
	out := make([]rawSpan, 0, len(spans))
	for _, sp := range spans {
		raw := rawSpan{Record: sp}
		var env spanEnvelope
		if sp.DataJSON != "" {
			_ = json.Unmarshal([]byte(sp.DataJSON), &env)
		}
		raw.Resource = env.Resource
		raw.Scope = env.Scope
		raw.Attributes = env.Attributes
		raw.ScopeName, _ = getString(raw.Scope, "name")
		raw.ServiceName, _ = getString(raw.Resource, "service.name")
		raw.Internal, _ = getBool(raw.Attributes, telemetry.UIInternalAttr)
		out = append(out, raw)
	}
	slices.SortFunc(out, compareRawSpanStart)
	return out
}

func detectClients(traceID string, spans []rawSpan) []*clientState {
	clients := make([]*clientState, 0)
	for _, sp := range spans {
		if !isClientConnectSpan(sp) {
			continue
		}
		client := Client{
			ID:                ClientID(traceID, sp.Record.SpanID),
			TraceID:           traceID,
			SpanID:            sp.Record.SpanID,
			Name:              clientName(sp),
			CommandArgs:       getStringSlice(sp.Resource, "process.command_args"),
			ServiceName:       sp.ServiceName,
			ServiceVersion:    getStringDefault(sp.Resource, "service.version"),
			ScopeName:         sp.ScopeName,
			ClientVersion:     getStringDefault(sp.Resource, "dagger.io/client.version"),
			ClientOS:          getStringDefault(sp.Resource, "dagger.io/client.os"),
			ClientArch:        getStringDefault(sp.Resource, "dagger.io/client.arch"),
			ClientMachineID:   getStringDefault(sp.Resource, "dagger.io/client.machine_id"),
			FirstSeenUnixNano: sp.Record.StartUnixNano,
			LastSeenUnixNano:  spanEndUnixNano(sp.Record),
		}
		clients = append(clients, &clientState{Client: client})
	}
	slices.SortFunc(clients, func(a, b *clientState) int {
		if cmp := compareInt64(a.FirstSeenUnixNano, b.FirstSeenUnixNano); cmp != 0 {
			return cmp
		}
		if cmp := compareInt64(b.LastSeenUnixNano, a.LastSeenUnixNano); cmp != 0 {
			return cmp
		}
		return compareString(a.SpanID, b.SpanID)
	})
	return clients
}

func assignPreliminarySpanOwners(spans []rawSpan, clients []*clientState, bySpanID map[string]rawSpan) map[string]string {
	ownerBySpanID := make(map[string]string, len(spans))
	clientBySpanID := make(map[string]string, len(clients))
	for _, client := range clients {
		clientBySpanID[client.SpanID] = client.ID
	}
	for _, sp := range spans {
		if clientID, ok := clientBySpanID[sp.Record.SpanID]; ok {
			ownerBySpanID[sp.Record.SpanID] = clientID
			continue
		}
		if owner := nearestAncestorOwner(sp.Record.ParentSpanID, ownerBySpanID, bySpanID); owner != "" {
			ownerBySpanID[sp.Record.SpanID] = owner
			continue
		}
		if owner := latestClientBefore(clients, sp.Record.StartUnixNano, ""); owner != "" {
			ownerBySpanID[sp.Record.SpanID] = owner
		}
	}
	return ownerBySpanID
}

func assignParentClients(clients []*clientState, ownerBySpanID map[string]string, bySpanID map[string]rawSpan) {
	if len(clients) == 0 {
		return
	}
	for _, client := range clients {
		parentID := ""
		parentSpanID := bySpanID[client.SpanID].Record.ParentSpanID
		for parentSpanID != "" {
			if owner := ownerBySpanID[parentSpanID]; owner != "" && owner != client.ID {
				parentID = owner
				break
			}
			parentSpan, ok := bySpanID[parentSpanID]
			if !ok {
				break
			}
			parentSpanID = parentSpan.Record.ParentSpanID
		}
		client.ParentClientID = parentID
	}
}

func finalizeSessions(clients []*clientState) {
	if len(clients) == 0 {
		return
	}
	byID := make(map[string]*clientState, len(clients))
	for _, client := range clients {
		byID[client.ID] = client
	}
	for _, client := range clients {
		root := client
		for root.ParentClientID != "" {
			next := byID[root.ParentClientID]
			if next == nil {
				break
			}
			root = next
		}
		client.RootClientID = root.ID
		client.SessionID = SessionID(root.ID)
	}
}

func buildSessions(traceID string, clients []*clientState) []Session {
	if len(clients) == 0 {
		return nil
	}
	byID := make(map[string]*Session, len(clients))
	for _, client := range clients {
		if client.SessionID == "" {
			continue
		}
		session := byID[client.SessionID]
		if session == nil {
			session = &Session{
				ID:                client.SessionID,
				TraceID:           traceID,
				RootClientID:      client.RootClientID,
				FirstSeenUnixNano: client.FirstSeenUnixNano,
				LastSeenUnixNano:  client.LastSeenUnixNano,
			}
			byID[client.SessionID] = session
		}
		if client.FirstSeenUnixNano > 0 && (session.FirstSeenUnixNano == 0 || client.FirstSeenUnixNano < session.FirstSeenUnixNano) {
			session.FirstSeenUnixNano = client.FirstSeenUnixNano
		}
		if client.LastSeenUnixNano > session.LastSeenUnixNano {
			session.LastSeenUnixNano = client.LastSeenUnixNano
		}
	}
	sessions := make([]Session, 0, len(byID))
	for _, session := range byID {
		sessions = append(sessions, *session)
	}
	slices.SortFunc(sessions, func(a, b Session) int {
		if cmp := compareInt64(a.FirstSeenUnixNano, b.FirstSeenUnixNano); cmp != 0 {
			return cmp
		}
		return compareString(a.ID, b.ID)
	})
	return sessions
}

func assignFinalSpanOwnership(idx *ScopeIndex, spans []rawSpan, bySpanID map[string]rawSpan) {
	if idx == nil {
		return
	}
	for _, sp := range spans {
		if client, ok := idx.clientBySpanID[sp.Record.SpanID]; ok {
			idx.SpanClientIDs[sp.Record.SpanID] = client.ID
			idx.SpanSessionIDs[sp.Record.SpanID] = client.SessionID
			continue
		}
		if owner := nearestAncestorOwner(sp.Record.ParentSpanID, idx.SpanClientIDs, bySpanID); owner != "" {
			idx.SpanClientIDs[sp.Record.SpanID] = owner
			if sessionID := idx.SessionByClient[owner]; sessionID != "" {
				idx.SpanSessionIDs[sp.Record.SpanID] = sessionID
			}
			continue
		}
		if owner := latestClientForTraceScope(sp.Record.StartUnixNano, idx.Clients); owner != "" {
			idx.SpanClientIDs[sp.Record.SpanID] = owner
			if sessionID := idx.SessionByClient[owner]; sessionID != "" {
				idx.SpanSessionIDs[sp.Record.SpanID] = sessionID
			}
			continue
		}
		if idx.FallbackSession != "" {
			idx.SpanSessionIDs[sp.Record.SpanID] = idx.FallbackSession
		}
	}
}

func isClientConnectSpan(sp rawSpan) bool {
	return sp.ScopeName == engineClientScopeName && sp.Record.Name == engineClientConnect
}

func clientName(sp rawSpan) string {
	if args := getStringSlice(sp.Resource, "process.command_args"); len(args) > 0 {
		return strings.Join(args, " ")
	}
	if service := strings.TrimSpace(sp.ServiceName); service != "" {
		return service
	}
	return sp.Record.Name
}

func latestClientBefore(clients []*clientState, startUnixNano int64, rootClientID string) string {
	bestID := ""
	bestStart := int64(-1)
	bestEnd := int64(-1)
	for _, client := range clients {
		if client.FirstSeenUnixNano > startUnixNano {
			continue
		}
		if rootClientID != "" && client.RootClientID != rootClientID {
			continue
		}
		if client.FirstSeenUnixNano > bestStart ||
			(client.FirstSeenUnixNano == bestStart && client.LastSeenUnixNano > bestEnd) ||
			(client.FirstSeenUnixNano == bestStart && client.LastSeenUnixNano == bestEnd && client.ID > bestID) {
			bestID = client.ID
			bestStart = client.FirstSeenUnixNano
			bestEnd = client.LastSeenUnixNano
		}
	}
	return bestID
}

func latestClientForTraceScope(startUnixNano int64, clients []Client) string {
	bestRootID := ""
	bestRootStart := int64(-1)
	roots := make([]Client, 0, len(clients))
	for _, client := range clients {
		if client.ParentClientID == "" {
			roots = append(roots, client)
		}
	}
	for _, root := range roots {
		if root.FirstSeenUnixNano > startUnixNano {
			continue
		}
		if root.FirstSeenUnixNano > bestRootStart || (root.FirstSeenUnixNano == bestRootStart && root.ID > bestRootID) {
			bestRootID = root.ID
			bestRootStart = root.FirstSeenUnixNano
		}
	}
	bestClientID := ""
	bestStart := int64(-1)
	bestEnd := int64(-1)
	for _, client := range clients {
		if client.FirstSeenUnixNano > startUnixNano {
			continue
		}
		if bestRootID != "" && client.RootClientID != bestRootID {
			continue
		}
		if client.FirstSeenUnixNano > bestStart ||
			(client.FirstSeenUnixNano == bestStart && client.LastSeenUnixNano > bestEnd) ||
			(client.FirstSeenUnixNano == bestStart && client.LastSeenUnixNano == bestEnd && client.ID > bestClientID) {
			bestClientID = client.ID
			bestStart = client.FirstSeenUnixNano
			bestEnd = client.LastSeenUnixNano
		}
	}
	return bestClientID
}

func nearestAncestorOwner(parentSpanID string, ownerBySpanID map[string]string, bySpanID map[string]rawSpan) string {
	for parentSpanID != "" {
		if owner := ownerBySpanID[parentSpanID]; owner != "" {
			return owner
		}
		parentSpan, ok := bySpanID[parentSpanID]
		if !ok {
			return ""
		}
		parentSpanID = parentSpan.Record.ParentSpanID
	}
	return ""
}

func projectionHasCalls(proj *transform.TraceProjection) bool {
	if proj == nil {
		return false
	}
	for _, event := range proj.Events {
		if event.RawKind == "call" {
			return true
		}
	}
	return false
}

func compareRawSpanStart(a, b rawSpan) int {
	if cmp := compareInt64(a.Record.StartUnixNano, b.Record.StartUnixNano); cmp != 0 {
		return cmp
	}
	if cmp := compareInt64(spanEndUnixNano(b.Record), spanEndUnixNano(a.Record)); cmp != 0 {
		return cmp
	}
	return compareString(a.Record.SpanID, b.Record.SpanID)
}

func compareInt64(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func compareString(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func spanEndUnixNano(sp store.SpanRecord) int64 {
	if sp.EndUnixNano > 0 {
		return sp.EndUnixNano
	}
	return sp.StartUnixNano
}

func getBool(m map[string]any, key string) (bool, bool) {
	if m == nil {
		return false, false
	}
	raw, ok := m[key]
	if !ok {
		return false, false
	}
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

func getString(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	raw, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := raw.(string)
	return s, ok
}

func getStringDefault(m map[string]any, key string) string {
	v, _ := getString(m, key)
	return v
}

func getStringSlice(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	raw, ok := m[key]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok || s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}
