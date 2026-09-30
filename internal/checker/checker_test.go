package checker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fbottega-dev/ronda-http/internal/config"
)

func testTarget(address string) config.Target {
	return config.Target{Name: "serviço", URL: address, Method: "GET", ExpectStatus: []int{200}, TimeoutMS: 1000}
}

func runChecks(t *testing.T, ctx context.Context, targets []config.Target, opts Options) Report {
	t.Helper()
	report, err := Run(ctx, config.Config{Version: 1, Targets: targets}, opts)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func lastAttempt(t *testing.T, report Report) Attempt {
	t.Helper()
	if len(report.Results) != 1 || len(report.Results[0].Attempts) == 0 {
		t.Fatalf("resultado incompleto: %+v", report)
	}
	return report.Results[0].Attempts[len(report.Results[0].Attempts)-1]
}

func TestStatusAndLiteralContent(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		expected []int
		body     string
		contains string
		code     string
	}{
		{"status aceito", 204, []int{200, 204}, "", "", "ok"},
		{"status inesperado", 418, []int{200}, "", "", "status"},
		{"texto literal", 200, []int{200}, "antes a.b[1] depois", "a.b[1]", "ok"},
		{"sem expressão regular", 200, []int{200}, "aXb1", "a.b[1]", "content"},
		{"sensível a maiúsculas", 200, []int{200}, "Pronto", "pronto", "content"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			target := testTarget(server.URL)
			target.ExpectStatus, target.Contains = tc.expected, tc.contains
			report := runChecks(t, context.Background(), []config.Target{target}, Options{})
			attempt := lastAttempt(t, report)
			if attempt.Code != tc.code || attempt.Status != tc.status {
				t.Fatalf("tentativa = %+v; código esperado = %s", attempt, tc.code)
			}
			wantPassed := tc.code == "ok"
			if report.Results[0].Passed != wantPassed || report.Passed+report.Failed != 1 || (report.Passed == 1) != wantPassed {
				t.Fatalf("contagens incorretas: %+v", report)
			}
			if report.SchemaVersion != 1 || report.StartedAt.IsZero() || report.DurationMS < 0 || report.Canceled {
				t.Fatalf("metadados incorretos: %+v", report)
			}
		})
	}
}

func TestHEADAndNoBodyReadWithoutContains(t *testing.T) {
	for _, method := range []string{"GET", "HEAD"} {
		t.Run(method, func(t *testing.T) {
			var seenMethod atomic.Value
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seenMethod.Store(r.Method)
				w.Header().Set("Content-Length", "10000")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				// Reading this body would reach the client timeout. Closing it
				// after checking headers cancels the server-side request.
				if method == "GET" {
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			target := testTarget(server.URL)
			target.Method, target.TimeoutMS = method, 100
			report := runChecks(t, context.Background(), []config.Target{target}, Options{})
			if got := lastAttempt(t, report); got.Code != "ok" {
				t.Fatalf("não deveria ler corpo: %+v", got)
			}
			if seenMethod.Load() != method {
				t.Fatalf("método = %s; esperado %s", seenMethod.Load(), method)
			}
		})
	}
}

func TestTimeoutAndLatency(t *testing.T) {
	t.Run("timeout de cabeçalhos", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer server.Close()
		target := testTarget(server.URL)
		target.TimeoutMS = 100
		report := runChecks(t, context.Background(), []config.Target{target}, Options{})
		if got := lastAttempt(t, report); got.Code != "timeout" || got.Status != 0 {
			t.Fatalf("timeout não registrado: %+v", got)
		}
	})
	t.Run("timeout de corpo", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "10000")
			_, _ = io.WriteString(w, "parcial")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		defer server.Close()
		target := testTarget(server.URL)
		target.TimeoutMS, target.Contains = 100, "pronto"
		report := runChecks(t, context.Background(), []config.Target{target}, Options{})
		if got := lastAttempt(t, report); got.Code != "timeout" || got.Status != 200 {
			t.Fatalf("timeout de leitura não registrado: %+v", got)
		}
	})
	t.Run("latência inclui corpo validado", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			time.Sleep(40 * time.Millisecond)
			_, _ = io.WriteString(w, "pronto")
		}))
		defer server.Close()
		target := testTarget(server.URL)
		target.MaxLatencyMS, target.Contains = 10, "pronto"
		report := runChecks(t, context.Background(), []config.Target{target}, Options{})
		if got := lastAttempt(t, report); got.Code != "latency" || got.DurationMS < 40 {
			t.Fatalf("latência não registrada: %+v", got)
		}
	})
}

