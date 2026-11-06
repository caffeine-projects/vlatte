package openapi

import (
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte/internal/slug"
)

// _idMax bounds a mock's ID, and so the name of its file, below the 255 bytes file systems take.
const _idMax = 200

// _mediaNames are the short names of common media types in IDs.
var _mediaNames = map[string]string{
	"application/json":                  "json",
	"application/xml":                   "xml",
	"text/xml":                          "xml",
	"text/plain":                        "text",
	"text/html":                         "html",
	"application/x-www-form-urlencoded": "form",
	"multipart/form-data":               "multipart",
}

// Namer returns the ID of a mock, which its file is named after: letters, digits, '_', '~' and '-', so the only dots
// of the file name are those of .mock.yaml, and never a name Windows keeps for a device, such as con. [Generate]
// makes IDs unique, ignoring case, and short enough for a file name.
type Namer func(m *Mock) string

// RouteFirst names a mock route first, so a folder lists the mocks of a route together:
// <route>-<method>-<status>[-<media>][-<variant>], such as pets-_id-get-200-json. The segments of the route are
// joined by '-', a parameter {id} is written _id, and a dot inside a part is written '-'. The route / is root.
func RouteFirst(m *Mock) string {
	var parts []string

	for seg := range strings.SplitSeq(m.Route, "/") {
		seg = strings.NewReplacer("{", "_", "}", "").Replace(seg)
		if s := part(seg); s != "" {
			parts = append(parts, s)
		}
	}

	if len(parts) == 0 {
		parts = append(parts, "root")
	}

	parts = append(parts, part(strings.ToLower(m.Method)), strings.ToLower(m.Status))

	if m.MediaType != "" {
		parts = append(parts, part(mediaName(m.MediaType)))
	}

	if v := part(m.Variant); v != "" {
		parts = append(parts, v)
	}

	return strings.Join(parts, "-")
}

// part returns s as a part of an ID: its slug, with '-' for each dot.
func part(s string) string {
	return strings.Trim(strings.ReplaceAll(slug.Make(s, _idMax), ".", "-"), "-")
}

// mediaName returns the short name of a media type: json for application/json, and the slug of anything else,
// without application/ and its parameters, such as vnd.api-json for application/vnd.api+json. A * of a media range is
// any: */* is any, and text/* text-any.
func mediaName(mt string) string {
	mt, _, _ = strings.Cut(strings.ToLower(mt), ";")
	mt = strings.TrimSpace(mt)

	if name, ok := _mediaNames[mt]; ok {
		return name
	}

	if mt == "*/*" {
		return "any"
	}

	return slug.Make(strings.ReplaceAll(strings.TrimPrefix(mt, "application/"), "*", "any"), _idMax)
}

// name sets the ID of each of mocks with namer, then makes them unique: an ID longer than _idMax is cut and ends with
// a hash of itself, and one that another takes, ignoring case, as macOS and Windows do, ends with -2, -3 and so on,
// in ID order.
func name(mocks []Mock, namer Namer) {
	for i := range mocks {
		mocks[i].ID = shorten(namer(&mocks[i]))
	}

	order := make([]int, len(mocks))
	for i := range order {
		order[i] = i
	}

	slices.SortStableFunc(order, func(a, b int) int { return strings.Compare(mocks[a].ID, mocks[b].ID) })

	taken := make(map[string]bool, len(mocks))

	for _, i := range order {
		id := mocks[i].ID

		for n := 2; taken[strings.ToLower(id)]; n++ {
			suffix := "-" + strconv.Itoa(n)
			id = mocks[i].ID[:min(len(mocks[i].ID), _idMax-len(suffix))] + suffix
		}

		taken[strings.ToLower(id)] = true
		mocks[i].ID = id
	}
}

// shorten returns id when it is at most _idMax bytes, and otherwise its start and a hash of all of it.
func shorten(id string) string {
	if len(id) <= _idMax {
		return id
	}

	h := fnv.New32a()
	h.Write([]byte(id))

	return fmt.Sprintf("%s-%08x", id[:_idMax-9], h.Sum32())
}
