package mpart

import (
	"bytes"
	"testing"
)

func BenchmarkEncode(b *testing.B) {
	// A 10-part reply, written at each response.
	s := &Spec{Subtype: "mixed"}
	for range 10 {
		s.Parts = append(s.Parts, &PartSpec{Name: "p", ContentType: "application/octet-stream", Body: bytes.Repeat([]byte("x"), 4<<10)})
	}

	e, err := Compile(s, _engine)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		if _, _, err := e.Encode(nil); err != nil {
			b.Fatal(err)
		}
	}
}
