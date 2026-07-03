package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
)

func TestSuccessJSONEnvelope(t *testing.T) {
	var stdout bytes.Buffer
	r := &Renderer{JSON: true, Stdout: &stdout, Stderr: &bytes.Buffer{}}
	if err := r.Success(map[string]string{"version": "dev"}); err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env["ok"] != true {
		t.Fatalf("ok = %v", env["ok"])
	}
}

func TestErrorJSONEnvelope(t *testing.T) {
	var stdout bytes.Buffer
	r := &Renderer{JSON: true, Stdout: &stdout, Stderr: &bytes.Buffer{}}
	err := r.Error(apperr.New(apperr.ErrUsage, "bad command"))
	if err == nil {
		t.Fatal("expected error")
	}
	s := stdout.String()
	if !strings.Contains(s, `"ok":false`) {
		t.Fatalf("missing ok:false: %s", s)
	}
	if !strings.Contains(s, `"ERR_USAGE"`) {
		t.Fatalf("missing code: %s", s)
	}
}

func TestErrorHumanGoesToStderr(t *testing.T) {
	var stderr bytes.Buffer
	r := &Renderer{JSON: false, Stdout: &bytes.Buffer{}, Stderr: &stderr}
	if err := r.Error(apperr.New(apperr.ErrPathInvalid, "bad path")); err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(stderr.String(), "bad path") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
