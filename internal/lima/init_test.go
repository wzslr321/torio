/*
 * AI-Provenance:
 *   model: Cursor Grok 4.5
 *   harness: Cursor
 *   skills:
 *     - mark-ai-provenance
 */

package lima

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wzslr321/torio/internal/execx"
)

func TestInitCreatesAbsentInstance(t *testing.T) {
	var createArgs []string
	var seenTemplate string
	listCalls := 0
	fr := &fakeRunner{respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
		switch {
		case len(cmd.Args) >= 1 && cmd.Args[0] == "list":
			listCalls++
			if listCalls == 1 {
				return stdoutResult(""), nil // absent before create
			}
			return stdoutResult(fixtureCompatibleInstanceJSON(InstanceName, "Stopped")), nil
		case len(cmd.Args) >= 1 && cmd.Args[0] == "create":
			createArgs = append([]string{}, cmd.Args...)
			if len(cmd.Args) >= 4 {
				seenTemplate = cmd.Args[3]
				body, err := os.ReadFile(seenTemplate)
				if err != nil {
					t.Fatalf("create template unreadable: %v", err)
				}
				text := string(body)
				if strings.Contains(text, "__TORIO_OPERATOR_USER__") {
					t.Fatalf("operator placeholder left unsubstituted")
				}
				if !strings.Contains(text, "mounts: []") {
					t.Fatalf("template missing mounts: []")
				}
				if strings.Contains(text, "forwardAgent: true") {
					t.Fatalf("template must not enable forwardAgent")
				}
				if strings.Contains(text, "usermod -aG docker hermes") {
					t.Fatalf("template must not grant hermes docker group")
				}
				if !strings.Contains(text, PromotedImageDigest) {
					t.Fatalf("template missing promoted image digest")
				}
				if !strings.Contains(text, "operator") {
					t.Fatalf("template missing substituted operator user")
				}
			}
			return exitResult(0, "", ""), nil
		default:
			t.Fatalf("unexpected argv: %v", cmd.Args)
			return execx.Result{}, nil
		}
	}}
	a := New(fr)

	res, err := a.Init(context.Background(), InitOptions{OperatorUser: "operator"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !res.Created {
		t.Fatalf("Created = false, want true")
	}
	if res.ImageDigest != PromotedImageDigest {
		t.Fatalf("ImageDigest = %q, want %q", res.ImageDigest, PromotedImageDigest)
	}
	want := []string{"create", "--name=" + InstanceName, "--tty=false", seenTemplate}
	if !equalArgs(createArgs, want) {
		t.Fatalf("create argv = %v, want %v", createArgs, want)
	}
	if seenTemplate == "" {
		t.Fatal("expected template path on create")
	}
	if _, err := os.Stat(seenTemplate); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("template %q should be removed after create; stat err=%v", seenTemplate, err)
	}
	if listCalls != 2 {
		t.Fatalf("listCalls = %d, want 2 (pre-create absent + post-create verify)", listCalls)
	}
}

func TestInitCreatePostListEmptyFailsClosed(t *testing.T) {
	fr := &fakeRunner{respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
		if cmd.Args[0] == "list" {
			return stdoutResult(""), nil
		}
		return exitResult(0, "", ""), nil
	}}
	a := New(fr)

	_, err := a.Init(context.Background(), InitOptions{OperatorUser: "operator"})
	var lerr *Error
	if !errors.As(err, &lerr) || lerr.Kind != KindPostconditionFailed {
		t.Fatalf("want KindPostconditionFailed, got %v", err)
	}
}

func TestInitCreatePostListIncompatibleFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"mounts", fixtureIncompatibleMountsJSON(InstanceName, "Stopped")},
		{"forwardAgent", fixtureIncompatibleForwardAgentJSON(InstanceName, "Stopped")},
		{"digest", fixtureIncompatibleDigestJSON(InstanceName, "Stopped")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			listCalls := 0
			fr := &fakeRunner{respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
				if cmd.Args[0] == "list" {
					listCalls++
					if listCalls == 1 {
						return stdoutResult(""), nil
					}
					return stdoutResult(tc.json), nil
				}
				return exitResult(0, "", ""), nil
			}}
			a := New(fr)

			_, err := a.Init(context.Background(), InitOptions{OperatorUser: "operator"})
			var lerr *Error
			if !errors.As(err, &lerr) || lerr.Kind != KindPostconditionFailed {
				t.Fatalf("want KindPostconditionFailed, got %v", err)
			}
		})
	}
}

