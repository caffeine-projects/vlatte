package xhttp

import "github.com/caffeine-projects/vlatte"

var _ vlatte.OnComplete = (*HTTPOnComplete)(nil)

// HTTPOnComplete implements vlatte.OnComplete for HTTP mocks.
type HTTPOnComplete struct {
	m    *HTTPMock
	req  *RequestValues
	res  *Response
	kit  vlatte.Kit
	args vlatte.Parameters
}

func (i *HTTPOnComplete) Kind() vlatte.Kind {
	return vlatte.KindHTTP
}

func (i *HTTPOnComplete) Parameters() vlatte.Parameters {
	return i.args
}

func (i *HTTPOnComplete) Kit() vlatte.Kit {
	return i.kit
}

func (i *HTTPOnComplete) Bean() vlatte.Bean {
	return i.m
}

func (i *HTTPOnComplete) Request() *RequestValues {
	return i.req
}

func (i *HTTPOnComplete) Response() *Response {
	return i.res
}
