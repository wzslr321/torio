package cli

import (
	"fmt"

	"github.com/wzslr321/torio/internal/lima"
)

// checkData is one proven check in a JSON envelope. Detail is a short derived
// value — a parsed version, a uid, a mode, a count — never a raw output blob
// and never a guest filename.
type checkData struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// checkPayload renders adapter checks for a success envelope.
func checkPayload(checks []lima.CheckResult) []checkData {
	out := make([]checkData, 0, len(checks))
	for _, c := range checks {
		out = append(out, checkData{Name: c.Name, OK: c.OK, Detail: c.Detail})
	}
	return out
}

// checkDetails renders adapter checks for an error's details, so a failing
// command still names the check that did not hold. Values pass through the
// final redactor in fail().
func checkDetails(checks []lima.CheckResult) []map[string]any {
	out := make([]map[string]any, 0, len(checks))
	for _, c := range checks {
		out = append(out, map[string]any{"name": c.Name, "ok": c.OK, "detail": c.Detail})
	}
	return out
}

// writeCheckLines prints the human rendering: one `[ok|FAIL] name: detail`
// line per check.
func (a *app) writeCheckLines(checks []lima.CheckResult) error {
	for _, c := range checks {
		mark := "ok"
		if !c.OK {
			mark = "FAIL"
		}
		if _, err := fmt.Fprintf(a.stdout, "[%s] %s: %s\n", mark, c.Name, c.Detail); err != nil {
			return err
		}
	}
	return nil
}
