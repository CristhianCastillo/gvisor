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
	"unsafe"
)

func receiverBaseID(r *Receiver) uint32 {
	// This "hash function" is the linear congruential generator defined as
	// nrand48() by POSIX, with the constant addition removed (since it almost
	// never affects the output due to the bit shift).
	//
	// receiverBaseID is essentially a random number generator seeded by the
	// Receiver's address (so that it doesn't require the enormous amount of
	// space required by math.Rand.Source), so it doesn't matter if r is
	// relocated by a future GC.
	addr := uintptr((unsafe.Pointer)(&r))
	h := (uint64(addr) * 0x5deece66d) >> 16
	return uint32(h)
}