func TestRedirectNeverFollowedWithToken(t *testing.T) {
	var redirected atomic.Int32
	var authorization atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/destination" {
			redirected.Add(1)
			return
		}
		authorization.Store(r.Header.Get("Authorization"))
		http.Redirect(w, r, "/destination", http.StatusFound)
	}))
	defer server.Close()
	target := testTarget(server.URL)
	target.TokenEnv = "RONDA_TEST_TOKEN"
	opts := Options{LookupEnv: func(string) (string, bool) { return "segredo-do-teste", true }}
	for _, status := range []int{200, 302} {
		target.ExpectStatus = []int{status}
		report := runChecks(t, context.Background(), []config.Target{target}, opts)
		attempt := lastAttempt(t, report)
		if attempt.Status != 302 || report.Results[0].Passed != (status == 302) {
			t.Fatalf("status do redirecionamento perdido: %+v", report)
		}
		encoded, _ := json.Marshal(report)
		if strings.Contains(string(encoded), "segredo-do-teste") {
			t.Fatal("token exposto no relatório")
		}
	}
	if redirected.Load() != 0 || authorization.Load() != "Bearer segredo-do-teste" {
		t.Fatal("redirecionamento seguido ou cabeçalho de autenticação incorreto")
	}
}

func TestTokenPreflightBeforeAnyNetwork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	first, second := testTarget(server.URL), testTarget(server.URL+"/url-secreta")
	second.Name, second.TokenEnv = "autenticado", "RONDA_MISSING"
	for _, tc := range []struct {
		name    string
		value   string
		present bool
	}{
		{"ausente", "", false},
		{"vazio", "", true},
		{"espaços", "  ", true},
		{"injeção de cabeçalho", "segredo\r\nOutro: valor", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Run(context.Background(), config.Config{Version: 1, Targets: []config.Target{first, second}}, Options{
				LookupEnv: func(string) (string, bool) { return tc.value, tc.present },
			})
			if err == nil || requests.Load() != 0 {
				t.Fatal("tokens devem ser resolvidos antes de qualquer requisição")
			}
			if strings.Contains(err.Error(), "url-secreta") || strings.Contains(err.Error(), "segredo") || strings.Contains(err.Error(), server.URL) {
				t.Fatal("erro expôs dados sensíveis")
			}
		})
	}
}

func TestTokenDefaultsToEnvironment(t *testing.T) {
	t.Setenv("RONDA_CHECKER_TEST_TOKEN", "valor-local")
	var authorized atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		authorized.Store(r.Header.Get("Authorization") == "Bearer valor-local")
	}))
	defer server.Close()
	target := testTarget(server.URL)
	target.TokenEnv = "RONDA_CHECKER_TEST_TOKEN"
	report := runChecks(t, context.Background(), []config.Target{target}, Options{})
	if !report.Results[0].Passed || !authorized.Load() {
		t.Fatal("token do ambiente não enviado corretamente")
	}
}

func TestBodyLimit(t *testing.T) {
	for _, size := range []int{maxBodyBytes, maxBodyBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, strings.Repeat("a", size))
			}))
			defer server.Close()
			target := testTarget(server.URL)
			target.Contains = "a"
			report := runChecks(t, context.Background(), []config.Target{target}, Options{})
			want := "ok"
			if size > maxBodyBytes {
				want = "body_limit"
			}
			if got := lastAttempt(t, report); got.Code != want {
				t.Fatalf("limite do corpo: %+v; esperado %s", got, want)
			}
		})
	}
}

