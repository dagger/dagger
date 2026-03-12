package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/store"
	"github.com/dagger/dagger/internal/odag/transform"
)

type v2ObjectType struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	ModuleRef         string   `json:"moduleRef,omitempty"`
	FirstSeenUnixNano int64    `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64    `json:"lastSeenUnixNano"`
	TraceCount        int      `json:"traceCount"`
	SessionCount      int      `json:"sessionCount"`
	ClientCount       int      `json:"clientCount"`
	SnapshotCount     int      `json:"snapshotCount"`
	FunctionCount     int      `json:"functionCount"`
	TraceIDs          []string `json:"traceIDs"`
	SessionIDs        []string `json:"sessionIDs"`
	ClientIDs         []string `json:"clientIDs"`
	SnapshotDagqlIDs  []string `json:"snapshotDagqlIDs,omitempty"`
	FunctionDagqlIDs  []string `json:"functionDagqlIDs,omitempty"`
	FunctionNames     []string `json:"functionNames,omitempty"`
	ModuleRefs        []string `json:"moduleRefs,omitempty"`
}

type v2ObjectTypeAggregate struct {
	item             v2ObjectType
	traceIDs         map[string]struct{}
	sessionIDs       map[string]struct{}
	clientIDs        map[string]struct{}
	snapshotDagqlIDs map[string]struct{}
	functionDagqlIDs map[string]struct{}
	functionNames    map[string]struct{}
	moduleRefs       map[string]struct{}
	sawWithoutModule bool
}

type v2TraceObjectTypeClassifier struct {
	moduleIndex                 *v2ModuleIndex
	moduleDefinedTypeNamesByRef map[string]map[string]struct{}
	coreTypeNames               map[string]struct{}
}

var v2KnownCoreObjectTypeNames = map[string]struct{}{
	"CacheVolume":   {},
	"Changeset":     {},
	"Container":     {},
	"Directory":     {},
	"File":          {},
	"GitRef":        {},
	"GitRepository": {},
	"Secret":        {},
	"Service":       {},
}

func (s *Server) handleV2ObjectTypes(w http.ResponseWriter, r *http.Request) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q)
	if err != nil {
		http.Error(w, fmt.Sprintf("resolve traces: %v", err), http.StatusInternalServerError)
		return
	}

	byName := map[string]*v2ObjectTypeAggregate{}
	for _, traceID := range traceIDs {
		spans, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		classifier, err := buildV2TraceObjectTypeClassifier(traceID, spans, proj, scopeIdx)
		if err != nil {
			http.Error(w, fmt.Sprintf("build object type classifier for trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		for _, obj := range proj.Objects {
			for _, st := range obj.StateHistory {
				if st.StateDigest == "" {
					continue
				}
				if !q.IncludeInternal {
					if event := findProjectionEvent(proj, st.SpanID); event != nil && event.Internal {
						continue
					}
				}
				if !intersectsTime(st.StartUnixNano, st.EndUnixNano, q.FromUnixNano, q.ToUnixNano) {
					continue
				}
				sessionID := scopeIdx.SessionIDForSpan(st.SpanID)
				clientID := scopeIdx.ClientIDForSpan(st.SpanID)
				if !matchesV2Scope(q, traceID, sessionID, clientID) {
					continue
				}
				typeName := v2ResolvedOutputStateTypeName(obj.TypeName, st.OutputStateJSON)
				typeID, moduleRefs := classifier.classify(typeName, sessionID, clientID, true)
				recordV2ObjectTypeObservation(byName, typeID, typeName, traceID, sessionID, clientID, st.StartUnixNano, st.EndUnixNano, moduleRefs, st.StateDigest, "", "")
				if returnType, functionName := v2FunctionReturnObjectType(st.OutputStateJSON); returnType != "" {
					returnTypeID, returnTypeModuleRefs := classifier.classify(returnType, sessionID, clientID, true)
					recordV2ObjectTypeObservation(byName, returnTypeID, returnType, traceID, sessionID, clientID, st.StartUnixNano, st.EndUnixNano, returnTypeModuleRefs, "", st.StateDigest, functionName)
				}
			}
		}
	}

	items := make([]v2ObjectType, 0, len(byName))
	for _, agg := range byName {
		agg.item.TraceIDs = setToSortedSlice(agg.traceIDs)
		agg.item.SessionIDs = setToSortedSlice(agg.sessionIDs)
		agg.item.ClientIDs = setToSortedSlice(agg.clientIDs)
		agg.item.SnapshotDagqlIDs = setToSortedSlice(agg.snapshotDagqlIDs)
		agg.item.FunctionDagqlIDs = setToSortedSlice(agg.functionDagqlIDs)
		agg.item.FunctionNames = setToSortedSlice(agg.functionNames)
		agg.item.TraceCount = len(agg.item.TraceIDs)
		agg.item.SessionCount = len(agg.item.SessionIDs)
		agg.item.ClientCount = len(agg.item.ClientIDs)
		agg.item.SnapshotCount = len(agg.item.SnapshotDagqlIDs)
		agg.item.FunctionCount = len(agg.item.FunctionDagqlIDs)
		if !agg.sawWithoutModule {
			agg.item.ModuleRefs = setToSortedSlice(agg.moduleRefs)
		}
		if len(agg.item.ModuleRefs) > 0 {
			agg.item.ModuleRef = agg.item.ModuleRefs[0]
		}
		items = append(items, agg.item)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].LastSeenUnixNano != items[j].LastSeenUnixNano {
			return items[i].LastSeenUnixNano > items[j].LastSeenUnixNano
		}
		return items[i].Name < items[j].Name
	})

	page, next := paginate(items, q.Offset, q.Limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"derivationVersion": derivationVersionV2,
		"items":             page,
		"nextCursor":        next,
	})
}

func recordV2ObjectTypeObservation(
	byName map[string]*v2ObjectTypeAggregate,
	typeID string,
	typeName string,
	traceID string,
	sessionID string,
	clientID string,
	startUnixNano int64,
	endUnixNano int64,
	moduleRefs []string,
	snapshotDagqlID string,
	functionDagqlID string,
	functionName string,
) {
	typeName = strings.TrimSpace(typeName)
	if typeName == "" || strings.EqualFold(typeName, "Function") || strings.EqualFold(typeName, "Module") || strings.EqualFold(typeName, "ModuleSource") {
		return
	}
	typeID = strings.TrimSpace(typeID)
	if typeID == "" {
		typeID = v2ObjectTypeID(typeName, "")
	}
	agg := byName[typeID]
	if agg == nil {
		agg = &v2ObjectTypeAggregate{
			item: v2ObjectType{
				ID:                typeID,
				Name:              typeName,
				FirstSeenUnixNano: startUnixNano,
				LastSeenUnixNano:  endUnixNano,
			},
			traceIDs:         map[string]struct{}{},
			sessionIDs:       map[string]struct{}{},
			clientIDs:        map[string]struct{}{},
			snapshotDagqlIDs: map[string]struct{}{},
			functionDagqlIDs: map[string]struct{}{},
			functionNames:    map[string]struct{}{},
			moduleRefs:       map[string]struct{}{},
		}
		byName[typeID] = agg
	}
	if agg.item.FirstSeenUnixNano == 0 || (startUnixNano > 0 && startUnixNano < agg.item.FirstSeenUnixNano) {
		agg.item.FirstSeenUnixNano = startUnixNano
	}
	if endUnixNano > agg.item.LastSeenUnixNano {
		agg.item.LastSeenUnixNano = endUnixNano
	}
	if traceID != "" {
		agg.traceIDs[traceID] = struct{}{}
	}
	if sessionID != "" {
		agg.sessionIDs[sessionID] = struct{}{}
	}
	if clientID != "" {
		agg.clientIDs[clientID] = struct{}{}
	}
	if snapshotDagqlID != "" {
		agg.snapshotDagqlIDs[snapshotDagqlID] = struct{}{}
	}
	if functionDagqlID != "" {
		agg.functionDagqlIDs[functionDagqlID] = struct{}{}
	}
	if functionName != "" {
		agg.functionNames[functionName] = struct{}{}
	}
	if len(moduleRefs) > 0 {
		for _, moduleRef := range moduleRefs {
			moduleRef = strings.TrimSpace(moduleRef)
			if moduleRef == "" {
				continue
			}
			agg.moduleRefs[moduleRef] = struct{}{}
		}
	} else {
		agg.sawWithoutModule = true
	}
}

func buildV2TraceObjectTypeClassifier(traceID string, spans []store.SpanRecord, proj *transform.TraceProjection, scopeIdx *derive.ScopeIndex) (*v2TraceObjectTypeClassifier, error) {
	moduleIndex, err := buildV2TraceModuleIndex(traceID, spans, proj, scopeIdx)
	if err != nil {
		return nil, err
	}
	classifier := &v2TraceObjectTypeClassifier{
		moduleIndex:                 moduleIndex,
		moduleDefinedTypeNamesByRef: map[string]map[string]struct{}{},
		coreTypeNames:               map[string]struct{}{},
	}
	for typeName := range v2KnownCoreObjectTypeNames {
		classifier.coreTypeNames[typeName] = struct{}{}
	}
	for _, obj := range proj.Objects {
		for _, st := range obj.StateHistory {
			typeName := v2ResolvedOutputStateTypeName(obj.TypeName, st.OutputStateJSON)
			if typeName == "" {
				continue
			}
			sessionID := scopeIdx.SessionIDForSpan(st.SpanID)
			clientID := scopeIdx.ClientIDForSpan(st.SpanID)
			moduleRef := classifier.moduleRefForScope(sessionID, clientID)
			if moduleRef != "" {
				for _, name := range v2OutputStateModuleDefinedTypeNames(st.OutputStateJSON) {
					classifier.addModuleDefinedTypeName(moduleRef, name)
				}
				if v2OutputStateLooksModuleOwned(st.OutputStateJSON) {
					classifier.addModuleDefinedTypeName(moduleRef, typeName)
				}
			}
			if !v2OutputStateLooksModuleOwned(st.OutputStateJSON) && (moduleRef == "" || v2IsKnownCoreObjectTypeName(typeName)) {
				classifier.coreTypeNames[typeName] = struct{}{}
			}
		}
	}
	return classifier, nil
}

func (c *v2TraceObjectTypeClassifier) moduleRefForScope(sessionID, clientID string) string {
	if c == nil || c.moduleIndex == nil {
		return ""
	}
	clientID = strings.TrimSpace(clientID)
	if clientID != "" {
		if ref := strings.TrimSpace(c.moduleIndex.PreferredRefByClient[clientID]); ref != "" {
			return ref
		}
	}
	return strings.TrimSpace(c.moduleIndex.PreferredRefBySession[sessionID])
}

func (c *v2TraceObjectTypeClassifier) addModuleDefinedTypeName(moduleRef, typeName string) {
	moduleRef = strings.TrimSpace(moduleRef)
	typeName = strings.TrimSpace(typeName)
	if moduleRef == "" || typeName == "" {
		return
	}
	names := c.moduleDefinedTypeNamesByRef[moduleRef]
	if names == nil {
		names = map[string]struct{}{}
		c.moduleDefinedTypeNamesByRef[moduleRef] = names
	}
	names[typeName] = struct{}{}
}

func (c *v2TraceObjectTypeClassifier) classify(typeName, sessionID, clientID string, allowModuleFallback bool) (string, []string) {
	typeName = strings.TrimSpace(typeName)
	if typeName == "" {
		return "", nil
	}
	moduleRef := c.moduleRefForScope(sessionID, clientID)
	if moduleRef != "" {
		if names := c.moduleDefinedTypeNamesByRef[moduleRef]; len(names) > 0 {
			if _, ok := names[typeName]; ok {
				return v2ObjectTypeID(typeName, moduleRef), []string{moduleRef}
			}
		}
	}
	if moduleRef == "" || !allowModuleFallback {
		return v2ObjectTypeID(typeName, ""), nil
	}
	if _, ok := c.coreTypeNames[typeName]; ok {
		return v2ObjectTypeID(typeName, ""), nil
	}
	return v2ObjectTypeID(typeName, moduleRef), []string{moduleRef}
}

func v2IsKnownCoreObjectTypeName(typeName string) bool {
	_, ok := v2KnownCoreObjectTypeNames[strings.TrimSpace(typeName)]
	return ok
}

func v2ObjectTypeID(typeName, moduleRef string) string {
	typeName = strings.TrimSpace(typeName)
	moduleRef = strings.TrimSpace(moduleRef)
	if moduleRef == "" {
		return "type:" + typeName
	}
	return "type:" + moduleRef + "#" + typeName
}

func v2ResolvedOutputStateTypeName(fallbackTypeName string, outputState map[string]any) string {
	typeName := strings.TrimSpace(fallbackTypeName)
	if stateType := v2OutputStateTypeName(outputState); stateType != "" {
		typeName = stateType
	}
	return strings.TrimSpace(typeName)
}

func v2OutputStateLooksModuleOwned(outputState map[string]any) bool {
	if len(v2OutputStateModuleDefinedTypeNames(outputState)) > 0 {
		return true
	}
	typeDef := v2OutputStateFieldMap(outputState, "TypeDef")
	value, _ := typeDef["value"].(map[string]any)
	name, _ := getV2String(value, "Name")
	return strings.TrimSpace(name) != ""
}

func v2OutputStateModuleDefinedTypeNames(outputState map[string]any) []string {
	set := map[string]struct{}{}
	typeDef := v2OutputStateFieldMap(outputState, "TypeDef")
	if value, _ := typeDef["value"].(map[string]any); value != nil {
		if name := v2ObjectTypeDefName(value); name != "" {
			set[name] = struct{}{}
		}
	}
	module := v2OutputStateFieldMap(outputState, "Module")
	moduleValue, _ := module["value"].(map[string]any)
	defs, _ := moduleValue["ObjectDefs"].([]any)
	for _, item := range defs {
		def, _ := item.(map[string]any)
		if name := v2ObjectTypeDefName(def); name != "" {
			set[name] = struct{}{}
		}
	}
	return setToSortedSlice(set)
}

func v2ObjectTypeDefName(typeDef map[string]any) string {
	if typeDef == nil {
		return ""
	}
	asObject, _ := typeDef["AsObject"].(map[string]any)
	valid, ok := getV2Bool(asObject, "Valid")
	if ok && !valid {
		return ""
	}
	value, _ := asObject["Value"].(map[string]any)
	if value == nil {
		value = typeDef
	}
	name, _ := getV2String(value, "OriginalName")
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	name, _ = getV2String(value, "Name")
	return strings.TrimSpace(name)
}

func v2OutputStateTypeName(outputState map[string]any) string {
	if outputState == nil {
		return ""
	}
	value, _ := getV2String(outputState, "type")
	return strings.TrimSpace(value)
}

func v2OutputStateFieldMap(outputState map[string]any, name string) map[string]any {
	fields, _ := outputState["fields"].(map[string]any)
	field, _ := fields[name].(map[string]any)
	return field
}

func v2OutputStateFieldString(outputState map[string]any, name string) string {
	field := v2OutputStateFieldMap(outputState, name)
	value, _ := getV2String(field, "value")
	return strings.TrimSpace(value)
}

func v2FunctionReturnObjectType(outputState map[string]any) (typeName string, functionName string) {
	if strings.TrimSpace(v2OutputStateTypeName(outputState)) != "Function" {
		return "", ""
	}
	functionName = v2OutputStateFieldString(outputState, "Name")
	field := v2OutputStateFieldMap(outputState, "ReturnType")
	value, _ := field["value"].(map[string]any)
	asObject, _ := value["AsObject"].(map[string]any)
	valid, _ := getV2Bool(asObject, "Valid")
	if !valid {
		return "", functionName
	}
	obj, _ := asObject["Value"].(map[string]any)
	typeName, _ = getV2String(obj, "Name")
	return strings.TrimSpace(typeName), functionName
}
