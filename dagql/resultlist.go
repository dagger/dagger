package dagql

import "container/list"

type resultList struct {
	l   *list.List
	idx map[*sharedResult]*list.Element
}

func newResultList() *resultList {
	return &resultList{
		l:   list.New(),
		idx: map[*sharedResult]*list.Element{},
	}
}

func (rl *resultList) first() *sharedResult {
	if rl == nil || rl.l == nil || rl.l.Len() == 0 {
		return nil
	}
	return rl.l.Front().Value.(*sharedResult)
}

func (rl *resultList) add(res *sharedResult) {
	if rl == nil || rl.l == nil || res == nil {
		return
	}
	if _, ok := rl.idx[res]; ok {
		return
	}
	rl.idx[res] = rl.l.PushBack(res)
}

func (rl *resultList) remove(res *sharedResult) {
	if rl == nil || rl.l == nil || res == nil {
		return
	}
	if el, ok := rl.idx[res]; ok {
		rl.l.Remove(el)
		delete(rl.idx, res)
	}
}

func (rl *resultList) empty() bool {
	return rl == nil || rl.l == nil || rl.l.Len() == 0
}

func (rl *resultList) len() int {
	if rl == nil || rl.l == nil {
		return 0
	}
	return rl.l.Len()
}
