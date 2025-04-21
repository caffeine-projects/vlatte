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
	*kcore.M

	Reply any

	after              []matcher.OnMockSent
	unaryExpectations  []*kcore.Eval[*UnaryValueSelectorIn]
	streamExpectations []*kcore.Eval[*StreamValueSelectorIn]
}

func newMock() *GRPCMock {
	return &GRPCMock{M: kcore.NewMock(kcore.UseUUID()), after: make([]matcher.OnMockSent, 0)}
}

func (m *GRPCMock) getRef() string {
	if len(m.GetName()) == 0 {
		return m.GetID()
	}

	return m.GetName()
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
