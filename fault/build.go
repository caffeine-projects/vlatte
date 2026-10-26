package fault

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
)

// The names of the faults in mock files.
const (
	_fEmptyResponse              = "empty_response"
	_fMalformedResponseChunk     = "malformed_response_chunk"
	_fRandomDataThenClose        = "random_data_then_close"
	_fConnectionResetByPeer      = "connection_reset_by_peer"
	_fConnectionResetByPeerAfter = "connection_reset_by_peer_after"
	_fChunkedDribbleDelay        = "chunked_dribble_delay"
	_fLatency                    = "latency"
	_fBandwidth                  = "bandwidth"
	_fSlicer                     = "slicer"
	_fSlowClose                  = "slow_close"
	_fTimeout                    = "timeout"
	_fLimitData                  = "limit_data"
)

// _maxMilliseconds is the most milliseconds a time.Duration holds.
const _maxMilliseconds = math.MaxInt64 / int64(time.Millisecond)

// _specExample is a fault spec, for errors.
const _specExample = `["latency", "100ms"]`

// _durationWant says how mock files write durations.
const _durationWant = "whole milliseconds or a Go duration such as 1.5s"

// _bandwidthNames lists the names of _bandwidths.
const _bandwidthNames = "gprs, dial_up, 2g, good_2g, 3g, good_3g, dsl, 4g, wifi or fiber"

// _bandwidths are the [Bandwidth] constants by the names mock files give them.
var _bandwidths = map[string]int{
	"gprs":    BandwidthGPRS,
	"dial_up": BandwidthDialUp,
	"2g":      Bandwidth2G,
	"good_2g": BandwidthGood2G,
	"3g":      Bandwidth3G,
	"good_3g": BandwidthGood3G,
	"dsl":     BandwidthDSL,
	"4g":      Bandwidth4G,
	"wifi":    BandwidthWiFi,
	"fiber":   BandwidthFiber,
}

// _builders build the faults by name.
var _builders = map[string]func(s spec) (vlatte.Step, error){
	_fEmptyResponse:          noArgs(EmptyResponse),
	_fMalformedResponseChunk: noArgs(MalformedResponseChunk),
	_fRandomDataThenClose:    noArgs(RandomDataThenClose),
	_fConnectionResetByPeer:  noArgs(ConnectionResetByPeer),
	_fConnectionResetByPeerAfter: func(s spec) (vlatte.Step, error) {
		return s.oneDuration(ConnectionResetByPeerAfter)
	},
	_fChunkedDribbleDelay: func(s spec) (vlatte.Step, error) {
		if err := s.arity(2, 2); err != nil {
			return nil, err
		}

		chunks, err := s.whole(0, "chunks")
		if err != nil {
			return nil, err
		}

		total, err := s.duration(1, "total")
		if err != nil {
			return nil, err
		}

		return built(func() vlatte.Step { return ChunkedDribbleDelay(chunks, total) })
	},
	_fLatency:   buildLatency,
	_fBandwidth: buildBandwidth,
	_fSlicer: func(s spec) (vlatte.Step, error) {
		if err := s.arity(3, 3); err != nil {
			return nil, err
		}

		size, err := s.whole(0, "the average size")
		if err != nil {
			return nil, err
		}

		variation, err := s.whole(1, "the size variation")
		if err != nil {
			return nil, err
		}

		delay, err := s.duration(2, "the delay")
		if err != nil {
			return nil, err
		}

		return built(func() vlatte.Step { return Slicer(size, variation, delay) })
	},
	_fSlowClose: func(s spec) (vlatte.Step, error) { return s.oneDuration(SlowClose) },
	_fTimeout:   func(s spec) (vlatte.Step, error) { return s.oneDuration(Timeout) },
	_fLimitData: func(s spec) (vlatte.Step, error) {
		if err := s.arity(1, 1); err != nil {
			return nil, err
		}

		n, err := s.whole(0, "a number of bytes")
		if err != nil {
			return nil, err
		}

		return built(func() vlatte.Step { return LimitData(n) })
	},
}

var errEmptySpec = errors.New("fault: a fault spec names its fault first, as in " + _specExample)

// Build builds the fault a spec of a mock file names: a fault's name alone, such as "empty_response", or a list
// whose first item names the fault and whose other items are its arguments, such as ["slow_close", "2s"]. Every
// fault describes itself as the spec that builds it again. Durations are whole milliseconds, or a Go duration such
// as "1.5s", from 0. The specs:
//
//	empty_response                                        [EmptyResponse]
//	malformed_response_chunk                              [MalformedResponseChunk]
//	random_data_then_close                                [RandomDataThenClose]
//	connection_reset_by_peer                              [ConnectionResetByPeer]
//	[connection_reset_by_peer_after, d]                   [ConnectionResetByPeerAfter]
//	[chunked_dribble_delay, chunks, total]                [ChunkedDribbleDelay]
//	[latency, latency]                                    [Latency], without jitter
//	[latency, latency, jitter]                            [Latency]
//	[bandwidth, bytesPerSecond]                           [Bandwidth]; a name in place of the rate, such as 3g,
//	                                                      is the constant [Bandwidth3G], as are gprs, dial_up, 2g,
//	                                                      good_2g, good_3g, dsl, 4g, wifi and fiber
//	[slicer, averageSize, sizeVariation, delay]           [Slicer]
//	[slow_close, delay]                                   [SlowClose]
//	[timeout, timeout]                                    [Timeout]
//	[limit_data, n]                                       [LimitData]
//
// A spec the fault's constructor would panic on, such as [bandwidth, 0], fails with an error instead.
func Build(spec any) (vlatte.Step, error) {
	switch s := spec.(type) {
	case string:
		return build(s, nil)
	case []any:
		if len(s) == 0 {
			return nil, errEmptySpec
		}

		name, ok := s[0].(string)
		if !ok {
			return nil, fmt.Errorf("fault: a fault spec names its fault first, as in %s, not %s", _specExample, config.Describe(s[0]))
		}

		return build(name, s[1:])
	default:
		return nil, fmt.Errorf("fault: a fault is a name or a spec such as %s, not %s", _specExample, config.Describe(spec))
	}
}

