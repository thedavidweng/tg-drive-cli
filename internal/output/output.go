package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
)

// SchemaVersion is the stable JSON output schema version.
const SchemaVersion = "2026-07-29"

// Renderer writes human or JSON output.
type Renderer struct {
	JSON        bool
	Quiet       bool
	Stdout      io.Writer
	Stderr      io.Writer
	Command     string
	RequestID   string
	Start       time.Time
	Warnings    []string
	Events      bool
	EventWriter io.Writer
}

// Metadata is included in every JSON envelope.
type Metadata struct {
	Command       string   `json:"command"`
	DurationMS    int64    `json:"duration_ms"`
	SchemaVersion string   `json:"schema_version"`
	RequestID     string   `json:"request_id,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

type successEnvelope struct {
	OK   bool     `json:"ok"`
	Data any      `json:"data,omitempty"`
	Meta Metadata `json:"meta"`
}

type errorEnvelope struct {
	OK    bool            `json:"ok"`
	Error apperr.AppError `json:"error"`
	Meta  Metadata        `json:"meta"`
}

// New creates a renderer from flags.
func New(json bool) *Renderer {
	return &Renderer{
		JSON:   json,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
}

// Duration returns elapsed time since Start.
func (r *Renderer) Duration() time.Duration {
	if r.Start.IsZero() {
		return 0
	}
	return time.Since(r.Start)
}

func (r *Renderer) meta() Metadata {
	m := Metadata{
		Command:       r.Command,
		DurationMS:    r.Duration().Milliseconds(),
		SchemaVersion: SchemaVersion,
		RequestID:     r.RequestID,
	}
	if len(r.Warnings) > 0 {
		m.Warnings = append([]string(nil), r.Warnings...)
	}
	return m
}

// Event writes a single NDJSON event. Used by long-running commands.
func (r *Renderer) Event(command string, data any) error {
	w := r.EventWriter
	if w == nil {
		w = r.Stdout
	}
	return r.writeJSON(w, successEnvelope{OK: true, Data: data, Meta: Metadata{
		Command:       command,
		DurationMS:    r.Duration().Milliseconds(),
		SchemaVersion: SchemaVersion,
		RequestID:     r.RequestID,
	}})
}

// Success writes a success envelope or human line.
func (r *Renderer) Success(data any) error {
	if r.JSON {
		return r.writeJSON(r.Stdout, successEnvelope{OK: true, Data: data, Meta: r.meta()})
	}
	if s, ok := data.(string); ok {
		_, err := fmt.Fprintln(r.Stdout, s)
		return err
	}
	return r.writeJSON(r.Stdout, data)
}

// SuccessLine writes a human line regardless of JSON mode helper.
func (r *Renderer) SuccessLine(format string, args ...any) error {
	if r.JSON || r.Quiet {
		return nil
	}
	_, err := fmt.Fprintf(r.Stdout, format+"\n", args...)
	return err
}

// Error writes an error to the appropriate stream and returns it for exit handling.
func (r *Renderer) Error(err error) error {
	ae, ok := apperr.As(err)
	if !ok {
		// Uncategorized errors keep exit code 1 per the CLI contract.
		ae = apperr.New("ERR_UNKNOWN", err.Error())
	}
	if ae.Details == nil {
		ae.Details = map[string]any{}
	}
	if r.JSON {
		_ = r.writeJSON(r.Stdout, errorEnvelope{OK: false, Error: *ae, Meta: r.meta()})
	} else {
		_, _ = fmt.Fprintf(r.Stderr, "error: %s\n", ae.Message)
	}
	return ae
}

func (r *Renderer) writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
