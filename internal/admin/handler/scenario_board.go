package handler

import (
	_ "embed"
	"net/http"
)

// _scenarioBoardPage shows and moves scenarios through GET, PUT and DELETE /scenarios, and follows GET /events.
//
//go:embed scenario_board.html
var _scenarioBoardPage []byte

// ScenarioBoardPage answers the page that shows and moves scenarios: GET /scenario-board. On a server that keeps no
// scenarios, it says so.
func ScenarioBoardPage() http.Handler {
	return page(_scenarioBoardPage)
}
