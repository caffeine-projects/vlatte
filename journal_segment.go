package vlatte

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	_journalPrefix    = "journal-"
	_journalLogSuffix = ".log"

	// _segmentEntries and _segmentBytes bound a segment: the journal starts a new one when either is reached, so
	// dropping the oldest entries deletes whole files.
	_segmentEntries = 10_000
	_segmentBytes   = 64 << 20
)

// journalRow is what a journal keeps in memory of an entry: all but its headers and body, which stay on disk, and
// where it is there. Its JSON names are those of the entry's line, so a scan reads a row from a line.
type journalRow struct {
	Seq     uint64    `json:"seq"`
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Kind    Kind      `json:"kind"`
	Outcome Outcome   `json:"outcome"`
	MockID  string    `json:"mock_id,omitempty"`
	Method  string    `json:"method,omitempty"`
	Path    string    `json:"path,omitempty"`
	Target  string    `json:"target,omitempty"`
	Status  int       `json:"status,omitzero"`
	Segment int       `json:"segment"`
	Offset  int64     `json:"offset"`
	Size    int       `json:"size"`
}

// segmentPath returns the path of the log file of segment n of dir.
func segmentPath(dir string, n int) string {
	return filepath.Join(dir, fmt.Sprintf("%s%06d%s", _journalPrefix, n, _journalLogSuffix))
}

// segments returns the numbers of the segments of dir, in order.
func segments(dir string) ([]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var numbers []int

	for _, entry := range entries {
		rest, ok := strings.CutPrefix(entry.Name(), _journalPrefix)
		if !ok || entry.IsDir() {
			continue
		}

		digits, ok := strings.CutSuffix(rest, _journalLogSuffix)
		if !ok {
			continue
		}

		if n, err := strconv.Atoi(digits); err == nil && n >= 0 {
			numbers = append(numbers, n)
		}
	}

	slices.Sort(numbers)

	return numbers, nil
}

// encodeEntry returns the line of e, without its sequence number, which withSeq adds once the journal gives it one.
// Text that isn't UTF-8, such as a header of another encoding, gets U+FFFD in place of its bad bytes.
func encodeEntry(e *JournalEntry) ([]byte, error) {
	line := e.line()
	line.Seq = 0

	return json.Marshal(&line, jsontext.AllowInvalidUTF8(true))
}

// withSeq returns line, a line encodeEntry made, numbered seq and ended by a new line. The ID comes first in line and
// is never left out, so line holds more than "{}".
func withSeq(line []byte, seq uint64) []byte {
	out := make([]byte, 0, len(line)+32)
	out = append(out, `{"seq":`...)
	out = strconv.AppendUint(out, seq, 10)
	out = append(out, ',')
	out = append(out, line[1:]...)

	return append(out, '\n')
}

// scanSegment reads the rows of segment n of dir. A line that doesn't decode, such as one a crash cut short, is left
// out and counted in bad: the rows that follow it are still read.
func scanSegment(dir string, n int) (rows []journalRow, bad int, err error) {
	data, err := os.ReadFile(segmentPath(dir, n))
	if err != nil {
		return nil, 0, err
	}

	offset := 0
	for offset < len(data) {
		end := bytes.IndexByte(data[offset:], '\n')
		if end < 0 {
			// A line a crash cut short has no end: nothing follows it.
			bad++
			break
		}

		line := data[offset : offset+end+1]

		var row journalRow
		if err := json.Unmarshal(line, &row); err != nil || row.Seq == 0 {
			bad++
		} else {
			row.Segment, row.Offset, row.Size = n, int64(offset), len(line)
			rows = append(rows, row)
		}

		offset += len(line)
	}

	return rows, bad, nil
}

// readEntry reads the entry of row from f, the log file of its segment.
func readEntry(f *os.File, row journalRow) (*JournalEntry, error) {
	data := make([]byte, row.Size)
	if _, err := f.ReadAt(data, row.Offset); err != nil {
		return nil, err
	}

	var line journalLine
	if err := json.Unmarshal(data, &line); err != nil {
		return nil, err
	}

	e := line.entry()

	return &e, nil
}
