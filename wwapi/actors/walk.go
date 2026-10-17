// Package actors reads the game's live actors: a bounded, validated walk
// of g_fopAcTg_Queue, each actor's fopAc_ac_c fields, and the re-read
// every write rests on. The walk is generic: a caller that needs more of a
// class than fopAc_ac_c (its enemyice, mode or weapon) passes a Layout for
// its proc to ReadWith. Its user today is co-op's combat sync
// (docs/08-coop-design.md §7.2, M6), whose K1 classes, layouts and gates
// live in internal/coop/combat.
//
// Every actor lives on a heap, so nothing here is a static address but
// the queue's: each pointer is checked before it is followed, an actor is
// taken only while its tag says the queue holds it, and an actor that a
// later write rests on is read again just before the write (Reread),
// since its memory can be freed and reused for another actor between two
// reads (fopAcM_delete, then fpcDt_Handler at the next frame's start,
// f_pc_manager.cpp:280).
//
// Citations follow wwapi/addr: file:line is the zeldaret/tww
// decomp at 4d78c994, sym:N is line N of its config/GZLE01/symbols.txt.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package actors

import (
	"encoding/binary"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// MaxNodes is where a walk gives up (§7.2).
const MaxNodes = addr.MaxActorNodes

// Walk is one walk of g_fopAcTg_Queue, a node_list_class of every actor's
// actor_tag (fopAc_Create, f_op_actor.cpp:262-263).
type Walk struct {
	Head, Tail uint32
	Size       int32  // the queue's mSize
	Type       uint32 // g_fopAc_type
	// ProcCounter is the next process ID the game will give
	// (addr.ProcessIDCounter), read before the queue.
	ProcCounter uint32
	Actors      []Actor
	// Stop says why the walk ended before the list did; "" when it
	// reached a null next. A walk that stopped is partial: what it has
	// was checked, but actors past the stop are missing.
	Stop string
	// Notes are inconsistencies that did not stop it: the tail or the
	// size the queue's header gives disagree with the walk.
	Notes []string
}

// Complete reports whether w walked the whole list as its header
// describes it.
func (w *Walk) Complete() bool { return w.Stop == "" && len(w.Notes) == 0 }

// ByPID returns the actor with process ID pid, if w has it.
func (w *Walk) ByPID(pid uint32) (*Actor, bool) {
	for i := range w.Actors {
		if w.Actors[i].PID == pid {
			return &w.Actors[i], true
		}
	}
	return nil, false
}

// ByAddr returns the actor at a, if w has it.
func (w *Walk) ByAddr(a uint32) (*Actor, bool) {
	for i := range w.Actors {
		if w.Actors[i].Addr == a {
			return &w.Actors[i], true
		}
	}
	return nil, false
}

// Read walks g_fopAcTg_Queue as ReadWith does, reading no class beyond
// fopAc_ac_c.
func Read(m mem.Reader, limit int) (Walk, error) {
	return ReadWith(m, limit, nil)
}

// ReadWith walks g_fopAcTg_Queue from head to tail, at most limit nodes (0 or
// less: MaxNodes), and never more than the queue's own mSize. Each node is
// a create_tag_class at actor +0xC4 whose mpTagData is the actor (cTg_Create,
// c_tag.cpp:89-92). Before a node is taken:
//
//   - it lies in MEM1, word-aligned, with room for its actor before it;
//   - its prev is the node before (the list did not change under the walk);
//   - its mpData is &g_fopAcTg_Queue, as cLs_Addition leaves a queued node
//     (c_list.cpp:40), and its mbIsUse is 1 (cTg_Addition, c_tag.cpp:74-80);
//   - its mpTagData is node - 0xC4;
//   - the actor's actor_type is g_fopAc_type (fopAc_IsActor,
//     f_op_actor.cpp:116-119).
//
// The walk stops at the first failure and on a repeated node. A
// g_fopAc_type of 0 means no actor was ever created (f_pc_base.cpp:28-33):
// the walk is empty. The error is a read error; a bad list is reported in
// Stop. Each actor whose proc has a Layout in layouts has that layout
// read too.
func ReadWith(m mem.Reader, limit int, layouts Layouts) (Walk, error) {
	var w Walk
	if limit <= 0 {
		limit = MaxNodes
	}
	var err error
	if w.ProcCounter, err = mem.ReadU32(m, addr.ProcessIDCounter); err != nil {
		return w, err
	}
	q, err := m.Read(addr.ActorQueue, addr.ActorQueueSize)
	if err != nil {
		return w, err
	}
	w.Head = be32(q[addr.ActorQueueOffHead:])
	w.Tail = be32(q[addr.ActorQueueOffTail:])
	w.Size = int32(be32(q[addr.ActorQueueOffSize:]))
	if w.Type, err = mem.ReadU32(m, addr.FopAcType); err != nil {
		return w, err
	}
	if w.Type == 0 {
		if w.Head != 0 || w.Size != 0 {
			w.Stop = "g_fopAc_type is 0, so no actor exists, but the queue has a head or a size"
		}
		return w, nil
	}

	seen := make(map[uint32]bool)
	var prev uint32
	for node := w.Head; node != 0; {
		switch {
		case len(w.Actors) >= limit:
			w.Stop = fmt.Sprintf("stopped at the limit of %d nodes", limit)
		case int32(len(w.Actors)) >= w.Size:
			w.Stop = fmt.Sprintf("the queue's mSize is %d, but the list goes on", w.Size)
		case seen[node]:
			w.Stop = fmt.Sprintf("node 0x%08X comes round again: the list has a cycle", node)
		default:
			w.Stop = NodeBad(node)
			if w.Stop != "" {
				w.Stop = fmt.Sprintf("node 0x%08X %s", node, w.Stop)
			}
		}
		if w.Stop != "" {
			break
		}
		seen[node] = true
		a, tag, err := readNode(m, node)
		if err != nil {
			return w, err
		}
		if p := be32(tag[addr.TagOffPrev:]); p != prev {
			w.Stop = fmt.Sprintf("node 0x%08X has prev 0x%08X, not 0x%08X: the list changed during the walk", node, p, prev)
			break
		}
		if why := tagBad(node, tag); why != "" {
			w.Stop = fmt.Sprintf("node 0x%08X %s", node, why)
			break
		}
		if t := be32(a[addr.ActorOffActorType:]); t != w.Type {
			w.Stop = fmt.Sprintf("actor 0x%08X has actor_type 0x%08X, not g_fopAc_type 0x%08X", node-addr.ActorOffActorTag, t, w.Type)
			break
		}
		act := decode(node, a)
		if l, ok := layouts[act.Proc]; ok {
			if err := readLayout(m, &act, l); err != nil {
				return w, err
			}
		}
		w.Actors = append(w.Actors, act)
		prev, node = node, be32(tag[addr.TagOffNext:])
	}
	if w.Stop == "" {
		if prev != w.Tail {
			w.Notes = append(w.Notes, fmt.Sprintf("the last node is 0x%08X but mpTail is 0x%08X", prev, w.Tail))
		}
		if int32(len(w.Actors)) != w.Size {
			w.Notes = append(w.Notes, fmt.Sprintf("walked %d nodes but mSize is %d", len(w.Actors), w.Size))
		}
	}
	return w, nil
}

// NodeBad says why node cannot be an actor_tag, or "".
func NodeBad(node uint32) string {
	switch {
	case node&3 != 0:
		return "is not word-aligned"
	case mem.CheckRange(node, addr.TagSize) != nil:
		return "is outside MEM1"
	case node < addr.MEM1Base+addr.ActorOffActorTag || mem.CheckRange(node-addr.ActorOffActorTag, addr.ActorSize) != nil:
		return "leaves no room in MEM1 for its actor"
	}
	return ""
}

// tagBad says why tag, read at node, is not a queued actor_tag of the
// actor before it, or "".
func tagBad(node uint32, tag []byte) string {
	actor := node - addr.ActorOffActorTag
	switch {
	case be32(tag[addr.TagOffData:]) != addr.ActorQueue:
		return fmt.Sprintf("has mpData 0x%08X, not &g_fopAcTg_Queue 0x%08X (c_list.cpp:40)", be32(tag[addr.TagOffData:]), addr.ActorQueue)
	case tag[addr.TagOffUse] != 1:
		return fmt.Sprintf("has mbIsUse %d, not 1 (c_tag.cpp:74-80)", int8(tag[addr.TagOffUse]))
	case be32(tag[addr.TagOffTagData:]) != actor:
		return fmt.Sprintf("has mpTagData 0x%08X, not its actor 0x%08X", be32(tag[addr.TagOffTagData:]), actor)
	}
	return ""
}

// readNode reads the actor whose actor_tag is at node, and the tag.
func readNode(m mem.Reader, node uint32) (actor, tag []byte, err error) {
	a, err := m.Read(node-addr.ActorOffActorTag, addr.ActorSize)
	if err != nil {
		return nil, nil, err
	}
	return a, a[addr.ActorOffActorTag : addr.ActorOffActorTag+addr.TagSize], nil
}

func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
func be16(b []byte) uint16 { return binary.BigEndian.Uint16(b) }
