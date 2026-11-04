package fakefy_test

import (
	"encoding/json/v2"
	"fmt"

	"github.com/caffeine-projects/vlatte/fakefy"
)

func ExampleFaker_Schema() {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"id", "name", "email"},
		"properties": map[string]any{
			"id":        map[string]any{"type": "string", "format": "uuid"},
			"name":      map[string]any{"type": "string"},
			"email":     map[string]any{"type": "string"},
			"age":       map[string]any{"type": "integer", "minimum": 18},
			"createdAt": map[string]any{"type": "string"},
			"status":    map[string]any{"enum": []any{"ACTIVE", "BLOCKED"}},
		},
	}

	v, err := fakefy.New(fakefy.WithSeed(42)).Schema(schema)
	if err != nil {
		fmt.Println(err)
		return
	}

	b, _ := json.Marshal(v, json.Deterministic(true))
	fmt.Println(string(b))
	// Output:
	// {"age":48,"createdAt":"2023-05-16T04:47:06Z","email":"alan.baker@example.com","id":"15a4b87c-c8a8-438b-a879-b539a822e70c","name":"Daniel","status":"ACTIVE"}
}
