package handler

import (
	"context"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

// Reset removes the mocks the admin API added, loads the mocks of the server's sources again, its mock files and its
// persistent store, starts every scenario over, and clears the journal. Mocks added in code stay as they are. A
// server without mock files, scenarios, or a journal, skips them. The sources load first: when they fail to, nothing
// changes. The mocks of a persistent store come back from it, as it holds them: reset never deletes what it holds.
func (o *Operations) Reset(ctx context.Context) error {
	return o.mocks.reset(ctx, o.files, o.scenarios, o.journal)
}

func (m *storeMocks) reset(ctx context.Context, files adminapi.Files, scenarios adminapi.Scenarios, journal adminapi.Journal) error {
	mocks, err := m.mocks.PickAll(ctx)
	if err != nil {
		return err
	}

	added := make(map[any]struct{}, len(mocks))
	for _, mock := range mocks {
		if origin(mock) == adminapi.Origin {
			added[mock] = struct{}{}
		}
	}

	// A reload swaps in what the sources hold in one step: a file's mock in place of an admin mock of its id, and the
	// mocks of a persistent store in place of those it served before. Or nothing at all.
	if files != nil {
		if err := files.Reload(); err != nil {
			return loadError(err)
		}
	}

	// The admin mocks the reload left are those no source holds.
	mocks, err = m.mocks.PickAll(ctx)
	if err != nil {
		return err
	}

	var left []vlatte.Mock
	for _, mock := range mocks {
		if _, ok := added[mock]; ok {
			left = append(left, mock)
		}
	}

	if len(left) > 0 {
		if err := m.mocks.Replace(ctx, left); err != nil {
			return err
		}
	}

	if scenarios != nil {
		scenarios.ResetScenarios()
	}

	if journal != nil {
		return journal.Clear()
	}

	return nil
}
