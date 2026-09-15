package workbench

import (
	"bytes"
	"encoding/json"

	"dev.jevido/work/internal/ops"
)

// maxTrackedWrites bounds how many of this replica's writes are remembered for
// the purpose of noticing one being overwritten.
//
// A bound rather than a full history because the useful window is short: a note
// about a line you edited an hour and four hundred edits ago is not a
// conflict, it is archaeology. When the bound is reached the oldest are
// dropped, which costs a note nobody would have read.
const maxTrackedWrites = 512

// writeLog remembers what this replica last wrote to each field, so that a
// later remote write displacing it can be recognised as *this machine's* loss
// rather than as the document merely moving.
//
// The alternative would be to compare actors on the op that displaced it, and
// that is exactly the thing this design does not do. What is compared here is
// the value: the document held what we put there, and now it does not.
type writeLog struct {
	// values is field key to the bytes this replica last wrote there.
	values map[string][]byte
	// order is insertion order, for evicting the oldest.
	order []string
}

func writeKey(node, field string) string { return node + "\x00" + field }

func (w *writeLog) remember(node, field string, data []byte) {
	if w.values == nil {
		w.values = make(map[string][]byte)
	}
	key := writeKey(node, field)
	if _, seen := w.values[key]; !seen {
		w.order = append(w.order, key)
	}
	w.values[key] = bytes.Clone(data)

	for len(w.order) > maxTrackedWrites {
		delete(w.values, w.order[0])
		w.order = w.order[1:]
	}
}

// ours reports whether data is what this replica last wrote to that field, and
// forgets it either way.
//
// Forgetting on a hit is what keeps one disagreement to one note. Forgetting on
// a miss is because a field whose value we no longer recognise has moved on
// past us, and the next thing to happen to it is not ours to claim.
func (w *writeLog) ours(node, field string, data []byte) bool {
	key := writeKey(node, field)
	mine, held := w.values[key]
	if !held {
		return false
	}
	match := bytes.Equal(mine, data)
	if match {
		w.forget(node, field)
	}
	return match
}

func (w *writeLog) forget(node, field string) {
	key := writeKey(node, field)
	if _, held := w.values[key]; !held {
		return
	}
	delete(w.values, key)
	for i, k := range w.order {
		if k == key {
			w.order = append(w.order[:i], w.order[i+1:]...)
			break
		}
	}
}

// track records what a batch of this replica's own ops wrote, and returns the
// conflicts that were apparent the moment they were merged.
//
// Two things come out of one pass because they are two ends of the same
// disagreement. An op of ours that did not take lost to something that arrived
// first, and is a conflict now. An op of ours that did take might lose later,
// and is remembered so that it can be recognised when it does.
func (w *writeLog) track(outcomes []ops.Outcome, values func(node, field string) json.RawMessage) []ConflictEvent {
	var found []ConflictEvent
	for _, outcome := range outcomes {
		if !outcome.Fresh {
			continue
		}
		for _, change := range outcome.Fields {
			if change.Took {
				w.remember(change.Node, change.Field, values(change.Node, change.Field))
				continue
			}
			// Did not take: something newer was already there. The value that
			// stayed is in the report, and what we wanted is what the document
			// would have said.
			found = append(found, ConflictEvent{
				Node:  change.Node,
				Field: change.Field,
				Yours: asText(values(change.Node, change.Field)),
				Now:   asText(change.Was),
			})
		}
	}
	return found
}

// displaced returns the conflicts in a batch of somebody else's ops: the fields
// they overwrote that this replica had written.
func (w *writeLog) displaced(outcomes []ops.Outcome) []ConflictEvent {
	var found []ConflictEvent
	for _, outcome := range outcomes {
		if !outcome.Fresh {
			continue
		}
		for _, change := range outcome.Fields {
			// Took, and displaced something. Whether that something was ours is
			// the only question, and it is answered by the value rather than by
			// anybody's name.
			if !change.Took || len(change.Was) == 0 {
				continue
			}
			if !w.ours(change.Node, change.Field, change.Was) {
				continue
			}
			found = append(found, ConflictEvent{
				Node:  change.Node,
				Field: change.Field,
				Yours: asText(change.Was),
			})
		}
	}
	return found
}

// asText renders a field value for a person to read.
//
// A field is arbitrary JSON, and the ones a person edits are strings. Anything
// else is shown as the JSON it is, which is worse to read and better than
// pretending a number was text.
func asText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// wanted is the value an op wrote to a field, for reporting what a losing write
// was trying to say. The merge does not keep it -- it lost -- so it is read back
// off the op.
func wanted(batch []ops.Op, node, field string) json.RawMessage {
	for _, op := range batch {
		target := op.Node
		if op.Kind == ops.KindExtractToTask {
			target = op.Task
		}
		if target != node {
			continue
		}
		if value, ok := op.Fields[field]; ok {
			return value
		}
	}
	return nil
}
