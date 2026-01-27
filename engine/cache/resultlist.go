package cache

import "container/list"

type resultList[K KeyType, V any] struct {
	l   *list.List
	idx map[*result[K, V]]*list.Element
}

func newResultList[K KeyType, V any]() *resultList[K, V] {
	return &resultList[K, V]{
		l:   list.New(),
		idx: map[*result[K, V]]*list.Element{},
	}
}

func (rl *resultList[K, V]) first() *result[K, V] {
	if rl == nil || rl.l == nil || rl.l.Len() == 0 {
		return nil
	}
	return rl.l.Front().Value.(*result[K, V])
}

func (rl *resultList[K, V]) add(res *result[K, V]) {
	if rl == nil || rl.l == nil || res == nil {
		return
	}
	if _, ok := rl.idx[res]; ok {
		return
	}
	rl.idx[res] = rl.l.PushBack(res)
}

func (rl *resultList[K, V]) remove(res *result[K, V]) {
	if rl == nil || rl.l == nil || res == nil {
		return
	}
	if el, ok := rl.idx[res]; ok {
		rl.l.Remove(el)
		delete(rl.idx, res)
	}
}

func (rl *resultList[K, V]) empty() bool {
	return rl == nil || rl.l == nil || rl.l.Len() == 0
}

func (rl *resultList[K, V]) len() int {
	if rl == nil || rl.l == nil {
		return 0
	}
	return rl.l.Len()
}
