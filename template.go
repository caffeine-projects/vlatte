package vlatte

import (
	htmltemplate "html/template"
	"io"
	texttemplate "text/template"
)

type Template interface {
	Name() string
	Parse(text string) (Template, error)
	Execute(wr io.Writer, data any) error
	ExecuteTemplate(wr io.Writer, name string, data any) error
}

type TemplateDataProviderInput interface {
	Sketch() Template
}

type TemplateDataProvider interface {
	Key() string
	Data(in TemplateDataProviderInput) (any, error)
}

func TemplateHTML(tmpl *htmltemplate.Template) Template {
	return &htmlTmpl{tmpl: tmpl}
}

func TemplateText(tmpl *texttemplate.Template) Template {
	return &textTmpl{tmpl: tmpl}
}

type htmlTmpl struct {
	tmpl *htmltemplate.Template
}

func (h *htmlTmpl) Name() string {
	return h.tmpl.Name()
}

func (h *htmlTmpl) Parse(text string) (Template, error) {
	if _, err := h.tmpl.Parse(text); err != nil {
		return nil, err
	}

	return h, nil
}

func (h *htmlTmpl) Execute(wr io.Writer, data any) error {
	return h.tmpl.Execute(wr, data)
}

func (h *htmlTmpl) ExecuteTemplate(wr io.Writer, name string, data any) error {
	return h.tmpl.ExecuteTemplate(wr, name, data)
}

type textTmpl struct {
	tmpl *texttemplate.Template
}

func (t *textTmpl) Name() string {
	return t.tmpl.Name()
}
func (t *textTmpl) Parse(text string) (Template, error) {
	if _, err := t.tmpl.Parse(text); err != nil {
		return nil, err
	}

	return t, nil
}

func (t *textTmpl) Execute(wr io.Writer, data any) error {
	return t.tmpl.Execute(wr, data)
}

func (t *textTmpl) ExecuteTemplate(wr io.Writer, name string, data any) error {
	return t.tmpl.ExecuteTemplate(wr, name, data)
}
