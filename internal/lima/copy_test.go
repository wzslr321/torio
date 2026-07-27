package lima

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
)

func TestCopyToGuestUsesPromotedExactArgv(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{{result: exitResult(0, "", "")}}}
	a := New(fr)

	if err := a.CopyToGuest(context.Background(), "/private/tmp/torio-brain-import-123/payload", HermesHome+"/.torio-brain-import-staging/payload"); err != nil {
		t.Fatalf("CopyToGuest: %v", err)
	}
	want := []string{
		"copy",
		"/private/tmp/torio-brain-import-123/payload/",
		"torio:/home/hermes/.torio-brain-import-staging/payload/",
	}
	if got := fr.callArgs(0); !equalArgs(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
}

func TestCopyFromGuestUsesPromotedExactArgv(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{{result: exitResult(0, "", "")}}}
	a := New(fr)

	if err := a.CopyFromGuest(context.Background(), HermesHome+"/.torio-brain-export-staging/payload", "/private/tmp/torio-brain-export-123/payload"); err != nil {
		t.Fatalf("CopyFromGuest: %v", err)
	}
	want := []string{
		"copy",
		"torio:/home/hermes/.torio-brain-export-staging/payload/",
		"/private/tmp/torio-brain-export-123/payload/",
	}
	if got := fr.callArgs(0); !equalArgs(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
}

func TestCopyRejectsPathsOutsideTheTypedBoundary(t *testing.T) {
	cases := []struct {
		name  string
		toVM  bool
		host  string
		guest string
	}{
		{"relative host source", true, "relative", HermesHome + "/staging"},
		{"relative host destination", false, "relative", HermesHome + "/staging"},
		{"host root", true, "/", HermesHome + "/staging"},
		{"guest outside hermes home", true, "/private/tmp/staging", "/tmp/staging"},
		{"guest home itself", true, "/private/tmp/staging", HermesHome},
		{"guest traversal", true, "/private/tmp/staging", HermesHome + "/../operator"},
		{"guest remote syntax", false, "/private/tmp/staging", HermesHome + "/bad:target"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fr := &fakeRunner{}
			a := New(fr)
			var err error
			if tc.toVM {
				err = a.CopyToGuest(context.Background(), tc.host, tc.guest)
			} else {
				err = a.CopyFromGuest(context.Background(), tc.guest, tc.host)
			}
			if err == nil {
				t.Fatal("copy accepted a path outside its typed boundary")
			}
			if fr.callCount() != 0 {
				t.Fatalf("runner called %d times for invalid input", fr.callCount())
			}
		})
	}
}

func TestCopyFailureNeverReturnsTransportOutputOrPaths(t *testing.T) {
	const privateMarker = "private-vault-customer-name"
	host := "/private/tmp/" + privateMarker
	guest := HermesHome + "/.torio-brain-import-staging"

	cases := []struct {
		name string
		resp scriptedResponse
	}{
		{
			name: "nonzero exit",
			resp: scriptedResponse{result: exitResult(11, privateMarker, "copy failed at "+privateMarker)},
		},
		{
			name: "runner failure",
			resp: scriptedResponse{err: errors.New("spawn failed for " + privateMarker)},
		},
		{
			name: "timeout",
			resp: scriptedResponse{err: errors.Join(errors.New(privateMarker), context.DeadlineExceeded)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fr := &fakeRunner{script: []scriptedResponse{tc.resp}}
			err := New(fr).CopyToGuest(context.Background(), host, guest)
			if err == nil {
				t.Fatal("copy failure returned nil")
			}
			for _, leak := range []string{privateMarker, host, guest} {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("error leaked %q: %v", leak, err)
				}
			}
			var lerr *Error
			if !errors.As(err, &lerr) {
				t.Fatalf("error type = %T, want *lima.Error", err)
			}
			if tc.name == "timeout" && lerr.Kind != KindTimeout {
				t.Fatalf("timeout kind = %s, want %s", lerr.Kind, KindTimeout)
			}
		})
	}
}

var _ execx.Runner = (*fakeRunner)(nil)