func TestRetryPolicy(t *testing.T) {
	cases := []struct {
		name     string
		statuses []int
		expected []int
		contains string
		attempts int
		code     string
	}{
		{"500 seguido de 200", []int{500, 200}, []int{200}, "", 2, "ok"},
		{"500 persistente", []int{500}, []int{200}, "", 3, "status"},
		{"400 sem retry", []int{400}, []int{200}, "", 1, "status"},
		{"conteúdo ausente sem retry", []int{200}, []int{200}, "pronto", 1, "content"},
		{"500 esperado e aprovado", []int{500}, []int{500}, "", 1, "ok"},
		{"500 esperado mas inválido", []int{500}, []int{500}, "pronto", 3, "content"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				index := int(requests.Add(1)) - 1
				w.WriteHeader(tc.statuses[min(index, len(tc.statuses)-1)])
			}))
			defer server.Close()
			target := testTarget(server.URL)
			target.Retries, target.ExpectStatus, target.Contains = 2, tc.expected, tc.contains
			report := runChecks(t, context.Background(), []config.Target{target}, Options{})
			if got := lastAttempt(t, report); got.Code != tc.code || len(report.Results[0].Attempts) != tc.attempts || int(requests.Load()) != tc.attempts {
				t.Fatalf("retry incorreto: %+v; requisições = %d", report, requests.Load())
			}
		})
	}
}

func TestTransportAndTimeoutAreRetried(t *testing.T) {
	for _, kind := range []string{"network", "timeout"} {
		t.Run(kind, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 {
					if kind == "timeout" {
						<-r.Context().Done()
					} else {
						connection, _, err := w.(http.Hijacker).Hijack()
						if err == nil {
							_ = connection.Close()
						}
					}
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			target := testTarget(server.URL)
			target.Retries, target.TimeoutMS = 1, 100
			report := runChecks(t, context.Background(), []config.Target{target}, Options{})
			if !report.Results[0].Passed || len(report.Results[0].Attempts) != 2 || report.Results[0].Attempts[0].Code != kind || requests.Load() != 2 {
				t.Fatalf("falha transitória não recuperada: %+v", report)
			}
		})
	}
}

func TestCanceledContextStartsNoRequests(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	first, second := testTarget(server.URL), testTarget(server.URL)
	second.Name = "segundo"
	report := runChecks(t, ctx, []config.Target{first, second}, Options{})
	if !report.Canceled || report.Failed != 2 || requests.Load() != 0 {
		t.Fatalf("cancelamento não respeitado: %+v", report)
	}
	for _, result := range report.Results {
		if len(result.Attempts) != 1 || result.Attempts[0].Code != "canceled" {
			t.Fatalf("resultado cancelado ausente: %+v", result)
		}
	}
}

func TestCancelActiveRequestAndQueuedTargets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		cancel()
		<-r.Context().Done()
	}))
	defer server.Close()
	first, second := testTarget(server.URL), testTarget(server.URL)
	first.Retries, second.Name = 2, "segundo"
	report := runChecks(t, ctx, []config.Target{first, second}, Options{Parallel: 1})
	if !report.Canceled || report.Failed != 2 || requests.Load() != 1 {
		t.Fatalf("requisição indevida após cancelamento: %+v; requests = %d", report, requests.Load())
	}
	for _, result := range report.Results {
		if result.Attempts[len(result.Attempts)-1].Code != "canceled" {
			t.Fatalf("resultado não cancelado: %+v", result)
		}
	}
}

func TestRetryBackoffIsCancelable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.(http.Flusher).Flush()
		// Cancel after the first response, inside the 100 ms retry interval.
		time.AfterFunc(20*time.Millisecond, cancel)
	}))
	defer server.Close()
	target := testTarget(server.URL)
	target.Retries = 2
	report := runChecks(t, ctx, []config.Target{target}, Options{})
	if !report.Canceled || requests.Load() != 1 || lastAttempt(t, report).Code != "canceled" {
		t.Fatalf("cancelamento durante backoff não respeitado: %+v", report)
	}
}

