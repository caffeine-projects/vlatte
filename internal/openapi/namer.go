package openapi

import (
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte/internal/mockid"
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

// _devices are names Windows keeps for devices: a file named one, with any extension, can't be opened.
var _devices = []string{
	"con", "prn", "aux", "nul",
	"com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
	"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9",
}

// Namer returns the ID of a mock, which its file is named after: letters, digits, '.', '_', '~' and '-'. [Generate]
// makes IDs unique, ignoring case, and short enough for a file name.
type Namer func(m *Mock) string

// RouteFirst names a mock route first, so a folder lists the mocks of a route together:
// <route>.<method>.<status>[.<media>][.<variant>], such as pets._id.get.200.json. The segments of the route are
// joined by '.', and a parameter {id} is written _id. The route / is root.
func RouteFirst(m *Mock) string {
	var parts []string

	for seg := range strings.SplitSeq(m.Route, "/") {
		seg = strings.NewReplacer("{", "_", "}", "").Replace(seg)
		if s := slug.Make(seg, _idMax); s != "" {
			parts = append(parts, s)
		}
	}

	if len(parts) == 0 {
		parts = append(parts, "root")
	}

	parts = append(parts, strings.ToLower(m.Method), strings.ToLower(m.Status))

	if m.MediaType != "" {
		parts = append(parts, mediaName(m.MediaType))
	}

	if v := slug.Make(m.Variant, _idMax); v != "" {
		parts = append(parts, v)
	}

	return strings.Join(parts, ".")
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

// name sets the ID of each of mocks with namer, then makes them safe and unique: an ID that is a Windows device name
// before its first '.' starts with '_', one longer than _idMax is cut and ends with a hash of itself, and one that
// another takes, ignoring case, as macOS and Windows do, ends with -2, -3 and so on, in ID order.
func name(mocks []Mock, namer Namer) error {
	for i := range mocks {
		m := &mocks[i]

		id := namer(m)
		if err := mockid.Check(id); err != nil {
			return fmt.Errorf("openapi: the namer named %s %s %s %q: %w", m.Method, m.Route, m.Status, id, err)
		}

		first, _, _ := strings.Cut(id, ".")
		if slices.Contains(_devices, strings.ToLower(first)) {
			id = "_" + id
		}

		m.ID = shorten(id)
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

	return nil
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
