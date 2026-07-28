package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

// schemaVersion is the CLI JSON envelope version defined in docs/contracts/cli.md.
const schemaVersion = "1"

// Envelope is the stable machine-readable output contract for `--json`.
// See docs/contracts/cli.md. Exactly one Envelope is written to stdout in JSON
// mode, and no diagnostics are ever mixed into it.
type Envelope struct {
	SchemaVersion string `json:"schema_version"`
	OK            bool   `json:"ok"`
	Command       string `json:"command"`
	Data          any    `json:"data"`
	// Warnings is always an empty array today: no command has a non-fatal
	// condition to report that does not already belong in Data (drift markers,
	// transfer skip counts and bootstrap checks are all structured results, not
	// warnings). The field stays because a caller parsing the envelope may rely
	// on its presence; only the never-used producer argument is gone.
	Warnings []string       `json:"warnings"`
	Error    *EnvelopeError `json:"error"`
}

// EnvelopeError is the error object carried by a failing Envelope. Message must
// never contain credentials, raw env, or full command lines with secrets.
type EnvelopeError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// successEnvelope builds an ok=true envelope for command with the given data.
func successEnvelope(command string, data any) Envelope {
	return Envelope{
		SchemaVersion: schemaVersion,
		OK:            true,
		Command:       command,
		Data:          data,
		Warnings:      []string{},
		Error:         nil,
	}
}

// errorEnvelope builds an ok=false envelope for command with the given error.
func errorEnvelope(command string, e *EnvelopeError) Envelope {
	return Envelope{
		SchemaVersion: schemaVersion,
		OK:            false,
		Command:       command,
		Data:          nil,
		Warnings:      []string{},
		Error:         e,
	}
}

// writeJSON marshals env as exactly one JSON document plus a trailing newline.
func writeJSON(w io.Writer, env Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}
