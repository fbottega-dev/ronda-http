package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandlerRoutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path   string
		status int
		text   string
	}{
		{"/health", http.StatusOK, `"status":"ok"`},
		{"/health?probe=1", http.StatusOK, `"status":"ok"`},
		{"/catalog", http.StatusOK, `"name":"Caderno"`},
		{"/private", http.StatusUnauthorized, `"error":"unauthorized"`},
		{"/moved", http.StatusFound, `"location":"/health"`},
		{"/unavailable", http.StatusServiceUnavailable, `"error":"service_unavailable"`},
		{"/slow", http.StatusOK, `"delay_ms":600`},
		{"/missing", http.StatusNotFound, `"error":"not_found"`},
		{"/health/", http.StatusNotFound, `"error":"not_found"`},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()
			response := httptest.NewRecorder()
			Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if !json.Valid(response.Body.Bytes()) || !strings.Contains(response.Body.String(), test.text) {
				t.Errorf("unexpected JSON body: %s", response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Errorf("Content-Type = %q", got)
			}
			if test.path == "/moved" && response.Header().Get("Location") != "/health" {
				t.Error("redirect must point to /health")
			}
		})
	}
}

func TestHandlerHead(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/health", "/catalog", "/private", "/moved", "/unavailable", "/slow", "/missing"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			handler := Handler()
			get, head := httptest.NewRecorder(), httptest.NewRecorder()
			handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, path, nil))
			handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, path, nil))
			if head.Code != get.Code {
				t.Errorf("HEAD status = %d, GET status = %d", head.Code, get.Code)
			}
			if head.Body.Len() != 0 {
				t.Errorf("HEAD returned a body: %s", head.Body.String())
			}
			for _, name := range []string{"Content-Type", "Content-Length", "Location"} {
				if head.Header().Get(name) != get.Header().Get(name) {
					t.Errorf("HEAD and GET differ in %s", name)
				}
			}
		})
	}
}

func TestHandlerRejectsMethods(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, httptest.NewRequest(method, "/health", nil))
		if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s: status = %d, Allow = %q", method, response.Code, response.Header().Get("Allow"))
		}
		if !json.Valid(response.Body.Bytes()) {
			t.Errorf("%s: response must be JSON", method)
		}
	}
}

func TestSlowHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/slow", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		Handler().ServeHTTP(response, request)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("slow handler did not stop on cancellation")
	}
	if response.Body.Len() != 0 {
		t.Error("canceled request returned a success body")
	}
}

func TestServeRejectsUnsafeAddresses(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{":8080", "0.0.0.0:8080", "[::]:8080", "192.168.1.20:8080", "example.com:8080", "localhost:8080", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:http", "127.0.0.1:-1"} {
		t.Run(addr, func(t *testing.T) {
			var output bytes.Buffer
			if err := Serve(context.Background(), addr, &output); err == nil {
				t.Error("Serve accepted an invalid or non-loopback address")
			}
			if output.Len() != 0 {
				t.Error("Serve announced a rejected address")
			}
		})
	}
}

func TestServeReportsOccupiedPort(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := Serve(context.Background(), listener.Addr().String(), io.Discard); err == nil {
		t.Fatal("Serve accepted an occupied port")
	}
}

func TestAddressAllowsLoopback(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"127.0.0.1:8080", "[::1]:8080"} {
		if err := validateAddress(addr); err != nil {
			t.Errorf("%s: %v", addr, err)
		}
	}
}

func TestServeStartsAndStops(t *testing.T) {
	t.Parallel()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := reservation.Addr().String()
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &startupWriter{ready: make(chan struct{})}
	finished := make(chan error, 1)
	go func() { finished <- Serve(ctx, addr, output) }()
	select {
	case <-output.ready:
	case err := <-finished:
		t.Fatalf("Serve exited before startup: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not announce startup")
	}
	client := &http.Client{
		Timeout:   time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	response, err := client.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("health returned %d", response.StatusCode)
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Serve shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not stop after cancellation")
	}
	if !strings.Contains(output.String(), "Demonstração em http://"+addr) || !strings.Contains(output.String(), "Ctrl+C") {
		t.Errorf("unexpected startup message: %q", output.String())
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("Serve left its listener open: %v", err)
	}
	listener.Close()
}

type startupWriter struct {
	bytes.Buffer
	ready chan struct{}
}

func (w *startupWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	close(w.ready)
	return n, err
}
