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

	if err := a.CopyToGuest(context.Background(), "/private/tmp/torio-brain-import-123/payload", HermesHome+"/.torio-brain-import-staging/payload", HermesHome); err != nil {
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

// TestCopyAcceptsTheOwningIdentitysStaging is the case that was impossible
// while the boundary was one fixed home. `brain import` on the second backend
// stages under that backend's own home, and the transport refused it — found
// the first time that backend's journey ran on a real guest.
func TestCopyAcceptsTheOwningIdentitysStaging(t *testing.T) {
	const home = "/home/claude"
	fr := &fakeRunner{script: []scriptedResponse{{result: exitResult(0, "", "")}}}

	if err := New(fr).CopyToGuest(context.Background(),
		"/private/tmp/torio-brain-import-123/payload",
		home+"/.torio-brain-import-staging/payload", home); err != nil {
		t.Fatalf("CopyToGuest: %v", err)
	}
	want := []string{
		"copy",
		"/private/tmp/torio-brain-import-123/payload/",
		InstanceName + ":" + home + "/.torio-brain-import-staging/payload/",
	}
	if got := fr.callArgs(0); !equalArgs(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
}

func TestCopyRejectsPathsOutsideTheTypedBoundary(t *testing.T) {
	const otherHome = "/home/claude"
	cases := []struct {
		name  string
		host  string
		guest string
		home  string
	}{
		{"relative host source", "relative", HermesHome + "/staging", HermesHome},
		{"host root", "/", HermesHome + "/staging", HermesHome},
		{"guest outside the identity home", "/private/tmp/staging", "/tmp/staging", HermesHome},
		{"guest home itself", "/private/tmp/staging", HermesHome, HermesHome},
		{"guest traversal", "/private/tmp/staging", HermesHome + "/../operator", HermesHome},
		{"guest remote syntax", "/private/tmp/staging", HermesHome + "/bad:target", HermesHome},
		// The boundary is one identity's home, not any home: a transfer for one
		// backend must not be accepted into the other's, which is where the
		// fixed root used to put it.
		{"another identity's home", "/private/tmp/staging", HermesHome + "/staging", otherHome},
		{"sibling by prefix", "/private/tmp/staging", otherHome + "-other/staging", otherHome},
		{"boundary is not absolute", "/private/tmp/staging", otherHome + "/staging", "home/claude"},
		{"boundary is the root", "/private/tmp/staging", "/staging", "/"},
		{"boundary carries remote syntax", "/private/tmp/staging", otherHome + "/staging", "/home/cl:aude"},
		{"boundary is unclean", "/private/tmp/staging", otherHome + "/staging", "/home/claude/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fr := &fakeRunner{}
			a := New(fr)
			if err := a.CopyToGuest(context.Background(), tc.host, tc.guest, tc.home); err == nil {
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
			err := New(fr).CopyToGuest(context.Background(), host, guest, HermesHome)
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

// The vault has to come back out for two boxes to agree on it (ADR-0025), and
// it comes out the same way it went in: one bounded transport call, the guest
// side named as a contained descendant of the owning identity's home.
func TestCopyFromGuestUsesTheMirroredArgv(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{{result: exitResult(0, "", "")}}}
	a := New(fr)

	if err := a.CopyFromGuest(context.Background(), HermesHome+"/.torio-brain-sync-staging", "/private/tmp/torio-brain-sync-456", HermesHome); err != nil {
		t.Fatalf("CopyFromGuest: %v", err)
	}
	want := []string{
		"copy",
		"torio:/home/hermes/.torio-brain-sync-staging/",
		"/private/tmp/torio-brain-sync-456/",
	}
	if got := fr.callArgs(0); !equalArgs(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
}

// The same boundary in the same direction: a guest path outside the owning
// identity's home is refused before anything is rendered into Lima's
// colon-based remote syntax.
func TestCopyFromGuestRefusesAGuestPathOutsideTheIdentityHome(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{{result: exitResult(0, "", "")}}}
	a := New(fr)

	for _, guestDir := range []string{
		"/tmp/anything",
		HermesHome,
		"/home/other/.torio-brain-sync-staging",
		HermesHome + "/../etc",
	} {
		if err := a.CopyFromGuest(context.Background(), guestDir, "/private/tmp/torio-brain-sync-456", HermesHome); err == nil {
			t.Errorf("CopyFromGuest(%q) was accepted", guestDir)
		}
	}
	if len(fr.calls) != 0 {
		t.Errorf("the transport ran %d times for refused paths", len(fr.calls))
	}
}
