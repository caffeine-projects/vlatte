package vlatte

import (
	htmltemplate "html/template"
	"io"
	texttemplate "text/template"
)

var _ Sketch = (*texttemplate.Template)(nil)
var _ Sketch = (*htmltemplate.Template)(nil)

type Sketch interface {
	Name() string
	Execute(wr io.Writer, data any) error
	ExecuteTemplate(wr io.Writer, name string, data any) error
}
