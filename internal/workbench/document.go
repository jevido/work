package workbench

import (
	"encoding/json"
	"fmt"
	"strings"

	"dev.jevido/work/internal/board"
	"dev.jevido/work/internal/ops"
)

// The workspace document is a node tree, and this is the shape Work puts in
// it.
//
// A tab is a root node. The cards a machine's board holds are its children.
// That is the whole schema, and it is the board rather than the transcript on
// purpose: the board is the durable answer to "what is being worked on", it is
// small, and it is the thing a second person actually needs. Streamed text and
// tool calls are a live view of one machine's run -- replaying them out of a
// log an hour later would tell a colleague nothing they want.
const (
	// FieldKind distinguishes a tab node from a card node, so a viewer can
	// read the tree without inferring anything from its shape.
	FieldKind = "kind"
	// KindTab and KindCard are the values FieldKind takes.
	KindTab  = "tab"
	KindCard = "card"

	// FieldName is a tab's label.
	FieldName = "name"

	// Card fields. These mirror board.Card, one field per column, because
	// fields merge one at a time: two people editing the title and the status
	// of the same card both keep their edit.
	FieldTitle   = "title"
	FieldStatus  = "status"
	FieldAgentID = "agentId"
	FieldRunID   = "runId"
	FieldNote    = "note"
	// FieldOrigin is the actor whose board the card came from. Two people
	// each running their own agents produce two sets of cards in one tab, and
	// this is what says which is whose.
	FieldOrigin = "origin"
)

// cardNode is the document node ID for one of this machine's board cards.
//
// Board IDs are per-machine and tiny -- "T1", "T2" -- so two people would
// collide on the first card each of them made. Namespacing by actor removes
// that without any coordination, and it does not make the node private: any
// replica may write to any node, the prefix only decides who mints the ID.
func cardNode(actor, cardID string) string {
	return "c_" + actor + "_" + cardID
}

// position sorts a card among its siblings.
//
// Positions are compared as strings by the merge, so a plain decimal index
// would order card 10 before card 2. Zero-padding fixes that and keeps the
// key insertable: a client that later needs a card between two neighbours can
// append to the lower one's key rather than renumbering the board.
func position(i int) string {
	return fmt.Sprintf("%06d", i)
}

// boardOps is the diff between what this machine's board says and what the
// merged document already holds, expressed as ops.
//
// It is a diff and not a snapshot, and that is the point. publishBoard runs
// several times per run -- a card added, a card moved to doing, a card moved
// to done -- and most of those change one field of one card. Sending the whole
// board each time would put a full re-encode of every card into the log on
// every status change, and every replica would have to merge it. So a status
// change costs one op with one field, and a publish that changed nothing costs
// no ops at all and never reaches the disk.
//
// The cost is O(cards) per publish against a board that holds a handful.
//
// The document can move between the diff and the apply -- the sync loop
// merges a peer's page on its own goroutine -- and that is left alone rather
// than locked out. The worst it produces is an op that restates something a
// colleague just wrote, and last-write-wins settles that the same way it
// settles every other concurrent edit. Holding the state lock across the
// outbox fsync to close a window that resolves correctly anyway would trade a
// real cost for an imaginary one.
func boardOps(s *Sync, tab string, cards []board.Card) ([]ops.Op, error) {
	if tab == "" {
		// Cards belong to a tab. With none active there is nothing to attach
		// them to, and inventing a root would put them somewhere no peer
		// would look.
		return nil, nil
	}

	planned, err := planBoard(s, tab, cards)
	if err != nil || len(planned) == 0 {
		return nil, err
	}

	// Clocks are reserved once for the whole batch, in order, so two ops in
	// one publish that touch the same field are decided by their order here
	// rather than arbitrarily. See Sync.reserveClocks.
	clock := s.reserveClocks(len(planned))
	out := make([]ops.Op, 0, len(planned))
	for i, p := range planned {
		p.Actor = s.actor
		p.Clock = clock + uint64(i)
		id, err := mintID()
		if err != nil {
			return nil, err
		}
		p.ID = id
		out = append(out, p)
	}
	return out, nil
}

