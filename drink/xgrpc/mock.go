package xgrpc

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter"
)

type Interceptors struct {
	app *GRPCMockApp
}

type GRPCMockBuilder interface {
	Roast(*GRPCMockApp) (*GRPCMock, error)
}

type GRPCMock struct {
	*vlatte.GreenBean

	Reply any

	after              []filter.OnMockSent
	unaryExpectations  []*vlatte.Eval[*UnaryValueSelectorIn]
	streamExpectations []*vlatte.Eval[*StreamValueSelectorIn]
}

func newMock() *GRPCMock {
	return &GRPCMock{GreenBean: vlatte.NewMock(vlatte.UseUUID()), after: make([]filter.OnMockSent, 0)}
}

func (m *GRPCMock) getRef() string {
	if len(m.GetName()) == 0 {
		return m.GetID()
	}

	return m.GetName()
}
