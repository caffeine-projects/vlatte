package xhttp

import (
	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.Kit = (*Kit)(nil)

type Kit struct {
	template vlatte.Template
	data     any
}

func (k *Kit) Sketch() vlatte.Template {
	return k.template
}

func (k *Kit) Data() any {
	return k.data
}
