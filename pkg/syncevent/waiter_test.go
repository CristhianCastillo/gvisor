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
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/sleep"
)

func TestWaiterAlreadyPending(t *testing.T) {
	var w Waiter
	w.Init()
	want := Set(1)
	w.Receiver().Notify(want)
	if got := w.Wait(AllEvents); got != want {
		t.Errorf("Waiter.Wait: got %#x, wanted %#x", got, want)
	}
}

func TestWaiterAsyncNotify(t *testing.T) {
	var w Waiter
	w.Init()
	want := Set(1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		w.Receiver().Notify(want)
	}()
	if got := w.Wait(AllEvents); got != want {
		t.Errorf("Waiter.Wait: got %#x, wanted %#x", got, want)
	}
}

func TestWaiterWaitFiltered(t *testing.T) {
	var w Waiter
	w.Init()
	evWaited := Set(1)
	evOther := Set(2)
	w.Receiver().Notify(evOther)
	notifiedEvent := uint32(0)
	go func() {
		time.Sleep(100 * time.Millisecond)
		atomic.StoreUint32(&notifiedEvent, 1)
		w.Receiver().Notify(evWaited)
	}()
	if got, want := w.Wait(evWaited), evWaited|evOther; got != want {
		t.Errorf("Waiter.Wait: got %#x, wanted %#x", got, want)
	}
	if atomic.LoadUint32(&notifiedEvent) == 0 {
		t.Errorf("Waiter.Wait returned before goroutine notified waited-for event")
	}
}

// BenchmarkWaiterX, BenchmarkSleeperX, and BenchmarkChannelX benchmark usage
// pattern X (described in terms of Waiter) with Waiter, sleep.Sleeper, and
// buffered chan struct{} respectively. When the maximum number of event
// sources is relevant, we use 3 event sources because this is representative
// of the kernel.Task.block() use case: an interrupt source, a timeout source,
// and the actual event source being waited on.

// Event set used by most benchmarks.
const evBench Set = 1

// Benchmark*InitNotify measures how long it takes to construct the
// minimum required state to notify a Waiter of an event, then do so.
//
// Since Waiter and sleep.Waker are trivial to construct, this is a reasonable
// approximation of the cost of a non-redundant call to
// Waiter.Receiver().Notify() and sleep.Waker.Assert() respectively.

func BenchmarkWaiterInitNotify(b *testing.B) {
	var w Waiter

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w = Waiter{}
		w.Init()
		w.Receiver().Notify(evBench)
	}
}

func BenchmarkSleeperInitNotify(b *testing.B) {
	var w sleep.Waker

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w = sleep.Waker{}
		w.Assert()
	}
}

func BenchmarkChannelInitNotify(b *testing.B) {
	var ch chan struct{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch = make(chan struct{}, 1)
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Benchmark*NotifyRedundant measures how long it takes to notify a Waiter of
// an event that is already pending.

func BenchmarkWaiterNotifyRedundant(b *testing.B) {
	var w Waiter
	w.Init()
	r := w.Receiver()
	r.Notify(evBench)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Notify(evBench)
	}
}

func BenchmarkSleeperNotifyRedundant(b *testing.B) {
	var s sleep.Sleeper
	var w sleep.Waker
	s.AddWaker(&w, 0)
	w.Assert()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Assert()
	}
}

func BenchmarkChannelNotifyRedundant(b *testing.B) {
	ch := make(chan struct{}, 1)
	ch <- struct{}{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Benchmark*NotifyWaitAck measures how long it takes to notify a Waiter an
// event, return that event using a blocking check, and then unset the event as
// pending.

func BenchmarkWaiterNotifyWaitAck(b *testing.B) {
	var w Waiter
	w.Init()
	r := w.Receiver()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Notify(evBench)
		w.Wait(evBench)
		w.Ack(evBench)
	}
}

func BenchmarkSleeperNotifyWaitAck(b *testing.B) {
	var s sleep.Sleeper
	var w sleep.Waker
	s.AddWaker(&w, 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Assert()
		s.Fetch(true)
	}
}

func BenchmarkChannelNotifyWaitAck(b *testing.B) {
	ch := make(chan struct{}, 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// notify
		select {
		case ch <- struct{}{}:
		default:
		}

		// wait + ack
		<-ch
	}
}

// Benchmark*NotifyWaitMultiAck is equivalent to NotifyWaitAck, but allows for
// multiple event sources.

func BenchmarkWaiterNotifyWaitMultiAck(b *testing.B) {
	var w Waiter
	w.Init()
	r := w.Receiver()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Notify(evBench)
		if e := w.Wait(AllEvents); e != evBench {
			b.Fatalf("Wait: got %#x, wanted %#x", e, evBench)
		}
		w.Ack(evBench)
	}
}

