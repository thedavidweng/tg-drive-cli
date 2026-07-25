package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/thedavidweng/tg-drive-cli/internal/apperr"
)

// Renderer writes human or JSON output.
type Renderer struct {
	JSON   bool
	Quiet  bool
	Stdout io.Writer
	Stderr io.Writer
}

// New creates a renderer from flags.
func New(json bool) *Renderer {
	return &Renderer{
		JSON:   json,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
}

type successEnvelope struct {
	OK   bool `json:"ok"`
	Data any  `json:"data"`
}

type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

type errorEnvelope struct {
	OK    bool      `json:"ok"`
	Error errorBody `json:"error"`
}

// Success writes a success envelope or human line.
func (r *Renderer) Success(data any) error {
	if r.JSON {
		return r.writeJSON(r.Stdout, successEnvelope{OK: true, Data: data})
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
	if r.JSON {
		env := errorEnvelope{
			OK: false,
			Error: errorBody{
				Code:    ae.Code,
				Message: ae.Message,
				Details: ae.Details,
			},
		}
		if env.Error.Details == nil {
			env.Error.Details = map[string]any{}
		}
		_ = r.writeJSON(r.Stdout, env)
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