// planBoard works out which ops the board needs, without minting IDs or
// clocks. Split out so it can be reasoned about -- and tested -- as a pure
// diff, with the parts that have to be unique added afterwards.
func planBoard(s *Sync, tab string, cards []board.Card) ([]ops.Op, error) {
	var out []ops.Op

	// The tab itself has to exist before anything hangs off it. A peer that
	// created the tab has already said this; the op is skipped in that case
	// rather than sent and merged to no effect.
	if node, ok := s.node(tab); !ok {
		fields, err := jsonFields(map[string]any{
			FieldKind: KindTab,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, ops.Op{Kind: ops.KindCreateNode, Node: tab, Fields: fields})
	} else if node.Deleted {
		// The tab was retired while this machine still had it open. A
		// tombstone is permanent, so there is nothing to write here -- and
		// writing cards under it would produce nodes no tree can reach.
		return nil, nil
	}

	live := make(map[string]struct{}, len(cards))

	for i, card := range cards {
		id := cardNode(s.actor, card.ID)
		live[id] = struct{}{}

		want := map[string]any{
			FieldKind:   KindCard,
			FieldTitle:  card.Title,
			FieldStatus: string(card.Status),
			FieldOrigin: s.actor,
		}
		// Optional columns are written only when set. An absent field and a
		// field set to "" are different things in a last-write-wins merge:
		// the first says nothing, the second says "it is empty now".
		if card.AgentID != "" {
			want[FieldAgentID] = card.AgentID
		}
		if card.RunID != "" {
			want[FieldRunID] = card.RunID
		}
		if card.Note != "" {
			want[FieldNote] = card.Note
		}

		node, exists := s.node(id)
		switch {
		case !exists:
			fields, err := jsonFields(want)
			if err != nil {
				return nil, err
			}
			out = append(out, ops.Op{
				Kind:     ops.KindCreateNode,
				Node:     id,
				Parent:   tab,
				Position: position(i),
				Fields:   fields,
			})
			continue
		case node.Deleted:
			// Deleting is permanent. A card that comes back with the same
			// board ID after a tombstone is a new card, and pretending
			// otherwise would write ops that merge into nothing.
			continue
		}

		changed, err := changedFields(node, want)
		if err != nil {
			return nil, err
		}
		if len(changed) > 0 {
			out = append(out, ops.Op{Kind: ops.KindSetFields, Node: id, Fields: changed})
		}
		if node.Parent != tab || node.Position != position(i) {
			out = append(out, ops.Op{
				Kind:     ops.KindMoveNode,
				Node:     id,
				Parent:   tab,
				Position: position(i),
			})
		}
	}

	out = append(out, retiredCards(s, tab, live)...)
	return out, nil
}

// retiredCards tombstones this machine's cards that are no longer on its
// board -- what ClearConversation leaves behind.
//
// Only this machine's own cards. A colleague's cards live in the same tab and
// are not on this board, and deleting them because they are missing from a
// board that never had them would let one person's "clear" wipe another's
// work.
func retiredCards(s *Sync, tab string, live map[string]struct{}) []ops.Op {
	prefix := "c_" + s.actor + "_"

	var out []ops.Op
	for _, node := range s.liveNodes() {
		if node.Parent != tab || !strings.HasPrefix(node.ID, prefix) {
			continue
		}
		if _, ok := live[node.ID]; ok {
			continue
		}
		out = append(out, ops.Op{Kind: ops.KindDeleteNode, Node: node.ID})
	}
	return out
}

// changedFields returns the subset of want that differs from what the node
// already holds.
//
// Comparing encoded bytes rather than decoding what is there is both cheaper
// and stricter: json.Marshal of a Go string is canonical, so two equal strings
// always produce equal bytes, and anything that does not match is genuinely a
// write. Skipping the ones that match is what keeps a status change to one
// field instead of five.
func changedFields(node ops.Node, want map[string]any) (map[string]json.RawMessage, error) {
	var out map[string]json.RawMessage
	for name, value := range want {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("workbench: encode field %s: %w", name, err)
		}
		if cur, ok := node.Fields[name]; ok && string(cur) == string(encoded) {
			continue
		}
		if out == nil {
			out = make(map[string]json.RawMessage, len(want))
		}
		out[name] = encoded
	}
	return out, nil
}

