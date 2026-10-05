package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func writeValidateConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateDoesNotResolveTokensRequestServicesOrExposeFields(t *testing.T) {
	const tokenEnv = "RONDA_VALIDATE_SECRET_ENV"
	t.Setenv(tokenEnv, "")
	if err := os.Unsetenv(tokenEnv); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		fmtWrite(w, "PRIVATE_RESPONSE_BODY")
	}))
	defer server.Close()
	data := fmt.Sprintf(`{"version":1,"targets":[
		{"name":"PRIVATE_TARGET_NAME","url":%q,"contains":"PRIVATE_EXPECTED_BODY","token_env":%q},
		{"name":"PRIVATE_SECOND_NAME","url":%q,"method":"HEAD"}
	]}`, server.URL+"/PRIVATE_PATH?token=PRIVATE_QUERY", tokenEnv, server.URL+"/PRIVATE_HEAD")
	path := writeValidateConfig(t, data)
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	code, stdout, stderr := run("validate", "--config", path)
	if code != 0 || !strings.Contains(strings.ToLower(stdout), "válida") || !strings.Contains(stdout, "2") || stderr != "" {
		t.Fatalf("configuração válida com dois destinos: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if calls.Load() != 0 {
		t.Fatalf("validação fez %d requisições HTTP", calls.Load())
	}
	for _, secret := range []string{"PRIVATE_", tokenEnv, server.URL, "GET", "HEAD"} {
		if strings.Contains(stdout+stderr, secret) {
			t.Errorf("validação expôs campo da configuração: %q", secret)
		}
	}
	entries, err := os.ReadDir(workingDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("validação não deve criar arquivos: entries=%v err=%v", entries, err)
	}
	saved, err := os.ReadFile(path)
	if err != nil || string(saved) != data {
		t.Fatalf("validação não deve modificar a configuração: err=%v", err)
	}
}

func TestValidateUsesDefaultConfiguration(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("ronda.json", []byte(`{"version":1,"targets":[{"name":"API local","url":"http://127.0.0.1:1"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := run("validate")
	if code != 0 || !strings.Contains(strings.ToLower(stdout), "válida") || !strings.Contains(stdout, "1") || stderr != "" {
		t.Fatalf("configuração padrão: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "ronda.json" {
		t.Fatalf("validação deve apenas ler ronda.json: entries=%v err=%v", entries, err)
	}
}

func TestValidateRejectsInvalidConfigurationBeforeSuccess(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{name: "JSON inválido", data: `{"PRIVATE_FIELD":`},
		{name: "campo desconhecido", data: `{"version":1,"targets":[{"name":"PRIVATE_NAME","url":"https://example.test","PRIVATE_FIELD":"PRIVATE_VALUE"}]}`},
		{name: "segundo destino inválido", data: `{"version":1,"targets":[{"name":"Válido","url":"https://example.test"},{"name":"PRIVATE_NAME","url":"ftp://example.test/PRIVATE_PATH"}]}`},
		{name: "chave duplicada", data: `{"version":1,"version":1,"targets":[{"name":"PRIVATE_NAME","url":"https://example.test"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeValidateConfig(t, tc.data)
			code, stdout, stderr := run("validate", "--config", path)
			if code != 2 || stdout != "" || stderr == "" {
				t.Fatalf("configuração inválida: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if strings.Contains(stdout+stderr, "PRIVATE_") {
				t.Fatal("erro de validação expôs conteúdo da configuração")
			}
		})
	}
}

func TestValidateMissingConfigurationDoesNotCreateFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, args := range [][]string{{"validate"}, {"validate", "--config", filepath.Join(dir, "PRIVATE_MISSING.json")}} {
		code, stdout, stderr := run(args...)
		if code != 2 || stdout != "" || stderr == "" || strings.Contains(stderr, "PRIVATE_MISSING") {
			t.Fatalf("arquivo ausente: code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("arquivo ausente não deve ser criado: entries=%v err=%v", entries, err)
	}
}

func TestValidateDetectsBrokenOutput(t *testing.T) {
	path := writeValidateConfig(t, `{"version":1,"targets":[{"name":"API local","url":"https://example.test"}]}`)
	var errOut bytes.Buffer
	code := Run(context.Background(), []string{"validate", "--config", path}, brokenWriter{}, &errOut, "test")
	if code != 2 || !strings.Contains(errOut.String(), "escrever a saída") {
		t.Fatalf("falha de saída: code=%d stderr=%s", code, &errOut)
	}
}

func TestValidateHelpAndInvalidArguments(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"validate", "--help"}, {"validate", "-h"}} {
		code, stdout, stderr := run(args...)
		if code != 0 || !strings.Contains(stdout+stderr, "validate") || !strings.Contains(stdout+stderr, "config") {
			t.Fatalf("ajuda deve funcionar sem configuração: code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
	}
	for _, args := range [][]string{{"validate", "--unknown"}, {"validate", "extra"}, {"validate", "--config"}, {"validate", "--format", "json"}} {
		code, stdout, stderr := run(args...)
		if code != 2 || stdout != "" || stderr == "" {
			t.Fatalf("argumentos inválidos %v: code=%d stdout=%s stderr=%s", args, code, stdout, stderr)
		}
	}
}
