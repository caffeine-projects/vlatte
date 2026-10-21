package vlatte

import (
	"slices"
)

// postings index the entries of a journal by a field: each value maps to the sequence numbers of the entries that
// have it, in order. Keys holds the values in the order they came. A list, and keys, only grow at their end, and are
// replaced, never changed, when they shrink: a query reads what it copied of them without the journal's lock.
type postings[K comparable] struct {
	lists map[K][]uint64
	keys  []K
}

func newPostings[K comparable]() postings[K] {
	return postings[K]{lists: make(map[K][]uint64)}
}

func (p *postings[K]) add(k K, seq uint64) {
	list, ok := p.lists[k]
	if !ok {
		p.keys = append(p.keys, k)
	}

	p.lists[k] = append(list, seq)
}

// values returns the values of p, for a query to match once the journal's lock is released.
func (p *postings[K]) values() []K {
	return slices.Clip(p.keys)
}

// of returns the lists of values, for a query to read once the journal's lock is released. A value p no longer has,
// one whose entries the journal dropped meanwhile, has none.
func (p *postings[K]) of(values []K) [][]uint64 {
	lists := make([][]uint64, 0, len(values))

	for _, k := range values {
		if list, ok := p.lists[k]; ok {
			lists = append(lists, slices.Clip(list))
		}
	}

	return lists
}

// compact drops the sequence numbers below first, those of the entries the journal dropped, in new lists, and the
// values left with none.
func (p *postings[K]) compact(first uint64) {
	gone := false

	for k, list := range p.lists {
		i, _ := slices.BinarySearch(list, first)

		switch {
		case i == len(list):
			delete(p.lists, k)

			gone = true
		case i > 0:
			p.lists[k] = slices.Clone(list[i:])
		}
	}

	if gone {
		keys := make([]K, 0, len(p.lists))

		for _, k := range p.keys {
			if _, ok := p.lists[k]; ok {
				keys = append(keys, k)
			}
		}

		p.keys = keys
	}
}

// journalIndex indexes the entries of a journal by the fields queries filter most: those with few values, and the
// path. Its lists may still hold entries the journal dropped, until compact runs: a query skips them.
type journalIndex struct {
	kind    postings[Kind]
	outcome postings[Outcome]
	mockID  postings[string]
	method  postings[string]
	path    postings[string]
	status  postings[int]

	// ids maps the ID of each entry the journal holds to its sequence number.
	ids map[string]uint64
}

func newJournalIndex() journalIndex {
	return journalIndex{
		kind:    newPostings[Kind](),
		outcome: newPostings[Outcome](),
		mockID:  newPostings[string](),
		method:  newPostings[string](),
		path:    newPostings[string](),
		status:  newPostings[int](),
		ids:     make(map[string]uint64),
	}
}

func (x *journalIndex) add(row *journalRow) {
	x.kind.add(row.Kind, row.Seq)
	x.outcome.add(row.Outcome, row.Seq)
	x.mockID.add(row.MockID, row.Seq)
	x.method.add(row.Method, row.Seq)
	x.path.add(row.Path, row.Seq)
	x.status.add(row.Status, row.Seq)

	if row.ID != "" {
		x.ids[row.ID] = row.Seq
	}
}

// drop forgets the IDs of rows, entries the journal dropped. Their sequence numbers stay in the lists until compact.
func (x *journalIndex) drop(rows []journalRow) {
	for i := range rows {
		if x.ids[rows[i].ID] == rows[i].Seq {
			delete(x.ids, rows[i].ID)
		}
	}
}

func (x *journalIndex) compact(first uint64) {
	x.kind.compact(first)
	x.outcome.compact(first)
	x.mockID.compact(first)
	x.method.compact(first)
	x.path.compact(first)
	x.status.compact(first)
}
