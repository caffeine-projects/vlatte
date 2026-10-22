package journal

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/atomicfile"
)

const (
	_journalPrefix    = "journal-"
	_journalLogSuffix = ".log"
	_journalIdxSuffix = ".idx"

	// _journalTemps names the files a journal is writing: a crash may leave them, which the next one removes.
	_journalTemps = ".journal-*.tmp"

	// _journalCleared names the tombstone of a Clear: the first segment it kept, whose earlier ones hold requests
	// it removed, which a journal of the directory drops as it opens.
	_journalCleared = "journal.clear"

	// _segmentEntries and _segmentBytes bound a segment: the journal starts a new one when either is reached, so
	// dropping the oldest entries deletes whole files.
	_segmentEntries = 10_000
	_segmentBytes   = 64 << 20
)

// journalRow is what a journal keeps in memory of an entry: all but its headers and body, which stay on disk, and
// where it is there. Its JSON names are those of the entry's line, so a scan reads a row from a line.
type journalRow struct {
	Seq     uint64      `json:"seq"`
	ID      string      `json:"id"`
	Time    time.Time   `json:"time"`
	Kind    vlatte.Kind `json:"kind"`
	Outcome Outcome     `json:"outcome"`
	MockID  string      `json:"mock_id,omitempty"`
	Method  string      `json:"method,omitempty"`
	Path    string      `json:"path,omitempty"`
	Target  string      `json:"target,omitempty"`
	Status  int         `json:"status,omitzero"`
	Segment int         `json:"segment"`
	Offset  int64       `json:"offset"`
	Size    int         `json:"size"`
}

// segmentPath returns the path of the log file of segment n of dir.
func segmentPath(dir string, n int) string {
	return segmentFile(dir, n, _journalLogSuffix)
}

// indexPath returns the path of the sidecar of segment n of dir: its rows, which a journal reads in place of the
// log, when it describes the log as it is.
func indexPath(dir string, n int) string {
	return segmentFile(dir, n, _journalIdxSuffix)
}

func segmentFile(dir string, n int, suffix string) string {
	return filepath.Join(dir, fmt.Sprintf("%s%06d%s", _journalPrefix, n, suffix))
}

// segmentIndex is the sidecar of a segment: its rows, and the size and time of change of the log they describe.
type segmentIndex struct {
	LogSize    int64        `json:"log_size"`
	LogModTime int64        `json:"log_mod_time"`
	Rows       []journalRow `json:"rows"`
}

// writeIndex writes the sidecar of segment n of dir, with rows, all those of the segment, which nothing appends to
// anymore.
func writeIndex(dir string, n int, rows []journalRow) error {
	info, err := os.Stat(segmentPath(dir, n))
	if err != nil {
		return err
	}

	data, err := json.Marshal(&segmentIndex{LogSize: info.Size(), LogModTime: info.ModTime().UnixNano(), Rows: rows})
	if err != nil {
		return err
	}

	return atomicfile.WritePerm(dir, _journalTemps, indexPath(dir, n), data, 0o600)
}

// readIndex returns the rows of segment n of dir from its sidecar, when there is one that describes the log as it
// is, of the size and time of change it names: a log written past its sidecar, or rewritten, is scanned instead.
func readIndex(dir string, n int) ([]journalRow, bool) {
	data, err := os.ReadFile(indexPath(dir, n))
	if err != nil {
		return nil, false
	}

	info, err := os.Stat(segmentPath(dir, n))
	if err != nil {
		return nil, false
	}

	var idx segmentIndex
	if err := json.Unmarshal(data, &idx); err != nil || idx.LogSize != info.Size() || idx.LogModTime != info.ModTime().UnixNano() {
		return nil, false
	}

	for i := range idx.Rows {
		row := &idx.Rows[i]
		if row.Seq == 0 || row.Offset < 0 || row.Size <= 0 || row.Offset+int64(row.Size) > idx.LogSize {
			return nil, false
		}

		row.Segment = n
	}

	return idx.Rows, true
}

// removeSegment removes the files of segment n of dir: its sidecar first, which names nothing without its log.
func removeSegment(dir string, n int) error {
	var errs []error

	for _, path := range []string{indexPath(dir, n), segmentPath(dir, n)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// listSegments returns the numbers of the logs of dir, and those of its sidecars, in order.
func listSegments(dir string) (logs, sidecars []int, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}

	for _, entry := range entries {
		rest, ok := strings.CutPrefix(entry.Name(), _journalPrefix)
		if !ok || entry.IsDir() {
			continue
		}

		for suffix, numbers := range map[string]*[]int{_journalLogSuffix: &logs, _journalIdxSuffix: &sidecars} {
			if digits, ok := strings.CutSuffix(rest, suffix); ok {
				if n, err := strconv.Atoi(digits); err == nil && n >= 0 {
					*numbers = append(*numbers, n)
				}
			}
		}
	}

	slices.Sort(logs)
	slices.Sort(sidecars)

	return logs, sidecars, nil
}

// encodeEntry returns the line of e, without its sequence number, which withSeq adds once the journal gives it one.
// Text that isn't UTF-8, such as a header of another encoding, gets U+FFFD in place of its bad bytes.
func encodeEntry(e *Entry) ([]byte, error) {
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
func readEntry(f *os.File, row journalRow) (*Entry, error) {
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

// writeCleared writes the tombstone of a Clear of dir that kept segment n and the later ones. It survives the process,
// as the journal's logs do, not the machine: neither is synced.
func writeCleared(dir string, n int) error {
	return atomicfile.WritePerm(dir, _journalTemps, filepath.Join(dir, _journalCleared), []byte(strconv.Itoa(n)+"\n"), 0o600)
}

// readCleared returns the first segment the last Clear of dir kept, 0 when no Clear left one to drop.
func readCleared(dir string) int {
	data, err := os.ReadFile(filepath.Join(dir, _journalCleared))
	if err != nil {
		return 0
	}

	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}

	return n
}