func TestVerifyCompatibleConfigRejectsExtraImage(t *testing.T) {
	rec := &instanceRecord{
		Name: InstanceName,
		Config: &instanceConfig{
			VMType: "vz",
			Arch:   "aarch64",
			Images: []struct {
				Location string `json:"location"`
				Digest   string `json:"digest"`
			}{
				{Location: PromotedImageURL, Digest: PromotedImageDigest},
				{Location: "https://example.com/other.img", Digest: "sha256:bad"},
			},
			Mounts: nil,
		},
	}
	rec.Config.SSH.ForwardAgent = false
	if err := verifyCompatibleConfig(rec); err == nil {
		t.Fatal("want error for two images")
	}
}

func TestInitAlreadyCompatibleIsIdempotent(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureCompatibleInstanceJSON(InstanceName, "Stopped"))},
	}}
	a := New(fr)

	res, err := a.Init(context.Background(), InitOptions{OperatorUser: "operator"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if res.Created {
		t.Fatalf("Created = true, want false (idempotent)")
	}
	if fr.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1 (must not create)", fr.callCount())
	}
	if got := fr.callArgs(0); !equalArgs(got, []string{"list", "--json", "--tty=false"}) {
		t.Fatalf("argv = %v, want list --json", got)
	}
}

func TestInitExistingIncompatibleFailsClosed(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureIncompatibleMountsJSON(InstanceName, "Stopped"))},
	}}
	a := New(fr)

	_, err := a.Init(context.Background(), InitOptions{OperatorUser: "operator"})
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindIncompatible {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindIncompatible)
	}
	if fr.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1 (must not recreate)", fr.callCount())
	}
}

func TestInitIncompatibleForwardAgentFailsClosed(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureIncompatibleForwardAgentJSON(InstanceName, "Running"))},
	}}
	a := New(fr)

	_, err := a.Init(context.Background(), InitOptions{OperatorUser: "operator"})
	var lerr *Error
	if !errors.As(err, &lerr) || lerr.Kind != KindIncompatible {
		t.Fatalf("want KindIncompatible, got %v", err)
	}
}

func TestInitIncompatibleImageDigestFailsClosed(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureIncompatibleDigestJSON(InstanceName, "Stopped"))},
	}}
	a := New(fr)

	_, err := a.Init(context.Background(), InitOptions{OperatorUser: "operator"})
	var lerr *Error
	if !errors.As(err, &lerr) || lerr.Kind != KindIncompatible {
		t.Fatalf("want KindIncompatible, got %v", err)
	}
}

func TestInitMalformedListOutput(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("{not-json")},
	}}
	a := New(fr)

	_, err := a.Init(context.Background(), InitOptions{OperatorUser: "operator"})
	var lerr *Error
	if !errors.As(err, &lerr) || lerr.Kind != KindMalformedOutput {
		t.Fatalf("want KindMalformedOutput, got %v", err)
	}
}

func TestInitBinaryMissing(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{err: errors.New("exec: limactl: executable file not found in $PATH")},
	}}
	a := New(fr)

	_, err := a.Init(context.Background(), InitOptions{OperatorUser: "operator"})
	var lerr *Error
	if !errors.As(err, &lerr) || lerr.Kind != KindBinaryUnavailable {
		t.Fatalf("want KindBinaryUnavailable, got %v", err)
	}
}

func TestInitTimeout(t *testing.T) {
	fr := &fakeRunner{respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
		return execx.Result{}, context.DeadlineExceeded
	}}
	a := New(fr)

	_, err := a.Init(context.Background(), InitOptions{OperatorUser: "operator"})
	var lerr *Error
	if !errors.As(err, &lerr) || lerr.Kind != KindTimeout {
		t.Fatalf("want KindTimeout, got %v", err)
	}
}

func TestInitCreateFailureCleansTemplate(t *testing.T) {
	var templatePath string
	fr := &fakeRunner{respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
		if cmd.Args[0] == "list" {
			return stdoutResult(""), nil
		}
		templatePath = cmd.Args[3]
		return exitResult(1, "", "create failed"), nil
	}}
	a := New(fr)

	_, err := a.Init(context.Background(), InitOptions{OperatorUser: "operator"})
	var lerr *Error
	if !errors.As(err, &lerr) || lerr.Kind != KindCommandFailed {
		t.Fatalf("want KindCommandFailed, got %v", err)
	}
	if templatePath == "" {
		t.Fatal("expected template path")
	}
	if _, err := os.Stat(templatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("template should be cleaned after failed create; stat=%v", err)
	}
}

