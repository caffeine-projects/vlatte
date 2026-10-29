package journal_test

import (
	"context"
	"fmt"
	"net/http"

	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
)

func Example() {
	// An empty Dir keeps the files in a temporary directory, which Close removes.
	j := journal.MustNew(journal.Options{})
	defer j.Close()

	for _, e := range []*journal.Entry{
		{ID: "1", Kind: "http", Outcome: journal.OutcomeMatched, Method: "POST", Path: "/orders", Status: http.StatusCreated},
		{ID: "2", Kind: "http", Outcome: journal.OutcomeMatched, Method: "GET", Path: "/orders/1", Status: http.StatusOK},
		{ID: "3", Kind: "http", Outcome: journal.OutcomeUnmatched, Method: "GET", Path: "/users", Status: http.StatusTeapot},
	} {
		if err := j.Append(e); err != nil {
			panic(err)
		}
	}

	page, err := j.Find(context.Background(), journal.Query{Path: match.HasPrefix("/orders")})
	if err != nil {
		panic(err)
	}

	// The newest come first.
	for _, e := range page.Entries {
		fmt.Println(e.Seq, e.Method, e.Path, e.Status)
	}

	unmatched, err := j.Count(context.Background(), journal.Query{Outcome: journal.OutcomeUnmatched})
	if err != nil {
		panic(err)
	}

	fmt.Println(unmatched)
	// Output:
	// 2 GET /orders/1 200
	// 1 POST /orders 201
	// 1
}

func ExampleJournal_Last() {
	j := journal.MustNew(journal.Options{})
	defer j.Close()

	// A request from before, such as another test's.
	if err := j.Append(&journal.Entry{ID: "1", Kind: "http", Method: "GET", Path: "/health"}); err != nil {
		panic(err)
	}

	mark := j.Last()

	if err := j.Append(&journal.Entry{ID: "2", Kind: "http", Method: "GET", Path: "/health"}); err != nil {
		panic(err)
	}

	// After selects only the entries appended since the mark.
	n, err := j.Count(context.Background(), journal.Query{Path: match.Equal("/health"), After: mark})
	if err != nil {
		panic(err)
	}

	fmt.Println(n)
	// Output: 1
}

func ExampleJournal_Find_pages() {
	j := journal.MustNew(journal.Options{})
	defer j.Close()

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if err := j.Append(&journal.Entry{ID: id, Kind: "http", Method: "GET", Path: "/" + id}); err != nil {
			panic(err)
		}
	}

	// Set Before to a page's Next to get the next page; Next is 0 on the last one.
	q := journal.Query{Limit: 2}

	for {
		page, err := j.Find(context.Background(), q)
		if err != nil {
			panic(err)
		}

		for _, e := range page.Entries {
			fmt.Print(e.ID, " ")
		}

		fmt.Println("of", page.Count)

		if page.Next == 0 {
			break
		}

		q.Before = page.Next
	}
	// Output:
	// e d of 5
	// c b of 5
	// a of 5
}
