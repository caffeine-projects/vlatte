package handler_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestOperations_ACapabilityTheServerLacksIsNotSupported(t *testing.T) {
	// An operation reached apart from the admin API's routes, which route only what a server can do, says the server
	// can't do it, as its route would: it never panics on the capability it lacks.
	ops := handler.NewOperations(adminapi.Capabilities{Mocks: adminapi.MocksOf[*fakeMock](newStore(t), nil)})
	ctx := t.Context()

	tests := []struct {
		name   string
		call   func(ctx context.Context) error
		detail string
	}{
		{
			name:   "add a mock",
			call:   func(ctx context.Context) error { _, err := ops.AddMock(ctx, []byte(`{}`), "json"); return err },
			detail: "this mock server can't build mocks from documents",
		},
		{
			name:   "validate a mock",
			call:   func(ctx context.Context) error { _, err := ops.ValidateMock(ctx, []byte(`{}`), "json"); return err },
			detail: "this mock server can't build mocks from documents",
		},
		{
			name:   "replace a mock",
			call:   func(ctx context.Context) error { _, err := ops.ReplaceMock(ctx, "a", []byte(`{}`), "json"); return err },
			detail: "this mock server can't build mocks from documents",
		},
		{
			name:   "reload the mock files",
			call:   func(context.Context) error { return ops.ReloadMocks() },
			detail: "this mock server can't load mock files",
		},
		{
			name:   "save the mocks",
			call:   func(ctx context.Context) error { _, err := ops.SaveMocks(ctx, nil, "json"); return err },
			detail: "this mock server can't save mock files",
		},
		{
			name:   "read the recording",
			call:   func(context.Context) error { _, err := ops.RecordingStatus(); return err },
			detail: "this mock server can't record",
		},
		{
			name:   "start a recording",
			call:   func(context.Context) error { _, err := ops.StartRecording(nil, "json"); return err },
			detail: "this mock server can't record",
		},
		{
			name:   "stop the recording",
			call:   func(ctx context.Context) error { _, err := ops.StopRecording(ctx); return err },
			detail: "this mock server can't record",
		},
		{
			name:   "list the scenarios",
			call:   func(context.Context) error { _, err := ops.ListScenarios(); return err },
			detail: "this mock server can't keep scenarios",
		},
		{
			name: "set a scenario",
			call: func(context.Context) error {
				_, err := ops.SetScenario("a", []byte(`{"state": "b"}`), "json")
				return err
			},
			detail: "this mock server can't keep scenarios",
		},
		{
			name:   "reset the scenarios",
			call:   func(context.Context) error { return ops.ResetScenarios() },
			detail: "this mock server can't keep scenarios",
		},
		{
			name:   "list the requests",
			call:   func(ctx context.Context) error { _, err := ops.ListRequests(ctx, 1, 0); return err },
			detail: "this mock server can't keep a journal of requests",
		},
		{
			name:   "query the requests",
			call:   func(ctx context.Context) error { _, err := ops.QueryRequests(ctx, nil, "json"); return err },
			detail: "this mock server can't keep a journal of requests",
		},
		{
			name:   "get a request",
			call:   func(context.Context) error { _, err := ops.GetRequest("a"); return err },
			detail: "this mock server can't keep a journal of requests",
		},
		{
			name:   "clear the requests",
			call:   func(context.Context) error { return ops.ClearRequests() },
			detail: "this mock server can't keep a journal of requests",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(ctx)
			require.EqualError(t, err, tt.detail)

			doc := handler.Problem(err, "op")

			require.Equal(t, handler.ProblemDocument{
				Status: 501, Title: "Not Implemented", Code: "not_supported", Detail: tt.detail, Instance: "op",
			}, doc)
		})
	}
}
