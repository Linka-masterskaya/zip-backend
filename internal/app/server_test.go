package app

import (
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeHTTPTreatsServerClosedAsSuccess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: http.NewServeMux(), ReadHeaderTimeout: time.Second}

	done := make(chan error, 1)
	go func() { done <- serveHTTPListener(srv, ln) }()

	time.Sleep(50 * time.Millisecond)
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveHTTPListener after Close = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveHTTPListener did not return after Close")
	}
}

func TestServeHTTPReturnsBindError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	srv := &http.Server{Addr: ln.Addr().String(), Handler: http.NewServeMux(), ReadHeaderTimeout: time.Second}

	err = serveHTTP(srv)
	if err == nil {
		t.Fatal("serveHTTP on a busy port = nil, want bind error")
	}
	if errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serveHTTP = %v, want a bind error, not ErrServerClosed", err)
	}
}
