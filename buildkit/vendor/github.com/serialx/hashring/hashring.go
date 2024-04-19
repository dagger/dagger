package hashring

import (
	"crypto/md5"
	"fmt"
	"sort"
	"strconv"
)

var defaultHashFunc = func() HashFunc {
	hashFunc, err := NewHash(md5.New).Use(NewInt64PairHashKey)
	if err != nil {
		panic(fmt.Sprintf("failed to create defaultHashFunc: %s", err.Error()))
	}
	return hashFunc
}()

type HashKey interface {
	Less(other HashKey) bool
}
type HashKeyOrder []HashKey

func (h HashKeyOrder) Len() int      { return len(h) }
func (h HashKeyOrder) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h HashKeyOrder) Less(i, j int) bool {
	return h[i].Less(h[j])
}

type HashFunc func([]byte) HashKey

type HashRing struct {
	ring       map[HashKey]string
	sortedKeys []HashKey
	nodes      []string
	weights    map[string]int
	hashFunc   HashFunc
}

type Uint32HashKey uint32

func (k Uint32HashKey) Less(other HashKey) bool {
	return k < other.(Uint32HashKey)
}

func New(nodes []string) *HashRing {
	return NewWithHash(nodes, defaultHashFunc)
}

func NewWithHash(
	nodes []string,
	hashKey HashFunc,
) *HashRing {
	hashRing := &HashRing{
		ring:       make(map[HashKey]string),
		sortedKeys: make([]HashKey, 0),
		nodes:      nodes,
		weights:    make(map[string]int),
		hashFunc:   hashKey,
	}
	hashRing.generateCircle()
	return hashRing
}

func NewWithWeights(weights map[string]int) *HashRing {
	return NewWithHashAndWeights(weights, defaultHashFunc)
}

func NewWithHashAndWeights(
	weights map[string]int,
	hashFunc HashFunc,
) *HashRing {
	nodes := make([]string, 0, len(weights))
	for node := range weights {
		nodes = append(nodes, node)
	}
	hashRing := &HashRing{
		ring:       make(map[HashKey]string),
		sortedKeys: make([]HashKey, 0),
		nodes:      nodes,
		weights:    weights,
		hashFunc:   hashFunc,
	}
	hashRing.generateCircle()
	return hashRing
}

func (h *HashRing) Size() int {
	return len(h.nodes)
}

func (h *HashRing) UpdateWithWeights(weights map[string]int) {
	nodesChgFlg := false
	if len(weights) != len(h.weights) {
		nodesChgFlg = true
	} else {
		for node, newWeight := range weights {
			oldWeight, ok := h.weights[node]
			if !ok || oldWeight != newWeight {
				nodesChgFlg = true
				break
			}
		}
	}

	if nodesChgFlg {
		newhring := NewWithHashAndWeights(weights, h.hashFunc)
		h.weights = newhring.weights
		h.nodes = newhring.nodes
		h.ring = newhring.ring
		h.sortedKeys = newhring.sortedKeys
	}
}

func (h *HashRing) generateCircle() {
	totalWeight := 0
	for _, node := range h.nodes {
		if weight, ok := h.weights[node]; ok {
			totalWeight += weight
		} else {
			totalWeight += 1
			h.weights[node] = 1
		}
	}

	for _, node := range h.nodes {
		weight := h.weights[node]

		for j := 0; j < weight; j++ {
			nodeKey := node + "-" + strconv.FormatInt(int64(j), 10)
			key := h.hashFunc([]byte(nodeKey))
			h.ring[key] = node
			h.sortedKeys = append(h.sortedKeys, key)
		}
	}

	sort.Sort(HashKeyOrder(h.sortedKeys))
}

func (h *HashRing) GetNode(stringKey string) (node string, ok bool) {
	pos, ok := h.GetNodePos(stringKey)
	if !ok {
		return "", false
	}
	return h.ring[h.sortedKeys[pos]], true
}

