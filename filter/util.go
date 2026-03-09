package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

func success() vlatte.Result                { return vlatte.Result{Pass: true, Message: ""} }
func mismatch(message string) vlatte.Result { return vlatte.Result{Pass: false, Message: message} }

func describe(m vlatte.Filter) any {
	if sd, ok := m.(descriptor.SelfDescriptive); ok {
		return sd.Describe()
	}

	return Anything().(descriptor.SelfDescriptive).Describe()
}
