package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestShutdownWaitsForActiveRequest(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			t.Error("active request canceled during graceful shutdown")
		}
		w.WriteHeader(201)
		close(finished)
	}))
	defer server.Close()
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	shutdownStarted := make(chan struct{})
	server.Config.RegisterOnShutdown(func() { close(shutdownStarted) })
	requestDone := make(chan error, 1)
	go func() {
		response, err := server.Client().Get(server.URL)
		if err == nil {
			defer response.Body.Close()
			_, err = io.Copy(io.Discard, response.Body)
			if response.StatusCode != 201 {
				err = errors.New("unexpected status")
			}
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not enter the handler")
	}
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- shutdownHTTP(server.Config, time.Second) }()
	select {
	case <-shutdownStarted:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not start")
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before request completion: %v", err)
	default:
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-requestDone; err != nil {
		t.Fatal(err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatal(err)
	}
	<-finished
}

func TestShutdownDeadlineForcesClose(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()
	requestDone := make(chan error, 1)
	go func() {
		response, err := server.Client().Get(server.URL)
		if response != nil {
			response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not enter the handler")
	}
	started := time.Now()
	err := shutdownHTTP(server.Config, 30*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("shutdown err=%v elapsed=%v", err, time.Since(started))
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("forced close did not cancel the request")
	}
	if err := <-requestDone; err == nil {
		t.Fatal("forced request unexpectedly completed")
	}
}
