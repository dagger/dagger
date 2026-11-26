package dagql

import (
	set "github.com/hashicorp/go-set/v3"
)

// A list whose value holds its items inline is read one position at a time
// (Result.NthValue), and each item read is a call: the list's call with the
// position, on the list as receiver. Its recipe names the list by the list's
// recipe, so it cannot tell two entries of one list recipe apart, though they
// can hold different values: one expired while still in use and was computed
// again, or a session that did not cover the first entry's session-resource
// requirements computed its own. Neither can it tell whether a transferred
// item was read from the list value the cache holds: merge keeps the cache's
// own value for a recipe and moves the bundle's references to it, and the
// Cloud replaces a value in place. Items matched by recipe can therefore
// assemble a list that is neither value.
//
// So a list entry records the item read at each position of its value
// (sharedResult.listItems), and an item read (CallRequest.ListItem) is
// answered only by the item its list recorded there: a lookup has no other
// candidate, and a publication adopts no other entry. A new item registers
// as its recipe's current entry when the recipe has none, and otherwise
// beside it, not indexed, like a value of a session that does not cover the
// current entry's requirements. Sessions that share a list entry share its
// items. The records are not persisted: after a restart, or for a list that
// came by transfer, the first read of each position records a new item. An
// item whose value is replaced in place, as an expired entry nothing uses can
// be, leaves its list's record. An engine replaces a list's value in place
// only when nothing uses it, and every item depends on its list through its
// call, so a list never keeps records while its value is replaced.

// listItemLocked returns the item req's receiver recorded for req.Nth, if it
// can serve: it is registered with a value, its attachment did not fail, and
// it has not expired. Requires egraphMu.
func (c *Cache) listItemLocked(req *CallRequest, nowUnix int64) *sharedResult {
	if req.Receiver == nil {
		return nil
	}
	list := c.resultsByID[sharedResultID(req.Receiver.ResultID)]
	if list == nil {
		return nil
	}
	item := list.listItems[req.Nth]
	if item == nil || c.resultsByID[item.id] != item || item.noValueLocked() ||
		item.attachmentState() == resultAttachmentFailed || c.resultExpiredAtLocked(item, nowUnix) {
		return nil
	}
	return item
}

// lookupMatchForListItemLocked is a list item read's lookup: the item its
// list recorded for the position, or none. The item was published for the
// same frame, so it is a hit on the request's own recipe. Requires egraphMu.
func (c *Cache) lookupMatchForListItemLocked(req *CallRequest, nowUnix int64) lookupMatch {
	match := lookupMatch{
		primaryLookupPossible: true,
		missingInputIndex:     -1,
	}
	if item := c.listItemLocked(req, nowUnix); item != nil {
		match.candidates = set.NewTreeSet(compareSharedResults)
		match.candidates.Insert(item)
		match.hitRecipeDigest = true
		match.route = CacheHitRouteRecipe
	}
	return match
}

// recordListItemLocked records item as the item of req's receiver at
// req.Nth. Requires egraphMu for writing.
func (c *Cache) recordListItemLocked(req *CallRequest, item *sharedResult) {
	list := c.resultsByID[sharedResultID(req.Receiver.ResultID)]
	if list == nil {
		return
	}
	if list.listItems == nil {
		list.listItems = make(map[int64]*sharedResult)
	}
	list.listItems[req.Nth] = item
	item.listItemOf, item.listItemNth = list, req.Nth
}

// forgetListItemLocked drops res from its list's record, as res leaves the
// cache. Requires egraphMu for writing.
func (c *Cache) forgetListItemLocked(res *sharedResult) {
	list := res.listItemOf
	if list == nil {
		return
	}
	if list.listItems[res.listItemNth] == res {
		delete(list.listItems, res.listItemNth)
	}
	res.listItemOf = nil
}
