package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

func TestCodeReceiverShowsAuthorizationURLAndReturnsCallback(t *testing.T) {
	var output bytes.Buffer
	receiver := newCodeReceiver(&output)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result := make(chan *auth.AuthorizationResult, 1)
	errs := make(chan error, 1)
	go func() {
		got, err := receiver.Fetch(ctx, &auth.AuthorizationArgs{URL: "https://auth.example.test/authorize?state=expected"})
		result <- got
		errs <- err
	}()

	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "http://localhost:43119/callback?code=one-time-code&state=expected&iss=https%3A%2F%2Fauth.example.test", nil)
	receiver.Handler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("callback status = %d", response.Code)
	}
	if err := <-errs; err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got := <-result
	if got.Code != "one-time-code" || got.State != "expected" || got.Iss != "https://auth.example.test" {
		t.Fatalf("authorization result = %#v", got)
	}
	if shown := output.String(); !strings.Contains(shown, "https://auth.example.test/authorize") {
		t.Fatalf("operator output = %q, want authorization URL", shown)
	}
}

func TestCodeReceiverRefusesWrongPathAndIncompleteCallback(t *testing.T) {
	receiver := newCodeReceiver(&bytes.Buffer{})
	for _, target := range []string{
		"http://localhost:43119/not-callback?code=x&state=y",
		"http://localhost:43119/callback?state=y",
		"http://localhost:43119/callback?code=x",
	} {
		response := httptest.NewRecorder()
		receiver.Handler().ServeHTTP(response, httptest.NewRequest("GET", target, nil))
		if response.Code == 200 {
			t.Errorf("callback %s succeeded", target)
		}
	}
}
