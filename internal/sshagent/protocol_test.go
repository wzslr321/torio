package sshagent

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

// msgSignResponse is defined here and not in the package.
//
// The proxy forwards an approved signature without looking at it, so production
// code has no reason to name this number. Only a fake agent does.
const msgSignResponse = 14

// The golden pair below was produced by OpenSSH itself, not by this package:
//
//	ssh-keygen -t ed25519 -N '' -C torio-test-key -f k
//	ssh-keygen -lf k.pub
//	  256 SHA256:453QtO4nWnVBB8P7WEvUS9HGshG6/XJgoa3Y3IKs+B4 torio-test-key (ED25519)
//
// It is the point of the test: an operator pins a key by pasting what `ssh-add
// -l` printed, so Fingerprint must agree with OpenSSH and not merely with
// itself.
const (
	goldenKeyBlobBase64 = "AAAAC3NzaC1lZDI1NTE5AAAAIMIHKY2H6lvzQpUyj4zFDKqexXM5iw3spXMwK1OIVoRp"
	goldenFingerprint   = "SHA256:453QtO4nWnVBB8P7WEvUS9HGshG6/XJgoa3Y3IKs+B4"
)

func goldenIdentity(t *testing.T) Identity {
	t.Helper()
	blob, err := base64.StdEncoding.DecodeString(goldenKeyBlobBase64)
	if err != nil {
		t.Fatalf("decode golden key blob: %v", err)
	}
	return Identity{Blob: blob, Comment: "torio-test-key"}
}

func TestFingerprintMatchesOpenSSH(t *testing.T) {
	if got := goldenIdentity(t).Fingerprint(); got != goldenFingerprint {
		t.Errorf("Fingerprint() = %q, ssh-keygen -lf says %q", got, goldenFingerprint)
	}
}

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	want := frame{typ: msgSignRequest, body: []byte("body")}
	if err := writeFrame(&buf, want); err != nil {
		t.Fatalf("writeFrame() error = %v", err)
	}
	got, err := readFrame(&buf)
	if err != nil {
		t.Fatalf("readFrame() error = %v", err)
	}
	if got.typ != want.typ || !bytes.Equal(got.body, want.body) {
		t.Errorf("round trip = %v %q, want %v %q", got.typ, got.body, want.typ, want.body)
	}
}

// TestReadFrameRefusesAnOversizedFrame proves the length prefix is not an
// allocation instruction. The socket this reads from is one Torio hands a guest
// on purpose, so a declared length is an untrusted number.
func TestReadFrameRefusesAnOversizedFrame(t *testing.T) {
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.BigEndian, uint32(maxFrame+1)); err != nil {
		t.Fatalf("write length: %v", err)
	}
	if _, err := readFrame(&buf); err == nil || !strings.Contains(err.Error(), "protocol maximum") {
		t.Errorf("readFrame() error = %v, want the protocol maximum refusal", err)
	}
}

func TestReadFrameRefusesAnEmptyFrame(t *testing.T) {
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.BigEndian, uint32(0)); err != nil {
		t.Fatalf("write length: %v", err)
	}
	if _, err := readFrame(&buf); err == nil {
		t.Error("readFrame() accepted a frame with no message type")
	}
}

func TestReadFrameReportsATruncatedBody(t *testing.T) {
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.BigEndian, uint32(16)); err != nil {
		t.Fatalf("write length: %v", err)
	}
	buf.WriteString("short")
	if _, err := readFrame(&buf); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("readFrame() error = %v, want ErrUnexpectedEOF", err)
	}
}

func TestIdentitiesRoundTrip(t *testing.T) {
	want := []Identity{
		{Blob: []byte("blob-one"), Comment: "one"},
		{Blob: []byte("blob-two"), Comment: ""},
	}
	got, err := parseIdentities(encodeIdentities(want))
	if err != nil {
		t.Fatalf("parseIdentities() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("parseIdentities() returned %d identities, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i].Blob, want[i].Blob) || got[i].Comment != want[i].Comment {
			t.Errorf("identity %d = %q/%q, want %q/%q", i, got[i].Blob, got[i].Comment, want[i].Blob, want[i].Comment)
		}
	}
}

// TestParseIdentitiesRefusesAnInflatedCount proves a declared count is not an
// allocation budget: each identity costs two length prefixes at minimum, so a
// count past that is malformed rather than large.
func TestParseIdentitiesRefusesAnInflatedCount(t *testing.T) {
	body := binary.BigEndian.AppendUint32(nil, 1<<30)
	if _, err := parseIdentities(body); err == nil || !strings.Contains(err.Error(), "exceeds the message") {
		t.Errorf("parseIdentities() error = %v, want the count refusal", err)
	}
}

func TestParseIdentitiesReportsATruncatedList(t *testing.T) {
	body := encodeIdentities([]Identity{{Blob: []byte("blob"), Comment: "c"}})
	if _, err := parseIdentities(body[:len(body)-3]); !errors.Is(err, errTruncated) {
		t.Errorf("parseIdentities() error = %v, want errTruncated", err)
	}
}

// TestSignRequestKeyStopsAtTheKey proves the signable data is parsed past and
// never returned. What a signature is made over is a Git protocol exchange, and
// this package has no reason to hold one.
func TestSignRequestKeyStopsAtTheKey(t *testing.T) {
	body := appendString(nil, []byte("the-key"))
	body = appendString(body, []byte("the-data-under-signature"))
	body = binary.BigEndian.AppendUint32(body, 0)

	key, err := signRequestKey(body)
	if err != nil {
		t.Fatalf("signRequestKey() error = %v", err)
	}
	if string(key) != "the-key" {
		t.Errorf("signRequestKey() = %q, want %q", key, "the-key")
	}
}
