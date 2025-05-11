package xhttp

import "github.com/caffeine-projects/vlatte"

var _ vlatte.PostActionInput = (*PostActionInput)(nil)

type PostActionInput struct {
	m    *HTTPMock
	req  *RequestValues
	res  *Response
	kit  vlatte.Kit
	args any
}

func (i *PostActionInput) Kind() vlatte.Kind {
	return vlatte.KindHTTP
}

func (i *PostActionInput) Args() any {
	return i.args
}

func (i *PostActionInput) Kit() vlatte.Kit {
	return i.kit
}

func (i *PostActionInput) Bean() vlatte.Bean {
	return i.m
}

func (i *PostActionInput) Request() *RequestValues {
	return i.req
}

func (i *PostActionInput) Response() *Response {
	return i.res
}
