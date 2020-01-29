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

// +build go1.11
// +build !go1.15

// Check go:linkname function signatures when updating Go version.

package syncevent

import (
	"sync/atomic"
	"unsafe"
)

// Waiter allows a goroutine to block on pending events received by a Receiver.
//
// Waiter.Init() must be called before first use.
type Waiter struct {
	r Receiver

	// g is one of:
	//
	// - nil: No goroutine is blocking in Wait.
	//
	// - &preparingG: A goroutine is in Wait and has determined that no events
	// it is waiting for are pending, but hasn't yet completed receiverSleep.
	// Thus the wait can only be interrupted by replacing the value of g with
	// nil (the G may not be in state Gwaiting yet, so we can't call goready.)
	//
	// - Otherwise: g is a pointer to the runtime.g in state Gwaiting for the
	// goroutine blocked in Wait, which can only be woken by calling goready.
	g unsafe.Pointer `state:"zerovalue"`
}

// Sentinel object for Waiter.g.
var preparingG struct{}

// Init must be called before first use of w.
func (w *Waiter) Init() {
	w.r.Init(w)
}

// Receiver returns the Receiver that receives events that unblock calls to
// w.Wait().
func (w *Waiter) Receiver() *Receiver {
	return &w.r
}

// Pending returns the set of pending events.
func (w *Waiter) Pending() Set {
	return w.r.Pending()
}

// Wait blocks until at least one event in es is pending, then returns the set
// of pending events (including events not in es). It does not affect the set
// of pending events; instead, callers must call w.Ack().
func (w *Waiter) Wait(es Set) Set {
	for {
		// Optimization: Skip the atomic store to w.g if an event is already
		// pending.
		if p := w.r.Pending(); p&es != 0 {
			return p
		}

		// Indicate that we're preparing to go to sleep.
		atomic.StorePointer(&w.g, (unsafe.Pointer)(&preparingG))

		// If an event is pending, abort the sleep.
		if p := w.r.Pending(); p&es != 0 {
			atomic.StorePointer(&w.g, nil)
			return p
		}

		// If w.g is still preparingG (i.e. w.NotifyPending() has not been
		// called or has not reached atomic.SwapPointer()), go to sleep until
		// w.NotifyPending() => goready().
		const (
			waitReasonSelect     = 9  // Go: src/runtime/runtime2.go
			traceEvGoBlockSelect = 24 // Go: src/runtime/trace.go
		)
		gopark(waiterUnlock, &w.g, waitReasonSelect, traceEvGoBlockSelect, 0)
	}
}

// Ack marks the given events as not pending.
func (w *Waiter) Ack(es Set) {
	w.r.Clear(es)
}

// NotifyPending implements ReceiverCallback.NotifyPending.
func (w *Waiter) NotifyPending() {
	// Optimization: Skip the atomic swap on w.g if there is no sleeping
	// goroutine.
	if atomic.LoadPointer(&w.g) == nil {
		return
	}
	// Wake a sleeping G, or prevent a G that is preparing to sleep from doing
	// so. Swap is needed here to ensure that only one call to Notify calls
	// goready.
	if g := atomic.SwapPointer(&w.g, nil); g != nil && g != (unsafe.Pointer)(&preparingG) {
		goready(g, 0)
	}
}

//go:linkname gopark runtime.gopark
func gopark(unlockf func(unsafe.Pointer, *unsafe.Pointer) bool, wg *unsafe.Pointer, reason uint8, traceEv byte, traceskip int)

//go:linkname goready runtime.goready
func goready(g unsafe.Pointer, traceskip int)