func (h *HashRing) GetNodePos(stringKey string) (pos int, ok bool) {
	if len(h.ring) == 0 {
		return 0, false
	}

	key := h.GenKey(stringKey)

	nodes := h.sortedKeys
	pos = sort.Search(len(nodes), func(i int) bool { return key.Less(nodes[i]) })

	if pos == len(nodes) {
		// Wrap the search, should return First node
		return 0, true
	} else {
		return pos, true
	}
}

func (h *HashRing) GenKey(key string) HashKey {
	return h.hashFunc([]byte(key))
}

// GetNodes iterates over the hash ring and returns the nodes in the order
// which is determined by the key. GetNodes is thread safe if the hash
// which was used to configure the hash ring is thread safe.
func (h *HashRing) GetNodes(stringKey string, size int) (nodes []string, ok bool) {
	pos, ok := h.GetNodePos(stringKey)
	if !ok {
		return nil, false
	}

	if size > len(h.nodes) {
		return nil, false
	}

	returnedValues := make(map[string]bool, size)
	//mergedSortedKeys := append(h.sortedKeys[pos:], h.sortedKeys[:pos]...)
	resultSlice := make([]string, 0, size)

	for i := pos; i < pos+len(h.sortedKeys); i++ {
		key := h.sortedKeys[i%len(h.sortedKeys)]
		val := h.ring[key]
		if !returnedValues[val] {
			returnedValues[val] = true
			resultSlice = append(resultSlice, val)
		}
		if len(returnedValues) == size {
			break
		}
	}

	return resultSlice, len(resultSlice) == size
}

func (h *HashRing) AddNode(node string) *HashRing {
	return h.AddWeightedNode(node, 1)
}

func (h *HashRing) AddWeightedNode(node string, weight int) *HashRing {
	if weight <= 0 {
		return h
	}

	if _, ok := h.weights[node]; ok {
		return h
	}

	nodes := make([]string, len(h.nodes), len(h.nodes)+1)
	copy(nodes, h.nodes)
	nodes = append(nodes, node)

	weights := make(map[string]int)
	for eNode, eWeight := range h.weights {
		weights[eNode] = eWeight
	}
	weights[node] = weight

	hashRing := &HashRing{
		ring:       make(map[HashKey]string),
		sortedKeys: make([]HashKey, 0),
		nodes:      nodes,
		weights:    weights,
		hashFunc:   h.hashFunc,
	}
	hashRing.generateCircle()
	return hashRing
}

func (h *HashRing) UpdateWeightedNode(node string, weight int) *HashRing {
	if weight <= 0 {
		return h
	}

	/* node is not need to update for node is not existed or weight is not changed */
	if oldWeight, ok := h.weights[node]; (!ok) || (ok && oldWeight == weight) {
		return h
	}

	nodes := make([]string, len(h.nodes))
	copy(nodes, h.nodes)

	weights := make(map[string]int)
	for eNode, eWeight := range h.weights {
		weights[eNode] = eWeight
	}
	weights[node] = weight

	hashRing := &HashRing{
		ring:       make(map[HashKey]string),
		sortedKeys: make([]HashKey, 0),
		nodes:      nodes,
		weights:    weights,
		hashFunc:   h.hashFunc,
	}
	hashRing.generateCircle()
	return hashRing
}
func (h *HashRing) RemoveNode(node string) *HashRing {
	/* if node isn't exist in hashring, don't refresh hashring */
	if _, ok := h.weights[node]; !ok {
		return h
	}

	nodes := make([]string, 0)
	for _, eNode := range h.nodes {
		if eNode != node {
			nodes = append(nodes, eNode)
		}
	}

	weights := make(map[string]int)
	for eNode, eWeight := range h.weights {
		if eNode != node {
			weights[eNode] = eWeight
		}
	}

	hashRing := &HashRing{
		ring:       make(map[HashKey]string),
		sortedKeys: make([]HashKey, 0),
		nodes:      nodes,
		weights:    weights,
		hashFunc:   h.hashFunc,
	}
	hashRing.generateCircle()
	return hashRing
}
