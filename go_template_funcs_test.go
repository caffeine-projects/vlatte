package vlatte

import "testing"

func TestQueryFuncs(t *testing.T) {
	runFuncCases(t, []funcCase{
		{name: "queryUnescape undoes urlquery", text: `{{ urlquery . | queryUnescape }}`, data: "a b&c=d/é", want: "a b&c=d/é"},
		{name: "queryUnescape of a bad escape", text: `{{ queryUnescape "%zz" }}`, err: `error calling queryUnescape: invalid URL escape "%zz"`},
		{
			name: "parseQuery reads form text, each value decoded", text: `{{ $q := parseQuery . }}{{ $q.Get "a" }} {{ index $q.b 1 }}`,
			data: "a=1+2&b=x&b=y%26z", want: "1 2 y&z",
		},
		{name: "parseQuery of a bad escape", text: `{{ parseQuery "a=%zz" }}`, err: `error calling parseQuery: invalid URL escape "%zz"`},
	})
}
