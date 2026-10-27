package tmplfunc

import "testing"

const _orderXML = `<order id="o-1" xmlns:x="urn:x"><item sku="a">2</item><item sku="b">1</item><x:note>hi</x:note></order>`

func TestXPath(t *testing.T) {
	runFuncCases(t, []funcCase{
		{name: "the text of the first node", text: `{{ xpath "/order/item" . }}`, data: _orderXML, want: "2"},
		{name: "an attribute", text: `{{ xpath "/order/item[2]/@sku" . }}`, data: _orderXML, want: "b"},
		{name: "a count", text: `{{ xpath "count(//item)" . }}`, data: _orderXML, want: "2"},
		{name: "a sum keeps its fraction", text: `{{ xpath "sum(//item) div 2" . }}`, data: _orderXML, want: "1.5"},
		{name: "a boolean", text: `{{ xpath "count(//item) = 2" . }}`, data: _orderXML, want: "true"},
		{name: "nothing selected is empty", text: `[{{ xpath "/order/missing" . }}]`, data: _orderXML, want: "[]"},
		{
			// SOAP bodies: local-name() selects by name whatever the prefix.
			name: "a prefixed element by local name", text: `{{ xpath "//*[local-name()='note']" . }}`, data: _orderXML, want: "hi",
		},
		{name: "text that isn't XML", text: `{{ xpath "/a" "<a>" }}`, err: "error calling xpath: element <a> is not closed"},
		{name: "an invalid expression", text: `{{ xpath "/a[" "<a/>" }}`, err: "error calling xpath: expression must evaluate to a node-set"},
	})
}

func TestXPathNodes(t *testing.T) {
	runFuncCases(t, []funcCase{
		{
			name: "each node's name, text and attributes",
			text: `{{ range xpathNodes "//item" . }}{{ .Name }}:{{ .Attrs.sku }}={{ .Text }};{{ end }}`, data: _orderXML,
			want: "item:a=2;item:b=1;",
		},
		{
			// An element prints with the declarations its names need, so it is XML of its own.
			name: "printing an element writes its XML, namespaces declared", text: `{{ index (xpathNodes "//x:note" .) 0 }}`,
			data: _orderXML, want: `<x:note xmlns:x="urn:x">hi</x:note>`,
		},
		{name: "an attribute", text: `{{ range xpathNodes "//@sku" . }}{{ .Name }}={{ . }} {{ end }}`, data: _orderXML, want: "sku=a sku=b "},
		{name: "nothing selected", text: `{{ len (xpathNodes "//missing" .) }}`, data: _orderXML, want: "0"},
		{name: "text that isn't XML", text: `{{ xpathNodes "/a" "<a>" }}`, err: "error calling xpathNodes: element <a> is not closed"},
		{name: "a value instead of nodes", text: `{{ xpathNodes "count(//item)" . }}`, data: _orderXML, err: `error calling xpathNodes: "count(//item)" computes a float64, not nodes`},
	})
}
