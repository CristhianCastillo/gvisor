// Copyright 2020 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package syncevent

import (
	"fmt"
	"math/bits"

	"gvisor.dev/gvisor/pkg/sync"
)

// Broadcaster is an implementation of Source that supports any number of
// subscribed Receivers.
//
// The zero value of Broadcaster is valid and has no subscribed Receivers.
// Broadcaster is not copyable by value.
//
// All Broadcaster methods may be called concurrently from multiple goroutines.
type Broadcaster struct {
	// mu protects the following fields.
	mu sync.Mutex

	// load is the number of entries in table with receiver != nil.
	load int

	// If load drops to lowload, attempt to shrink table.
	lowload int

	// Invariants: len(table) is 0 or a power of 2.
	table []broadcasterEntry
}

type broadcasterEntry struct {
	receiver *Receiver
	filter   Set
	id       uint32
}

// SubscribeEvents implements Source.SubscribeEvents.
func (b *Broadcaster) SubscribeEvents(r *Receiver, filter Set) SubscriptionID {
	if r == nil {
		panic("nil Receiver")
	}
	if filter == 0 {
		panic("empty event.Set")
	}

	// Assign a "random" initial ID for this subscription.
	id := receiverBaseID(r)

	// Expand the table if needed to keep its load factor <= 1/2.
	b.mu.Lock()
	b.load++
	if b.load > len(b.table)/2 {
		newlen := 2
		if len(b.table) != 0 {
			newlen = 2 * len(b.table)
		}
		b.expandTableLocked(newlen)
	}

	// Linearly probe for an unused ID / free slot in the table.
	for {
		i := id & uint32((len(b.table) - 1))
		if b.table[i].receiver == nil {
			b.table[i] = broadcasterEntry{
				receiver: r,
				filter:   filter,
				id:       id,
			}
			b.mu.Unlock()
			return SubscriptionID(id)
		}
		id++
	}
}

// UnsubscribeEvents implements Source.UnsubscribeEvents.
func (b *Broadcaster) UnsubscribeEvents(id SubscriptionID) {
	b.mu.Lock()
	i := uint32(id) & uint32(len(b.table)-1)
	if b.table[i].id != uint32(id) {
		b.mu.Unlock()
		panic(fmt.Sprintf("no subscription with ID %d", id))
	}
	b.table[i] = broadcasterEntry{}
	b.load--

	if b.load <= b.lowload {
		// Determine the minimum number of ID bits needed to unambiguously
		// identify a table entry, which constrains the minimum table size. An
		// ID bit is ambiguous if at least two entries have the same value in
		// its position.
		var (
			twice0 uint32
			twice1 uint32
			once0  uint32
			once1  uint32
		)
		for i := range b.table {
			if b.table[i].receiver != nil {
				id := b.table[i].id
				twice0 |= once0 &^ id
				twice1 |= once1 & id
				once0 |= ^id
				once1 |= id
			}
		}
		unambig := ^(twice0 | twice1)
		// The constant below is 4 instead of 2 because we still want to keep
		// the load factor at or below 1/2 in the new table.
		if newlen := 4 << uint(bits.TrailingZeros32(unambig)); newlen < len(b.table) {
			b.shrinkTableLocked(newlen)
		} else {
			// We can't actually shrink the table since doing so would cause
			// entry collisions. Lower b.lowload by 1/4 of its original value
			// (which was len(b.table)/8) and try again in the future. If
			// len(b.table) < 32, b.lowload (< 4) won't change and we'll try
			// again in the next call to UnsubscribeEvents.
			b.lowload -= len(b.table) / 32
		}
	}
	b.mu.Unlock()
}

// Preconditions: b.mu must be locked. newlen > len(b.table).
func (b *Broadcaster) expandTableLocked(newlen int) {
	if newlen <= cap(b.table) {
		// "Un-compact" b.table, reusing the excess capacity.
		oldlen := len(b.table)
		b.table = b.table[:newlen]
		for i := 0; i < oldlen; i++ {
			if b.table[i].receiver != nil {
				j := b.table[i].id & uint32(newlen-1)
				if i != int(j) {
					b.table[j] = b.table[i]
					b.table[i] = broadcasterEntry{}
				}
			}
		}
	} else {
		// Allocate a new table and throw away the old one.
		newtable := make([]broadcasterEntry, newlen, newlen)
		for i := range b.table {
			if b.table[i].receiver != nil {
				j := b.table[i].id & uint32(newlen-1)
				newtable[j] = b.table[i]
			}
		}
		b.table = newtable
	}
	b.resetLowloadLocked()
}

// Preconditions: b.mu must be locked. newlen < len(b.table). newlen must not
// cause entries to collide in the new table.
func (b *Broadcaster) shrinkTableLocked(newlen int) {
	// Compact b.table. Since we're concerned primarily with the cost
	// of Broadcast, not memory usage, we keep the existing slice, but
	// shrink its length.
	for i := newlen; i < len(b.table); i++ {
		if b.table[i].receiver != nil {
			j := b.table[i].id & uint32(newlen-1)
			if b.table[j].receiver != nil {
				panic(fmt.Sprintf("compaction from %d to %d entries causes IDs %#x and %#x to collide", len(b.table), newlen, b.table[j].id, b.table[i].id))
			}
			b.table[j] = b.table[i]
			b.table[i] = broadcasterEntry{}
		}
	}
	b.table = b.table[:newlen]
	b.resetLowloadLocked()
}

// Preconditions: b.mu must be locked.
func (b *Broadcaster) resetLowloadLocked() {
	// Since Broadcast is linear in len(b.table), attempt to shrink the table
	// when b.load drops to 1/8 of len(b.table).
	b.lowload = -1
	if len(b.table) >= 8 {
		b.lowload = len(b.table) / 8
	}
}

// Broadcast notifies all Receivers subscribed to the Broadcaster of the subset
// of events to which they subscribed. The order in which Receivers are
// notified is unspecified.
func (b *Broadcaster) Broadcast(events Set) {
	b.mu.Lock()
	for i := range b.table {
		if b.table[i].receiver != nil {
			if subset := events & b.table[i].filter; subset != 0 {
				b.table[i].receiver.Notify(subset)
			}
		}
	}
	b.mu.Unlock()
}

// Listening returns the set of events for which NotifyEvents will notify at
// least one Receiver, i.e. the union of all filters for subscribed Receivers.
func (b *Broadcaster) Listening() Set {
	var es Set
	b.mu.Lock()
	for i := range b.table {
		es |= b.table[i].filter
	}
	b.mu.Unlock()
	return es
}

// IsEmpty returns true if b has no subscribed Receivers.
func (b *Broadcaster) IsEmpty() bool {
	b.mu.Lock()
	isEmpty := b.load == 0
	b.mu.Unlock()
	return isEmpty
}
