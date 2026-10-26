package fault

import (
	"context"
	"fmt"
	"math"
	"math/bits"
	"time"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ vlatte.Step  = bandwidth{}
	_ vlatte.Stage = (*bandwidthStage)(nil)
)

// _kbps is a kilobit per second, in bytes per second.
const _kbps = 1000 / 8

// _bandwidthPieces is how many pieces [Bandwidth] sends a second.
const _bandwidthPieces = 10

// Common bandwidths, in bytes per second, for [Bandwidth]: those of the network throttling presets of browser
// developer tools, and of other common links.
const (
	BandwidthGPRS   = 50 * _kbps     // 50 kbit/s, GPRS
	BandwidthDialUp = 56 * _kbps     // 56 kbit/s, a dial-up modem
	Bandwidth2G     = 250 * _kbps    // 250 kbit/s, a regular 2G (EDGE) link
	BandwidthGood2G = 450 * _kbps    // 450 kbit/s, a good 2G link
	Bandwidth3G     = 750 * _kbps    // 750 kbit/s, a regular 3G link
	BandwidthGood3G = 1500 * _kbps   // 1.5 Mbit/s, a good 3G link
	BandwidthDSL    = 2000 * _kbps   // 2 Mbit/s, DSL
	Bandwidth4G     = 4000 * _kbps   // 4 Mbit/s, a regular 4G link
	BandwidthWiFi   = 30000 * _kbps  // 30 Mbit/s, Wi-Fi
	BandwidthFiber  = 100000 * _kbps // 100 Mbit/s, fiber
)

// Bandwidth returns a step that limits what passes to bytesPerSecond, such as [Bandwidth3G], to test a client against
// a slow link: it sends each write in flushed pieces of a tenth of a second's worth of bytes, each once the time its
// bytes take at that rate passed since the write began, so a write of n bytes ends n/bytesPerSecond seconds later.
// The writer waits for it, as it would on a slow connection. The time between writes isn't counted: a write never
// goes faster for an earlier pause.
//
// It goes with [github.com/caffeine-projects/vlatte/mockhttp.Builder.Step], to slow the body, or with
// [github.com/caffeine-projects/vlatte/mockhttp.Builder.WireStep], to slow the whole answer. It panics when
// bytesPerSecond is less than 1.
func Bandwidth(bytesPerSecond int) vlatte.Step {
	if bytesPerSecond < 1 {
		panic(fmt.Sprintf("fault: bandwidth needs a byte per second at least, not %d", bytesPerSecond))
	}

	return bandwidth{rate: bytesPerSecond}
}

type bandwidth struct{ rate int }

func (b bandwidth) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return &bandwidthStage{ctx: flow.Context(), rate: b.rate, next: next}
}

// bandwidthStage passes each write on in pieces, at its rate.
type bandwidthStage struct {
	ctx  context.Context
	rate int
	next vlatte.Stage
}

func (s *bandwidthStage) Write(b []byte) (int, error) {
	start := time.Now()
	piece := max(1, s.rate/_bandwidthPieces)

	for sent := 0; sent < len(b); {
		end := min(len(b), sent+piece)

		if err := waitUntil(s.ctx, start.Add(sendTime(end, s.rate))); err != nil {
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

func (s *bandwidthStage) Flush() error { return s.next.Flush() }

func (s *bandwidthStage) Close() error { return s.next.Close() }

// sendTime is how long n bytes take at rate bytes a second, in 128 bits so a huge write doesn't overflow: a time
// longer than a duration holds is the longest one.
func sendTime(n, rate int) time.Duration {
	hi, lo := bits.Mul64(uint64(n), uint64(time.Second))
	if hi >= uint64(rate) {
		return math.MaxInt64
	}

	q, _ := bits.Div64(hi, lo, uint64(rate))

	return time.Duration(min(q, math.MaxInt64))
}
