// Package demo provides a local HTTP service for trying Ronda HTTP checks.
package demo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Handler returns a stateless service with predictable example responses.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			respond(w, r, http.StatusMethodNotAllowed, `{"error":"method_not_allowed"}`)
			return
		}

		switch r.URL.Path {
		case "/health":
			respond(w, r, http.StatusOK, `{"status":"ok"}`)
		case "/catalog":
			respond(w, r, http.StatusOK, `{"products":[{"id":1,"name":"Caderno","price":19.90,"currency":"BRL"}]}`)
		case "/private":
			respond(w, r, http.StatusUnauthorized, `{"error":"unauthorized"}`)
		case "/moved":
			w.Header().Set("Location", "/health")
			respond(w, r, http.StatusFound, `{"location":"/health"}`)
		case "/unavailable":
			respond(w, r, http.StatusServiceUnavailable, `{"error":"service_unavailable"}`)
		case "/slow":
			timer := time.NewTimer(600 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
				return
			case <-timer.C:
				respond(w, r, http.StatusOK, `{"status":"ok","delay_ms":600}`)
			}
		default:
			respond(w, r, http.StatusNotFound, `{"error":"not_found"}`)
		}
	})
}

func respond(w http.ResponseWriter, r *http.Request, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)+1))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body+"\n")
	}
}

// Serve runs the demonstration service until ctx is canceled. It accepts only
// literal loopback IP addresses and ports from 1 to 65535, so the demonstration
// cannot accidentally be exposed to the network. Cancellation is a clean exit.
func Serve(ctx context.Context, addr string, out io.Writer) error {
	if err := validateAddress(addr); err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("iniciar demonstração em %s: %w", addr, err)
	}
	defer listener.Close()
	if out == nil {
		out = io.Discard
	}
	if _, err := fmt.Fprintf(out, "Demonstração em http://%s\nPressione Ctrl+C para encerrar.\n", listener.Addr()); err != nil {
		return fmt.Errorf("informar endereço da demonstração: %w", err)
	}

	server := &http.Server{
		Handler:           Handler(),
		ReadHeaderTimeout: 3 * time.Second,
		IdleTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Second,
	}
	finished := make(chan error, 1)
	go func() {
		finished <- server.Serve(listener)
	}()

	select {
	case err := <-finished:
		return serveError(err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			_ = server.Close()
		}
		serveErr := <-finished
		if shutdownErr != nil {
			return fmt.Errorf("encerrar demonstração: %w", shutdownErr)
		}
		return serveError(serveErr)
	}
}

func validateAddress(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("endereço de demonstração inválido: use 127.0.0.1:8080 ou [::1]:8080")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("a demonstração exige um IP de loopback: 127.0.0.1 ou ::1")
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return fmt.Errorf("a porta da demonstração deve estar entre 1 e 65535")
	}
	return nil
}

func serveError(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("executar demonstração: %w", err)
	}
	return nil
}