func TestInitTemplateHasNoMountsAndPinnedImage(t *testing.T) {
	body := renderedTemplateForTest(t, InitOptions{OperatorUser: "alice", CPUs: 4, Memory: "8GiB", Disk: "60GiB"})
	if strings.Contains(body, "mounts:\n  -") || strings.Contains(body, "location: \"~") {
		t.Fatalf("template appears to declare host mounts:\n%s", body)
	}
	if !strings.Contains(body, "mounts: []") {
		t.Fatalf("template missing empty mounts")
	}
	if !strings.Contains(body, PromotedImageURL) || !strings.Contains(body, PromotedImageDigest) {
		t.Fatalf("template missing promoted image pin")
	}
	if !strings.Contains(body, `OPERATOR_USER="alice"`) {
		t.Fatalf("operator not substituted: %s", body)
	}
	if strings.Contains(body, "docker.io") || strings.Contains(body, "usermod -aG docker") {
		t.Fatalf("template must not install/rootful-docker hermes")
	}
}

func TestInitRejectsEmptyOperator(t *testing.T) {
	a := New(&fakeRunner{})
	_, err := a.Init(context.Background(), InitOptions{OperatorUser: "  "})
	var lerr *Error
	if !errors.As(err, &lerr) || lerr.Kind != KindVerificationFailed {
		t.Fatalf("want KindVerificationFailed for empty operator, got %v", err)
	}
	if a.Runner.(*fakeRunner).callCount() != 0 {
		t.Fatalf("must not call limactl before operator validation")
	}
}

func TestInitDefaultsResources(t *testing.T) {
	body := renderedTemplateForTest(t, InitOptions{OperatorUser: "bob"})
	if !strings.Contains(body, "cpus: 4\n") {
		t.Fatalf("default cpus missing:\n%s", body)
	}
	if !strings.Contains(body, "memory: 8GiB\n") {
		t.Fatalf("default memory missing:\n%s", body)
	}
	if !strings.Contains(body, "disk: 60GiB\n") {
		t.Fatalf("default disk missing:\n%s", body)
	}
}

func TestInitInterruptedCreateLeavesNoTemplate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fr := &fakeRunner{respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
		if cmd.Args[0] == "list" {
			return stdoutResult(""), nil
		}
		cancel()
		return execx.Result{}, context.Canceled
	}}
	a := New(fr)

	_, err := a.Init(ctx, InitOptions{OperatorUser: "operator"})
	var lerr *Error
	if !errors.As(err, &lerr) || lerr.Kind != KindCancelled {
		t.Fatalf("want KindCancelled, got %v", err)
	}
	// Best-effort: any leftover torio-lima-*.yaml in temp would be a leak;
	// scan a short window of the process temp dir created during this test.
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "torio-lima-*.yaml"))
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) < 2*time.Second {
			t.Fatalf("leaked template file %q", m)
		}
	}
}

func renderedTemplateForTest(t *testing.T, opts InitOptions) string {
	t.Helper()
	text, err := renderTemplate(opts)
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	return string(text)
}

func fixtureCompatibleInstanceJSON(name, status string) string {
	return `{"name":"` + name + `","status":"` + status + `","config":{"vmType":"vz","arch":"aarch64","images":[{"location":"` + PromotedImageURL + `","arch":"aarch64","digest":"` + PromotedImageDigest + `","variant":"server"}],"mounts":[],"ssh":{"forwardAgent":false},"cpus":4,"memory":"8GiB","disk":"60GiB"}}`
}

func fixtureIncompatibleMountsJSON(name, status string) string {
	return `{"name":"` + name + `","status":"` + status + `","config":{"vmType":"vz","arch":"aarch64","images":[{"location":"` + PromotedImageURL + `","digest":"` + PromotedImageDigest + `"}],"mounts":[{"location":"/Users/me","mountPoint":"/Users/me"}],"ssh":{"forwardAgent":false}}}`
}

func fixtureIncompatibleForwardAgentJSON(name, status string) string {
	return `{"name":"` + name + `","status":"` + status + `","config":{"vmType":"vz","arch":"aarch64","images":[{"location":"` + PromotedImageURL + `","digest":"` + PromotedImageDigest + `"}],"mounts":[],"ssh":{"forwardAgent":true}}}`
}

func fixtureIncompatibleDigestJSON(name, status string) string {
	return `{"name":"` + name + `","status":"` + status + `","config":{"vmType":"vz","arch":"aarch64","images":[{"location":"` + PromotedImageURL + `","digest":"sha256:deadbeef"}],"mounts":[],"ssh":{"forwardAgent":false}}}`
}
