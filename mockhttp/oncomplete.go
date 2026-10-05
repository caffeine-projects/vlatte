package mockhttp

import "github.com/caffeine-projects/vlatte"

var _ vlatte.OnComplete = (*OnComplete)(nil)

// OnComplete implements vlatte.OnComplete for HTTP mocks.
type OnComplete struct {
	m    *Mock
	req  *RequestValues
	res  *Response
	kit  vlatte.Kit
	args vlatte.Parameters
}

func (i *OnComplete) Kind() vlatte.Kind {
	return Kind
}

func (i *OnComplete) Parameters() vlatte.Parameters {
	return i.args
}

func (i *OnComplete) Kit() vlatte.Kit {
	return i.kit
}

func (i *OnComplete) Mock() vlatte.Mock {
	return i.m
}

func (i *OnComplete) Request() *RequestValues {
	return i.req
}

func (i *OnComplete) Response() *Response {
	return i.res
}
