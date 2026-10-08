// Package checker executes HTTP checks with bounded concurrency and safe reports.
package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fbottega-dev/ronda-http/internal/config"
)

const maxBodyBytes = 1 << 20

// Options controls scheduling and how token environment variables are resolved.
// Parallel defaults to 4. LookupEnv defaults to os.LookupEnv.
type Options struct {
	Parallel  int
	LookupEnv func(string) (string, bool)
}

// Report contains only safe labels and measurements, never request addresses,
// response bodies, expected text, credentials or underlying transport errors.
type Report struct {
	SchemaVersion int       `json:"schema_version"`
	StartedAt     time.Time `json:"started_at"`
	DurationMS    int64     `json:"duration_ms"`
	Passed        int       `json:"passed"`
	Failed        int       `json:"failed"`
	Canceled      bool      `json:"canceled"`
	Results       []Result  `json:"results"`
}

// Result preserves the position and name of a target in the configuration.
type Result struct {
	ID       string    `json:"id,omitempty"`
	Name     string    `json:"name"`
	Passed   bool      `json:"passed"`
	Attempts []Attempt `json:"attempts"`
}

// Attempt records one check. A canceled entry with zero duration also represents
// work canceled before a request started (including cancellation during backoff).
// Status is zero when no HTTP response was received.
type Attempt struct {
	Status     int    `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

// Run requires a configuration normalized by config.Load or config.Validate.
// Tokens for all targets are resolved before any network request. Target failures
// are recorded in Report; an error indicates an invalid option or missing token.
func Run(ctx context.Context, cfg config.Config, opts Options) (Report, error) {
	parallel := opts.Parallel
	if parallel == 0 {
		parallel = 4
	}
	if parallel < 1 || parallel > 8 {
		return Report{}, errors.New("parallel deve estar entre 1 e 8")
	}
	lookup := opts.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	tokens := make([]string, len(cfg.Targets))
	for i, target := range cfg.Targets {
		if target.TokenEnv == "" {
			continue
		}
		token, present := lookup(target.TokenEnv)
		if !present || strings.TrimSpace(token) == "" {
			return Report{}, fmt.Errorf("destino %d: variável de token ausente ou vazia", i+1)
		}
		// Header control characters are rejected here, so a bad token cannot cause
		// some unrelated targets to run before the configuration error is reported.
		if strings.ContainsFunc(token, func(r rune) bool { return r < 32 || r == 127 }) {
			return Report{}, fmt.Errorf("destino %d: variável de token contém caracteres inválidos", i+1)
		}
		tokens[i] = token
	}

	started := time.Now()
	report := Report{
		SchemaVersion: 1,
		StartedAt:     started.UTC(),
		Results:       make([]Result, len(cfg.Targets)),
	}
	// Build our own transport: a caller changing http.DefaultTransport must not
	// accidentally disable certificate verification for these checks.
	transport := &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		DialContext:            (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           parallel * 2,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: 1 << 20,
	}
	defer transport.CloseIdleConnections()
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(parallel, len(cfg.Targets)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				// Each worker owns a distinct result slot; the aggregation starts
				// only after Wait, so neither a mutex nor result sorting is needed.
				report.Results[index] = checkTarget(ctx, cfg.Targets[index], tokens[index], transport)
			}
		}()
	}

schedule:
	for index := range cfg.Targets {
		if ctx.Err() != nil {
			break
		}
		select {
		case jobs <- index:
		case <-ctx.Done():
			break schedule
		}
	}
	close(jobs)
	workers.Wait()
	for i := range report.Results {
		result := &report.Results[i]
		if len(result.Attempts) == 0 {
			*result = Result{ID: cfg.Targets[i].ID, Name: cfg.Targets[i].Name, Attempts: []Attempt{canceledAttempt()}}
		}
		if result.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
		if result.Attempts[len(result.Attempts)-1].Code == "canceled" {
			report.Canceled = true
		}
	}
	report.Canceled = report.Canceled || ctx.Err() != nil
	report.DurationMS = time.Since(started).Milliseconds()
	return report, nil
}

func checkTarget(ctx context.Context, target config.Target, token string, transport *http.Transport) Result {
	result := Result{ID: target.ID, Name: target.Name, Attempts: make([]Attempt, 0, target.Retries+1)}
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(target.TimeoutMS) * time.Millisecond,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for attemptNumber := 0; attemptNumber <= target.Retries; attemptNumber++ {
		if ctx.Err() != nil {
			result.Attempts = append(result.Attempts, canceledAttempt())
			break
		}
		attempt, retryable := checkOnce(ctx, client, target, token)
		result.Attempts = append(result.Attempts, attempt)
		result.Passed = attempt.Code == "ok"
		if result.Passed || !retryable || attemptNumber == target.Retries {
			break
		}
		timer := time.NewTimer(time.Duration(attemptNumber+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			result.Attempts = append(result.Attempts, canceledAttempt())
			return result
		case <-timer.C:
		}
	}
	return result
}

func checkOnce(ctx context.Context, client *http.Client, target config.Target, token string) (Attempt, bool) {
	started := time.Now()
	finish := func(status int, code, message string, retryable bool) (Attempt, bool) {
		elapsed := time.Since(started)
		// Use the same, unrounded duration for the decision and its display.
		// For example, 10.9 ms exceeds a 10 ms limit even though it displays 10.
		if code == "ok" && target.MaxLatencyMS > 0 && elapsed > time.Duration(target.MaxLatencyMS)*time.Millisecond {
			code, message, retryable = "latency", "Tempo da resposta acima do limite configurado.", status >= 500
		}
		return Attempt{Status: status, DurationMS: elapsed.Milliseconds(), Code: code, Message: message}, retryable
	}
	request, err := http.NewRequestWithContext(ctx, target.Method, target.URL, nil)
	if err != nil {
		return finish(0, "network", "Não foi possível preparar a requisição.", false)
	}
	request.Header.Set("User-Agent", "ronda-http")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		code, message, retryable := classifyError(ctx, err)
		return finish(0, code, message, retryable)
	}
	defer response.Body.Close()
	status := response.StatusCode
	if !slices.Contains(target.ExpectStatus, status) {
		return finish(status, "status", "Status HTTP diferente do esperado.", status >= 500)
	}
	if target.Contains != "" {
		body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
		if err != nil {
			code, message, retryable := classifyError(ctx, err)
			return finish(status, code, message, retryable)
		}
		if len(body) > maxBodyBytes {
			return finish(status, "body_limit", "Corpo da resposta excede o limite de 1 MiB.", status >= 500)
		}
		if !strings.Contains(string(body), target.Contains) {
			return finish(status, "content", "Texto esperado não encontrado na resposta.", status >= 500)
		}
	}
	if ctx.Err() != nil {
		return finish(status, "canceled", "Verificação cancelada.", false)
	}
	return finish(status, "ok", "Verificação aprovada.", false)
}

func classifyError(ctx context.Context, err error) (code, message string, retryable bool) {
	if ctx.Err() != nil {
		return "canceled", "Verificação cancelada.", false
	}
	var verification *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var invalidCertificate x509.CertificateInvalidError
	var hostname x509.HostnameError
	var record tls.RecordHeaderError
	if errors.As(err, &verification) || errors.As(err, &unknownAuthority) || errors.As(err, &invalidCertificate) || errors.As(err, &hostname) || errors.As(err, &record) {
		return "tls", "Não foi possível validar a conexão TLS.", false
	}
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
		return "timeout", "Tempo limite da requisição excedido.", true
	}
	return "network", "Falha de conexão ou de leitura da resposta.", true
}

func canceledAttempt() Attempt {
	return Attempt{Code: "canceled", Message: "Verificação cancelada."}
}
