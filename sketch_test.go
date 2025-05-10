package vlatte

import (
	htmltemplate "html/template"
	"os"
	"testing"
	texttemplate "text/template"
)

func TestXxx(t *testing.T) {
	t1 := texttemplate.Must(texttemplate.New("t").Parse("Hello {{.}}"))
	t1.Execute(os.Stdout, "something/else/nice/dc=us/categories=100/ok")

	t2 := htmltemplate.Must(htmltemplate.New("t").Parse("Hello {{.}}"))
	t2.Execute(os.Stdout, "something/else/nice/dc=us/categories=100/ok")
}
