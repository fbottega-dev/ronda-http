package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fbottega-dev/ronda-http/internal/checker"
	"github.com/fbottega-dev/ronda-http/internal/config"
)

func writeOnlyConfig(t *testing.T, targets ...config.Target) string {
	t.Helper()
	data, err := json.Marshal(config.Config{Version: 1, Targets: targets})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func onlyTarget(name, url string) config.Target {
	return config.Target{Name: name, URL: url, TimeoutMS: 1000, ExpectStatus: []int{200}}
}

func TestCheckOnlySelectsRequestsAndReport(t *testing.T) {
	for _, tc := range []struct {
		name         string
		args         []string
		wantNames    []string
		wantCode     int
		passed       int
		failed       int
		failedCalls  int32
		healthyCalls int32
	}{
		{name: "sem filtro verifica todos", wantNames: []string{"API indisponível", "Saúde da API"}, wantCode: 1, passed: 1, failed: 1, failedCalls: 1, healthyCalls: 1},
		{name: "seleciona nome normalizado da configuração", args: []string{"--only", "Saúde da API"}, wantNames: []string{"Saúde da API"}, passed: 1, healthyCalls: 1},
		{name: "falha selecionada mantém código um", args: []string{"--only", "API indisponível"}, wantNames: []string{"API indisponível"}, wantCode: 1, failed: 1, failedCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var failedCalls, healthyCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/failed" {
					failedCalls.Add(1)
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				healthyCalls.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			path := writeOnlyConfig(t,
				onlyTarget("API indisponível", server.URL+"/failed"),
				onlyTarget("  Saúde da API  ", server.URL+"/healthy"),
			)
			output := filepath.Join(t.TempDir(), "report.json")
			args := append([]string{"check", "--config", path, "--format", "json", "--output", output}, tc.args...)
			code, stdout, stderr := run(args...)
			if code != tc.wantCode {
				t.Fatalf("code=%d, want=%d, stderr=%s", code, tc.wantCode, stderr)
			}
			if failedCalls.Load() != tc.failedCalls || healthyCalls.Load() != tc.healthyCalls {
				t.Fatalf("requisições: falha=%d saudável=%d; esperadas %d e %d", failedCalls.Load(), healthyCalls.Load(), tc.failedCalls, tc.healthyCalls)
			}
			var report checker.Report
			if err := json.Unmarshal([]byte(stdout), &report); err != nil {
				t.Fatalf("stdout deve conter só JSON: %v", err)
			}
			if report.Passed != tc.passed || report.Failed != tc.failed || len(report.Results) != len(tc.wantNames) {
				t.Fatalf("contagens ou resultados incorretos: %+v", report)
			}
			for i, name := range tc.wantNames {
				result := report.Results[i]
				if result.Name != name || result.Passed != (name == "Saúde da API") || len(result.Attempts) != 1 {
					t.Errorf("resultado %d incorreto: %+v", i, result)
				}
			}
			saved, err := os.ReadFile(output)
			if err != nil || string(saved) != stdout {
				t.Fatalf("arquivo deve conter o mesmo relatório filtrado de stdout: %v", err)
			}
		})
	}
}

func TestCheckOnlyRejectsInvalidSelectionBeforeRequests(t *testing.T) {
	for _, value := range []string{"", " \t ", "Desconhecido", "saúde da api", " Saúde da API "} {
		t.Run(value, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			path := writeOnlyConfig(t, onlyTarget("Saúde da API", server.URL))
			output := filepath.Join(t.TempDir(), "report.json")
			code, stdout, stderr := run("check", "--config", path, "--only="+value, "--format", "json", "--output", output)
			if code != 2 || stdout != "" || stderr == "" {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if calls.Load() != 0 {
				t.Fatalf("seleção inválida fez %d requisições", calls.Load())
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("seleção inválida não deve criar relatório: %v", err)
			}
		})
	}
}

func TestCheckOnlyValidatesUnselectedTargets(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	path := writeOnlyConfig(t, onlyTarget("Saúde da API", server.URL), onlyTarget("Inválido", "ftp://example.test"))
	output := filepath.Join(t.TempDir(), "report.json")
	code, stdout, stderr := run("check", "--config", path, "--only", "Saúde da API", "--format", "json", "--output", output)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "destino 2") {
		t.Fatalf("configuração inteira deve ser validada: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if calls.Load() != 0 {
		t.Fatalf("configuração inválida fez %d requisições", calls.Load())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("configuração inválida não deve criar relatório: %v", err)
	}
}

func TestCheckOnlyResolvesSelectedToken(t *testing.T) {
	const tokenEnv = "RONDA_CLI_ONLY_TEST_TOKEN"
	for _, tc := range []struct {
		name       string
		selection  string
		token      string
		wantCode   int
		wantPublic int32
		wantSecret int32
	}{
		{name: "token ausente fora da seleção", selection: "Público", wantPublic: 1},
		{name: "token selecionado ausente", selection: "Privado", wantCode: 2},
		{name: "sem filtro ainda exige todos os tokens", wantCode: 2},
		{name: "token selecionado enviado", selection: "Privado", token: "local-test-token", wantSecret: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tokenEnv, tc.token)
			if tc.token == "" {
				if err := os.Unsetenv(tokenEnv); err != nil {
					t.Fatal(err)
				}
			}
			var publicCalls, secretCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/private" {
					secretCalls.Add(1)
					if r.Header.Get("Authorization") != "Bearer local-test-token" {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
				} else {
					publicCalls.Add(1)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			private := onlyTarget("Privado", server.URL+"/private")
			private.TokenEnv = tokenEnv
			path := writeOnlyConfig(t, onlyTarget("Público", server.URL+"/public"), private)
			args := []string{"check", "--config", path, "--format", "json"}
			if tc.selection != "" {
				args = append(args, "--only", tc.selection)
			}
			code, stdout, stderr := run(args...)
			if code != tc.wantCode {
				t.Fatalf("code=%d want=%d stdout=%s stderr=%s", code, tc.wantCode, stdout, stderr)
			}
			if publicCalls.Load() != tc.wantPublic || secretCalls.Load() != tc.wantSecret {
				t.Fatalf("requisições: público=%d privado=%d; esperadas %d e %d", publicCalls.Load(), secretCalls.Load(), tc.wantPublic, tc.wantSecret)
			}
			if tc.wantCode == 2 && (stdout != "" || stderr == "") {
				t.Fatalf("token ausente deve emitir apenas erro: stdout=%s stderr=%s", stdout, stderr)
			}
		})
	}
}
