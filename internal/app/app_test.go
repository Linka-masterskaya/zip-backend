package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
)

func TestShutdownClosesInfrastructureAfterHTTPDeadline(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	defer close(releaseRequest)

	apiSrv := &http.Server{
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(requestStarted)
			<-releaseRequest
		}),
		ReadHeaderTimeout: time.Second,
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = apiSrv.Close() }()

	serveDone := make(chan error, 1)
	go func() { serveDone <- serveHTTPListener(apiSrv, ln) }()

	requestDone := make(chan error, 1)
	go func() {
		req, requestErr := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+ln.Addr().String(), nil)
		if requestErr != nil {
			requestDone <- requestErr
			return
		}
		resp, requestErr := http.DefaultClient.Do(req)
		if requestErr == nil {
			_ = resp.Body.Close()
		}
		requestDone <- requestErr
	}()

	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach handler")
	}

	infraClosed := make(chan struct{})
	closer := &Closer{}
	closer.Add("test", func(context.Context) error {
		close(infraClosed)
		return nil
	})
	a := &App{
		cfg: &config.Config{Server: config.ServerConfig{
			ShutdownTimeout: 20 * time.Millisecond,
		}},
		closer:     closer,
		apiSrv:     apiSrv,
		metricsSrv: &http.Server{},
	}

	err = a.shutdown()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown() = %v, want deadline exceeded", err)
	}
	select {
	case <-infraClosed:
	default:
		t.Fatal("infrastructure was not closed after HTTP shutdown deadline")
	}
}
