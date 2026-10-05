// Package mockhttp runs HTTP mock servers for tests.
//
// Start a [Server], add mocks built with [Get], [Post] and the other builders,
// and point the code under test at [Server.URL]:
//
//	m := mockhttp.MustNew()
//	m.MustStart()
//	defer m.Close()
//
//	m.MustMock(mockhttp.Getf("/hello").Reply(mockhttp.OK().PlainText("hello world")))
//
// Use [github.com/caffeine-projects/vlatte/match] to match headers, query parameters or bodies.
// Mocks can also come from YAML or JSON files; see [FromFile] and [ConfigBuilder.MockFilePatterns].
//
// Settings come from code, then from a .vlatterc.json, .vlatterc.yaml or .vlatterc.yml file and VLATTE_*
// environment variables, each overriding the one before. [ConfigBuilder.ConfigSources] picks the sources,
// and [ConfigUsage] lists every setting.
package mockhttp
