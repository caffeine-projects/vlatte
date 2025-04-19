package kgrpc

import (
	"github.com/caffeine-projects/vlatte/kcore"
	"github.com/caffeine-projects/vlatte/matcher"
)

type Interceptors struct {
	app *GRPCMockApp
}

type GRPCMockBuilder interface {
	Build(*GRPCMockApp) (*GRPCMock, error)
}

type GRPCMock struct {
	*kcore.BaseMock

	Reply any

	after              []matcher.OnMockSent
	unaryExpectations  []*kcore.Expectation[*UnaryValueSelectorIn]
	streamExpectations []*kcore.Expectation[*StreamValueSelectorIn]
}

func newMock() *GRPCMock {
	return &GRPCMock{BaseMock: kcore.NewMock(), after: make([]matcher.OnMockSent, 0)}
}

func (m *GRPCMock) getRef() string {
	if len(m.Name) == 0 {
		return m.ID
	}

	return m.Name
}

const (
	targetMethod    = "Method"
	targetHeader    = "Header"
	targetBody      = "Body"
	targetBodyField = "Field"
)

func describeTarget(target, key string) string {
	if len(key) == 0 {
		return target
	}

	return target + "(" + key + ")"
}
