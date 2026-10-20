package mockhttp

import (
	"strings"

	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/config"
)

var (
	_ handler.Recorder = adminRecorder{}
	_ RecordConfigurer = recordDoc{}
)

// adminRecorder lets the admin API record: it starts recordings with the settings of a document.
type adminRecorder struct {
	*Server
}

// StartRecording starts a recording, as Server.StartRecording does, with the settings of doc, the record section of
// the server's settings, in format.
func (r adminRecorder) StartRecording(doc []byte, format string) error {
	return r.Server.StartRecording(recordDoc{data: doc, format: format})
}

// recordDoc applies the settings of a document to a recording: the keys of the record section of the server's
// settings, such as the body of the admin API's POST /recording. Settings that fail the recording's checks are
// problems of the document too.
type recordDoc struct {
	data   []byte
	format string
}

func (d recordDoc) Apply(c *RecordConfig) error {
	if strings.TrimSpace(string(d.data)) == "" {
		return nil
	}

	if _, err := _recordSchema().Load(c, []config.Source{config.Bytes("admin request", d.format, d.data)}); err != nil {
		return err
	}

	if err := c.clone().check(); err != nil {
		return &config.Error{Scope: "recording", Problems: []config.Problem{{
			Code: config.CodeInvalidValue, Origin: "admin request", Message: err.Error(),
		}}}
	}

	return nil
}