// jsonFields encodes a field map for an op.
func jsonFields(in map[string]any) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(in))
	for name, value := range in {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("workbench: encode field %s: %w", name, err)
		}
		out[name] = encoded
	}
	return out, nil
}

// tabOps describes a tab in the document: created, renamed or retired.
func tabOps(s *Sync, id, name string, closed bool) ([]ops.Op, error) {
	node, exists := s.node(id)

	var planned []ops.Op
	switch {
	case closed:
		if !exists || node.Deleted {
			return nil, nil
		}
		planned = append(planned, ops.Op{Kind: ops.KindDeleteNode, Node: id})
	case !exists:
		fields, err := jsonFields(map[string]any{FieldKind: KindTab, FieldName: name})
		if err != nil {
			return nil, err
		}
		planned = append(planned, ops.Op{Kind: ops.KindCreateNode, Node: id, Fields: fields})
	case node.Deleted:
		return nil, nil
	default:
		changed, err := changedFields(node, map[string]any{FieldKind: KindTab, FieldName: name})
		if err != nil {
			return nil, err
		}
		if len(changed) == 0 {
			return nil, nil
		}
		planned = append(planned, ops.Op{Kind: ops.KindSetFields, Node: id, Fields: changed})
	}

	clock := s.reserveClocks(len(planned))
	for i := range planned {
		opID, err := mintID()
		if err != nil {
			return nil, err
		}
		planned[i].ID = opID
		planned[i].Actor = s.actor
		planned[i].Clock = clock + uint64(i)
	}
	return planned, nil
}

// documentTabs reads the tabs out of the merged document.
//
// This is how a machine learns about a tab a colleague made: it is in the
// tree, so it is in here, and the workbench adopts it unbound. Nothing else
// about a tab crosses the wire -- where its project lives is per-machine, and
// see config.Tab for why.
func documentTabs(s *Sync) map[string]string {
	out := make(map[string]string)
	for _, node := range s.liveNodes() {
		if node.Parent != "" || fieldString(node, FieldKind) != KindTab {
			continue
		}
		out[node.ID] = fieldString(node, FieldName)
	}
	return out
}

// fieldString reads a string field, or empty if it is absent or not a string.
// Fields are arbitrary JSON written by any replica, including older and newer
// releases, so the type is checked rather than assumed.
func fieldString(node ops.Node, name string) string {
	raw, ok := node.Fields[name]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// cardsFromDocument turns a tab's nodes back into board cards, for showing
// what other machines are doing.
//
// The result is deliberately read-only. It is not merged into the local
// board: the local board is what this machine's runs are driving, and folding
// a colleague's cards into it would mean the workbench trying to schedule work
// it is not running.
func cardsFromDocument(doc Document, tab string) []board.Card {
	var out []board.Card
	for _, node := range doc.Tree {
		if node.ID != tab {
			continue
		}
		for _, child := range node.Children {
			if fieldString(child.Node, FieldKind) != KindCard {
				continue
			}
			out = append(out, board.Card{
				ID:      child.ID,
				RunID:   fieldString(child.Node, FieldRunID),
				AgentID: fieldString(child.Node, FieldAgentID),
				Title:   fieldString(child.Node, FieldTitle),
				Note:    fieldString(child.Node, FieldNote),
				Status:  board.Status(fieldString(child.Node, FieldStatus)),
			})
		}
	}
	return out
}