func build(name string, args []any) (vlatte.Step, error) {
	b, ok := _builders[name]
	if !ok {
		return nil, fmt.Errorf("fault: unknown fault %q", name)
	}

	return b(spec{name: name, args: args})
}

// noArgs builds a fault that takes no argument.
func noArgs(fault func() vlatte.Step) func(s spec) (vlatte.Step, error) {
	return func(s spec) (vlatte.Step, error) {
		if err := s.arity(0, 0); err != nil {
			return nil, err
		}

		return fault(), nil
	}
}

func buildLatency(s spec) (vlatte.Step, error) {
	if err := s.arity(1, 2); err != nil {
		return nil, err
	}

	latency, err := s.duration(0, "a latency")
	if err != nil {
		return nil, err
	}

	var jitter time.Duration
	if len(s.args) == 2 {
		if jitter, err = s.duration(1, "a jitter"); err != nil {
			return nil, err
		}
	}

	return built(func() vlatte.Step { return Latency(latency, jitter) })
}

func buildBandwidth(s spec) (vlatte.Step, error) {
	if err := s.arity(1, 1); err != nil {
		return nil, err
	}

	if name, ok := s.args[0].(string); ok {
		rate, known := _bandwidths[name]
		if !known {
			return nil, fmt.Errorf("fault: bandwidth takes bytes per second or %s, not %q", _bandwidthNames, name)
		}

		return Bandwidth(rate), nil
	}

	rate, err := s.whole(0, "bytes per second")
	if err != nil {
		return nil, err
	}

	return built(func() vlatte.Step { return Bandwidth(rate) })
}

// spec is a fault's name and arguments, as written, for checks whose errors name the fault.
type spec struct {
	name string
	args []any
}

// arity checks that the spec has from least to most arguments.
func (s spec) arity(least, most int) error {
	n := len(s.args)
	switch {
	case n >= least && n <= most:
		return nil
	case least == most:
		return fmt.Errorf("fault: %s takes %s, not %d", s.name, plural(least), n)
	default:
		return fmt.Errorf("fault: %s takes %d or %d arguments, not %d", s.name, least, most, n)
	}
}

// takes is the error of an argument of the wrong type.
func (s spec) takes(want string, arg any) error {
	return fmt.Errorf("fault: %s takes %s, not %s", s.name, want, config.Describe(arg))
}

// whole reads the argument at i as a whole number. Its range is the fault's to check.
func (s spec) whole(i int, what string) (int, error) {
	x, ok := s.args[i].(int64)
	if !ok || x < math.MinInt || x > math.MaxInt {
		return 0, s.takes(what+", a whole number", s.args[i])
	}

	return int(x), nil
}

// duration reads the argument at i as a duration from 0: whole milliseconds, or a Go duration, as mock files write
// durations.
func (s spec) duration(i int, what string) (time.Duration, error) {
	switch x := s.args[i].(type) {
	case int64:
		if x > _maxMilliseconds {
			return 0, fmt.Errorf("fault: %s takes %s of at most %d milliseconds, not %d", s.name, what, _maxMilliseconds, x)
		}

		if x >= 0 {
			return time.Duration(x) * time.Millisecond, nil
		}
	case string:
		d, err := time.ParseDuration(x)
		if err == nil && strings.TrimLeft(x, "+-") == x {
			return d, nil
		}
	default:
		return 0, s.takes(what+", "+_durationWant, x)
	}

	return 0, fmt.Errorf("fault: %s takes %s from 0, %s, not %v", s.name, what, _durationWant, s.args[i])
}

// oneDuration builds a fault that takes a duration alone.
func (s spec) oneDuration(fault func(time.Duration) vlatte.Step) (vlatte.Step, error) {
	if err := s.arity(1, 1); err != nil {
		return nil, err
	}

	d, err := s.duration(0, "a duration")
	if err != nil {
		return nil, err
	}

	return built(func() vlatte.Step { return fault(d) })
}

// built returns what fault, a constructor of this package, returns, or the error of the message it panics with on
// input it refuses: the constructors check their input only by panicking.
func built(fault func() vlatte.Step) (step vlatte.Step, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}

		msg, isMessage := r.(string)
		if !isMessage {
			panic(r)
		}

		err = errors.New(msg)
	}()

	return fault(), nil
}

// plural writes n arguments.
func plural(n int) string {
	if n == 1 {
		return "1 argument"
	}

	return fmt.Sprintf("%d arguments", n)
}