func TestBoundedConcurrencyAndConfigurationOrder(t *testing.T) {
	for _, parallel := range []int{1, 2, 0} {
		t.Run(fmt.Sprint(parallel), func(t *testing.T) {
			limit := parallel
			if limit == 0 {
				limit = 4
			}
			var active, peak atomic.Int32
			started := make(chan struct{}, 6)
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseAll()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				current := active.Add(1)
				defer active.Add(-1)
				for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
				}
				started <- struct{}{}
				<-release
				// Different outcomes make accidental result reordering visible.
				if r.URL.Path == "/0" {
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			targets := make([]config.Target, 6)
			for i := range targets {
				targets[i] = testTarget(fmt.Sprintf("%s/%d", server.URL, i))
				targets[i].Name = fmt.Sprintf("destino %d", i)
			}
			type outcome struct {
				report Report
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				report, err := Run(context.Background(), config.Config{Version: 1, Targets: targets}, Options{Parallel: parallel})
				done <- outcome{report, err}
			}()
			for range limit {
				select {
				case <-started:
				case <-time.After(2 * time.Second):
					releaseAll()
					t.Fatal("workers não iniciaram")
				}
			}
			select {
			case <-started:
				releaseAll()
				t.Fatal("concorrência excedeu o limite")
			case <-time.After(25 * time.Millisecond):
			}
			releaseAll()
			got := <-done
			if got.err != nil || int(peak.Load()) != limit || got.report.Passed != 5 || got.report.Failed != 1 {
				t.Fatalf("concorrência/contagem incorreta: %+v; pico %d; erro %v", got.report, peak.Load(), got.err)
			}
			for i, result := range got.report.Results {
				if result.Name != targets[i].Name || result.Passed != (i != 0) {
					t.Fatalf("ordem incorreta na posição %d: %+v", i, result)
				}
			}
		})
	}
}

func TestTLSCertificateIsValidated(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	target := testTarget(server.URL + "/caminho-secreto")
	target.Retries = 2
	report := runChecks(t, context.Background(), []config.Target{target}, Options{})
	if got := lastAttempt(t, report); got.Code != "tls" || report.Results[0].Passed || len(report.Results[0].Attempts) != 1 {
		t.Fatalf("certificado não confiável aceito ou repetido: %+v", report)
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), server.URL) || strings.Contains(string(encoded), "caminho-secreto") || strings.Contains(string(encoded), "x509") {
		t.Fatal("erro TLS expôs endereço ou erro interno")
	}
}

func TestNetworkErrorRedactsURLAndSecrets(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + listener.Addr().String() + "/caminho-secreto?key=consulta-secreta"
	_ = listener.Close()
	target := testTarget(address)
	target.TokenEnv, target.Contains = "RONDA_TOKEN", "conteudo-secreto"
	report := runChecks(t, context.Background(), []config.Target{target}, Options{
		LookupEnv: func(string) (string, bool) { return "token-secreto", true },
	})
	if lastAttempt(t, report).Code != "network" {
		t.Fatalf("falha de conexão não registrada: %+v", report)
	}
	encoded, _ := json.Marshal(report)
	for _, secret := range []string{address, "caminho-secreto", "consulta-secreta", "conteudo-secreto", "token-secreto", "dial tcp", "127.0.0.1"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("relatório expôs %q", secret)
		}
	}
}

func TestBodyAndExpectedTextAreNotReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "corpo-confidencial")
	}))
	defer server.Close()
	target := testTarget(server.URL)
	target.Contains = "texto-confidencial"
	report := runChecks(t, context.Background(), []config.Target{target}, Options{})
	encoded, _ := json.Marshal(report)
	if lastAttempt(t, report).Code != "content" || strings.Contains(string(encoded), "confidencial") {
		t.Fatalf("conteúdo exposto ou falha ausente: %s", encoded)
	}
}

func TestInvalidParallel(t *testing.T) {
	for _, parallel := range []int{-1, 9} {
		_, err := Run(context.Background(), config.Config{}, Options{Parallel: parallel})
		if err == nil {
			t.Fatalf("parallel %d deveria falhar", parallel)
		}
	}
}

func TestErrorClassificationDoesNotEchoCause(t *testing.T) {
	code, message, retryable := classifyError(context.Background(), errors.New("erro com senha-confidencial"))
	if code != "network" || !retryable || strings.Contains(message, "senha-confidencial") {
		t.Fatalf("classificação insegura: %s %s %v", code, message, retryable)
	}
}
