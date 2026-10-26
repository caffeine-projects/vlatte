package fault

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

var (
	_ vlatte.Step                = slicer{}
	_ descriptor.SelfDescriptive = slicer{}
	_ vlatte.Stage               = (*slicerStage)(nil)
)

// Slicer returns a step that slices each write into flushed slices of averageSize bytes, give or take sizeVariation,
// at random, and waits delay before each slice, to test a client that takes one read for a whole message: the client
// reads the answer in many small reads. The bytes, and their order, don't change. The writer waits for its slices.
// TCP can join slices that come close together: a delay keeps them apart.
//
// It goes with [github.com/caffeine-projects/vlatte/mockhttp.Builder.Step], to slice the body, or with
// [github.com/caffeine-projects/vlatte/mockhttp.Builder.WireStep], to slice the whole answer, its head included. It
// panics when averageSize is less than 1, sizeVariation is negative or not less than averageSize, or delay is
// negative.
func Slicer(averageSize, sizeVariation int, delay time.Duration) vlatte.Step {
	if averageSize < 1 {
		panic(fmt.Sprintf("fault: slicer needs slices of a byte at least, not %d", averageSize))
	}

	if sizeVariation < 0 || sizeVariation >= averageSize {
		panic(fmt.Sprintf("fault: slicer size variation must be from 0 to %d, not %d", averageSize-1, sizeVariation))
	}

	if delay < 0 {
		panic(fmt.Sprintf("fault: slicer can't wait %s", delay))
	}

	return slicer{size: averageSize, variation: sizeVariation, delay: delay}
}

type slicer struct {
	size      int
	variation int
	delay     time.Duration
}

// Describe implements [descriptor.SelfDescriptive], without a size variation or a delay of 0.
func (sl slicer) Describe() descriptor.Descriptor {
	props := []any{"type", "slicer", "average_size", sl.size}
	if sl.variation != 0 {
		props = append(props, "size_variation", sl.variation)
	}

	if sl.delay != 0 {
		props = append(props, "delay", sl.delay.String())
	}

	return descriptor.WithKeyValuePairs("slicer", props...)
}

func (sl slicer) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return &slicerStage{ctx: flow.Context(), slicer: sl, next: next}
}

// slicerStage passes each write on in slices.
type slicerStage struct {
	ctx    context.Context
	slicer slicer
	next   vlatte.Stage
}

func (s *slicerStage) Write(b []byte) (int, error) {
	for sent := 0; sent < len(b); {
		end := min(len(b), sent+s.slicer.size-s.slicer.variation+rand.IntN(2*s.slicer.variation+1))

		if err := waitUntil(s.ctx, time.Now().Add(s.slicer.delay)); err != nil {
			return sent, err
		}

		if _, err := s.next.Write(b[sent:end]); err != nil {
			return sent, err
		}

		if err := s.next.Flush(); err != nil {
			return end, err
		}

		sent = end
	}

	return len(b), nil
}

func (s *slicerStage) Flush() error { return s.next.Flush() }

func (s *slicerStage) Close() error { return s.next.Close() }
