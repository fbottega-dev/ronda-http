package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fbottega-dev/ronda-http/internal/checker"
	"github.com/fbottega-dev/ronda-http/internal/config"
)

func run(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), args, &out, &errOut, "test")
	return code, out.String(), errOut.String()
}

func TestHelpVersionAndUsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"help"}, {"version"}, {"check", "--help"}, {"init", "--help"}, {"demo", "--help"}} {
		if code, out, errOut := run(args...); code != 0 || out+errOut == "" {
			t.Fatalf("%v: code=%d, out=%s, err=%s", args, code, out, errOut)
		}
	}
	for _, args := range [][]string{{"invalid"}, {"version", "extra"}, {"init", "extra"}, {"demo", "extra"}, {"check", "--unknown"}, {"check", "--parallel", "0"}, {"check", "--parallel", "9"}, {"check", "--format", "xml"}} {
		if code, _, errOut := run(args...); code != 2 || errOut == "" {
			t.Fatalf("%v: code=%d, err=%s", args, code, errOut)
		}
	}
}

func TestInitCreatesValidConfigAndPreservesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ronda.json")
	if code, _, errOut := run("init", "--config", path); code != 0 {
		t.Fatal(errOut)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(bytes.NewReader(original)); err != nil {
		t.Fatalf("init gerou configuração inválida: %v", err)
	}
	if code, _, _ := run("init", "--config", path); code != 2 {
		t.Fatal("init substituiu arquivo existente")
	}
	current, _ := os.ReadFile(path)
	if !bytes.Equal(original, current) {
		t.Fatal("arquivo existente alterado")
	}
}

func writeConfig(t *testing.T, url string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(config.Config{Version: 1, Targets: []config.Target{{Name: "API local", URL: url, TimeoutMS: 1000, ExpectStatus: []int{200}, Contains: "pronto"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckJSONFileAndExitCodes(t *testing.T) {
	for _, status := range []int{200, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				fmtWrite(w, "pronto")
			}))
			defer server.Close()
			path := writeConfig(t, server.URL+"?private=SECRET_QUERY")
			output := filepath.Join(t.TempDir(), "report.json")
			code, stdout, stderr := run("check", "--config", path, "--format", "json", "--output", output)
			wantCode := 0
			if status != 200 {
				wantCode = 1
			}
			if code != wantCode {
				t.Fatalf("code=%d, stderr=%s", code, stderr)
			}
			var report checker.Report
			if err := json.Unmarshal([]byte(stdout), &report); err != nil {
				t.Fatalf("stdout deve conter só JSON: %v", err)
			}
			if len(report.Results) != 1 || report.Results[0].Passed != (status == 200) {
				t.Fatalf("relatório: %+v", report)
			}
			saved, err := os.ReadFile(output)
			if err != nil || string(saved) != stdout {
				t.Fatalf("arquivo difere de stdout: %v", err)
			}
			if strings.Contains(stdout+stderr, "SECRET_QUERY") {
				t.Fatal("query vazou no relatório")
			}
			if secondCode, _, _ := run("check", "--config", path, "--output", output); secondCode != 2 {
				t.Fatal("arquivo de relatório existente foi sobrescrito")
			}
		})
	}
}

func fmtWrite(w io.Writer, value string) { _, _ = io.WriteString(w, value) }

func TestCheckConfigurationErrorDoesNotLeakInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"secret-PRIVATE":"https://secret.test/?token=PRIVATE"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + ".missing"} {
		code, out, errOut := run("check", "--config", candidate, "--format", "json")
		if code != 2 || out != "" || errOut == "" || strings.Contains(errOut, "PRIVATE") {
			t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
		}
	}
}

func TestCanceledCheckProducesReportAndExit130(t *testing.T) {
	path := writeConfig(t, "http://127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	code := Run(ctx, []string{"check", "--config", path, "--format", "json"}, &out, &errOut, "test")
	if code != 130 || !strings.Contains(out.String(), `"canceled": true`) {
		t.Fatalf("code=%d out=%s err=%s", code, &out, &errOut)
	}
}

func TestTextReportShowsFailuresRetriesAndSummary(t *testing.T) {
	var out bytes.Buffer
	err := renderText(&out, checker.Report{Passed: 1, Failed: 1, Canceled: true, Results: []checker.Result{
		{Name: "Catálogo", Passed: true, Attempts: []checker.Attempt{{Status: 503}, {Status: 200, Message: "Resposta aprovada."}}},
		{Name: "API lenta", Attempts: []checker.Attempt{{Code: "timeout", Message: "Tempo limite excedido."}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"[OK] Catálogo", "[FALHOU] API lenta", "HTTP 200", "tentativas: 2", "1 aprovados · 1 falharam", "interrompida"} {
		if !strings.Contains(out.String(), phrase) {
			t.Errorf("saída não contém %q", phrase)
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestOutputFailuresReturnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmtWrite(w, "pronto") }))
	defer server.Close()
	path := writeConfig(t, server.URL)
	for _, format := range []string{"text", "json"} {
		var errOut bytes.Buffer
		if code := Run(context.Background(), []string{"check", "--config", path, "--format", format}, brokenWriter{}, &errOut, "test"); code != 2 || !strings.Contains(errOut.String(), "escrever a saída") {
			t.Fatalf("code=%d, error=%s", code, &errOut)
		}
	}
	if err := writeNewFile(filepath.Join(t.TempDir(), "missing", "report.json"), []byte("{}")); err == nil {
		t.Fatal("esperava falha para diretório inexistente")
	}
}

func TestSimpleCommandsDetectBrokenOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "created.json")
	for _, args := range [][]string{nil, {"help"}, {"version"}, {"init", "--config", path}} {
		var errOut bytes.Buffer
		if code := Run(context.Background(), args, brokenWriter{}, &errOut, "test"); code != 2 || !strings.Contains(errOut.String(), "escrever a saída") {
			t.Fatalf("%v: code=%d err=%s", args, code, &errOut)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("falha de stdout não deve apagar a configuração criada: %v", err)
	}
}
