/*
Copyright 2011-2017 Frederic Langlet
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
you may obtain a copy of the License at

                http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package entropy

import (
	"kanzi"
)

/////////////////////////////////////////////////////////////////
// APM maps a probability and a context into a new probability
// that bit y will next be 1.  After each guess it updates
// its state to improve future guesses.  Methods:
//
// APM a(N) creates with N contexts, uses 66*N bytes memory.
// a.get(y, pr, cx) returned adjusted probability in context cx (0 to
//   N-1).  rate determines the learning rate (smaller = faster, default 8).
//////////////////////////////////////////////////////////////////
type AdaptiveProbMap struct {
	index int   // last p, context
	rate  uint  // update rate
	data  []int // [NbCtx][33]:  p, context -> p
}

func newAdaptiveProbMap(n, rate uint) (*AdaptiveProbMap, error) {
	this := new(AdaptiveProbMap)
	this.data = make([]int, n*33)
	this.rate = rate
	k := 0

	for i := uint(0); i < n; i++ {
		for j := 0; j < 33; j++ {
			if i == 0 {
				this.data[k+j] = kanzi.Squash((j-16)<<7) << 4
			} else {
				this.data[k+j] = this.data[j]
			}
		}

		k += 33
	}

	return this, nil
}

func (this *AdaptiveProbMap) get(bit int, pr int, ctx int) int {
	// Update probability based on error and learning rate
	g := (bit << 16) + (bit << this.rate) - (bit << 1)
	this.data[this.index] += ((g - this.data[this.index]) >> this.rate)
	this.data[this.index+1] += ((g - this.data[this.index+1]) >> this.rate)
	pr = kanzi.STRETCH[pr]

	// Find new context
	this.index = ((pr + 2048) >> 7) + (ctx << 5) + ctx

	// Return interpolated probability
	w := pr & 127
	return (this.data[this.index]*(128-w) + this.data[this.index+1]*w) >> 11
}