func BenchmarkSleeperNotifyWaitMultiAck(b *testing.B) {
	var s sleep.Sleeper
	var ws [3]sleep.Waker
	for i := range ws {
		s.AddWaker(&ws[i], i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ws[0].Assert()
		if id, _ := s.Fetch(true); id != 0 {
			b.Fatalf("Fetch: got %d, wanted 0", id)
		}
	}
}

func BenchmarkChannelNotifyWaitMultiAck(b *testing.B) {
	ch0 := make(chan struct{}, 1)
	ch1 := make(chan struct{}, 1)
	ch2 := make(chan struct{}, 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// notify
		select {
		case ch0 <- struct{}{}:
		default:
		}

		// wait + clear
		select {
		case <-ch0:
			// ok
		case <-ch1:
			b.Fatalf("received from ch1")
		case <-ch2:
			b.Fatalf("received from ch2")
		}
	}
}

// Benchmark*NotifyAsyncWaitAck measures how long it takes to wait for an event
// while another goroutine signals the event. This assumes that a new goroutine
// doesn't run immediately (i.e. the creator of a new goroutine is allowed to
// go to sleep before the new goroutine has a chance to run).

func BenchmarkWaiterNotifyAsyncWaitAck(b *testing.B) {
	var w Waiter
	w.Init()
	r := w.Receiver()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		go func() {
			r.Notify(1)
		}()
		w.Wait(evBench)
		w.Ack(evBench)
	}
}

func BenchmarkSleeperNotifyAsyncWaitAck(b *testing.B) {
	var s sleep.Sleeper
	var w sleep.Waker
	s.AddWaker(&w, 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		go func() {
			w.Assert()
		}()
		s.Fetch(true)
	}
}

func BenchmarkChannelNotifyAsyncWaitAck(b *testing.B) {
	ch := make(chan struct{}, 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		go func() {
			select {
			case ch <- struct{}{}:
			default:
			}
		}()
		<-ch
	}
}

// Benchmark*NotifyAsyncWaitMultiAck is equivalent to NotifyAsyncWaitAck, but
// allows for multiple event sources.

func BenchmarkWaiterNotifyAsyncWaitMultiAck(b *testing.B) {
	var w Waiter
	w.Init()
	r := w.Receiver()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		go func() {
			r.Notify(evBench)
		}()
		if e := w.Wait(AllEvents); e != evBench {
			b.Fatalf("Wait: got %#x, wanted %#x", e, evBench)
		}
		w.Ack(evBench)
	}
}

func BenchmarkSleeperNotifyAsyncWaitMultiAck(b *testing.B) {
	var s sleep.Sleeper
	var ws [3]sleep.Waker
	for i := range ws {
		s.AddWaker(&ws[i], i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		go func() {
			ws[0].Assert()
		}()
		if id, _ := s.Fetch(true); id != 0 {
			b.Fatalf("Fetch: got %d, expected 0", id)
		}
	}
}

func BenchmarkChannelNotifyAsyncWaitMultiAck(b *testing.B) {
	ch0 := make(chan struct{}, 1)
	ch1 := make(chan struct{}, 1)
	ch2 := make(chan struct{}, 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		go func() {
			select {
			case ch0 <- struct{}{}:
			default:
			}
		}()

		select {
		case <-ch0:
			// ok
		case <-ch1:
			b.Fatalf("received from ch1")
		case <-ch2:
			b.Fatalf("received from ch2")
		}
	}
}
