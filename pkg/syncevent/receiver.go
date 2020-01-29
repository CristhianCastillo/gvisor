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
	"sync/atomic"
)

// Receiver is an event sink that holds pending events and invokes a callback
// whenever new events become pending. Receiver's methods may be called
// concurrently from any number of goroutines.
//
// Receiver.Init() must be called before first use.
type Receiver struct {
	// pending is the set of pending events. pending is accessed using atomic
	// memory operations. pending is a raw uint64 rather than a Set since *Set
	// is not convertible to *uint64 for sync/atomic without package unsafe.
	pending uint64

	// cb is notified when new events become pending. cb is immutable.
	cb ReceiverCallback
}

// ReceiverCallback receives callbacks from a Receiver.
type ReceiverCallback interface {
	// NotifyPending is called when the corresponding Receiver has new pending
	// events.
	//
	// NotifyPending is called synchronously from Receiver.Notify(), so
	// implementations must not take locks that may be held by callers of
	// Receiver.Notify(). NotifyPending may be called concurrently from
	// multiple goroutines.
	NotifyPending()
}

// Init must be called before first use of r.
func (r *Receiver) Init(cb ReceiverCallback) {
	r.cb = cb
}

// Notify sets the given events as pending.
func (r *Receiver) Notify(es Set) {
	es64 := uint64(es)
	for {
		p := atomic.LoadUint64(&r.pending)
		// Optimization: Skip the atomic CAS on r.pending if all events are
		// already pending.
		if p&es64 == es64 {
			return
		}
		// When this is uncontended (the common case), CAS is faster than
		// atomic-OR because the former is inlined and the latter (which we
		// would have to implement in assembly ourselves) is not.
		if atomic.CompareAndSwapUint64(&r.pending, p, p|es64) {
			break
		}
	}
	r.cb.NotifyPending()
}

// Clear unsets the given events as pending.
func (r *Receiver) Clear(es Set) {
	es64 := uint64(es)
	for {
		p := atomic.LoadUint64(&r.pending)
		// Optimization: Skip the atomic CAS on r.pending if all events are
		// already not pending.
		if p&es64 == 0 {
			return
		}
		// When this is uncontended (the common case), CAS is faster than
		// atomic-AND because the former is inlined and the latter (which we
		// would have to implement in assembly ourselves) is not.
		if atomic.CompareAndSwapUint64(&r.pending, p, p&^es64) {
			return
		}
	}
}

// Pending returns the set of pending events.
func (r *Receiver) Pending() Set {
	return Set(atomic.LoadUint64(&r.pending))
}
