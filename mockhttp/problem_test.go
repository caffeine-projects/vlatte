package mockhttp

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProblem_EncodesOnlyTheMembersSet(t *testing.T) {
	// RFC 9457 makes every member optional, and clients read a missing type as "about:blank": a member never set must
	// not reach the body as an empty value.
	rv := &RequestValues{RawRequest: _req, URL: _req.URL}

	tests := []struct {
		name    string
		problem *Problem
		want    string
	}{
		{
			name:    "empty",
			problem: NewProblem(),
			want:    `{}`,
		},
		{
			name:    "nil",
			problem: nil,
			want:    `{}`,
		},
		{
			name:    "title only",
			problem: NewProblem().Title("Not found"),
			want:    `{"title":"Not found"}`,
		},
		{
			name: "all standard members",
			problem: NewProblem().
				Type("https://example.com/probs/out-of-credit").
				Title("You do not have enough credit.").
				Status(http.StatusForbidden).
				Detail("Your current balance is 30, but that costs 50.").
				Instance("/account/12345/msgs/abc"),
			want: `{
				"type": "https://example.com/probs/out-of-credit",
				"title": "You do not have enough credit.",
				"status": 403,
				"detail": "Your current balance is 30, but that costs 50.",
				"instance": "/account/12345/msgs/abc"
			}`,
		},
		{
			name: "extensions beside standard members",
			problem: NewProblem().
				Title("Invalid request").
				Extension("balance", 30).
				Extension("accounts", []string{"/account/12345", "/account/67890"}),
			want: `{"title":"Invalid request","balance":30,"accounts":["/account/12345","/account/67890"]}`,
		},
		{
			name:    "an extension set again keeps the last value",
			problem: NewProblem().Extension("trace_id", "a").Extension("trace_id", "b"),
			want:    `{"trace_id":"b"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := replierOf(t, OK().Problem(tc.problem)).Reply(t.Context(), ReplyInput{RequestValues: rv})

			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(res.Body))
		})
	}
}

func TestProblem_AnExtensionNamedAfterAStandardMemberFailsTheMock(t *testing.T) {
	// An extension can't redefine a standard member: replacing it silently would hide the mistake, so the mock fails
	// to build.
	m := MustNew(Setup().Silent())

	for _, name := range []string{"type", "title", "status", "detail", "instance"} {
		t.Run(name, func(t *testing.T) {
			_, err := m.Mock(Getf("/p").Reply(OK().Problem(NewProblem().Extension(name, "x"))))

			require.ErrorIs(t, err, errProblemMember)
			require.ErrorContains(t, err, `"`+name+`"`)
		})
	}
}
