package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fbottega-dev/ronda-http/internal/config"
)

func TestListTargetsKeepsOrderWithoutRequestsTokensOrSecrets(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("RONDA_LIST_PRIVATE_TOKEN", "")
	cfg := config.Config{Version: 1, Targets: []config.Target{
		{Name: "Z saúde", URL: server.URL + "?key=PRIVATE_QUERY", Groups: []string{" produção ", "crítico"}, TokenEnv: "RONDA_LIST_PRIVATE_TOKEN", Contains: "PRIVATE_CONTENT", ExpectStatus: []int{200}, TimeoutMS: 1000},
		{Name: "A cabeçalho", URL: server.URL, Method: "HEAD", ExpectStatus: []int{200}, TimeoutMS: 1000},
	}}
	path := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := listTargets([]string{"--config", path}, &out, &errOut)
	want := "DESTINOS\nGET  Z saúde  [grupos: produção, crítico]\nHEAD  A cabeçalho\n"
	if code != 0 || out.String() != want || errOut.Len() != 0 {
		t.Fatalf("code=%d out=%q err=%s", code, out.String(), &errOut)
	}
	if requests.Load() != 0 {
		t.Fatal("list acessou um serviço")
	}
	for _, secret := range []string{server.URL, "PRIVATE_QUERY", "PRIVATE_CONTENT", "RONDA_LIST_PRIVATE_TOKEN"} {
		if strings.Contains(out.String()+errOut.String(), secret) {
			t.Fatalf("list expôs entrada privada: %q", secret)
		}
	}
	if code := listTargets([]string{"--config", path}, brokenWriter{}, &errOut); code != 2 {
		t.Fatalf("escrita interrompida: code=%d", code)
	}
}

func TestListTargetsRejectsInvalidInputAndSupportsHelp(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(bad, []byte(`{"version":1,"targets":[{"name":"bad","url":"PRIVATE_URL"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"extra"}, {"--unknown"}, {"--config", bad}, {"--config", bad + ".missing"}} {
		var out, errOut bytes.Buffer
		if code := listTargets(args, &out, &errOut); code != 2 || out.Len() != 0 || errOut.Len() == 0 || strings.Contains(errOut.String(), "PRIVATE_URL") {
			t.Fatalf("%v: code=%d out=%s err=%s", args, code, &out, &errOut)
		}
	}
	var out, errOut bytes.Buffer
	if code := listTargets([]string{"--help"}, &out, &errOut); code != 0 || !strings.Contains(errOut.String(), "config") {
		t.Fatalf("help: code=%d err=%s", code, &errOut)
	}
}

func TestSelectTargetsMatchesExactlyInConfigurationOrder(t *testing.T) {
	cfg, err := config.Validate(config.Config{Version: 1, Targets: []config.Target{
		{Name: "Z API", URL: "https://example.test/z", Groups: []string{" prod ", "critical"}},
		{Name: "A API", URL: "https://example.test/a", Groups: []string{"dev"}},
		{Name: "B API", URL: "https://example.test/b", Groups: []string{"prod"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, only, group string
		want              []string
	}{
		{name: "all", want: []string{"Z API", "A API", "B API"}},
		{name: "only", only: "A API", want: []string{"A API"}},
		{name: "group", group: "prod", want: []string{"Z API", "B API"}},
		{name: "second_membership", group: "critical", want: []string{"Z API"}},
		{name: "unknown_name", only: "missing"},
		{name: "unknown_group", group: "missing"},
		{name: "name_case", only: "a API"},
		{name: "group_case", group: "Prod"},
		{name: "name_space", only: " A API "},
		{name: "group_space", group: " prod "},
		{name: "exclusive", only: "Z API", group: "prod"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := selectTargets(cfg, test.only, test.group)
			if test.want == nil {
				if err == nil {
					t.Fatal("esperava erro de seleção")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			names := make([]string, len(got.Targets))
			for i := range got.Targets {
				names[i] = got.Targets[i].Name
			}
			if got.Version != cfg.Version || !reflect.DeepEqual(names, test.want) {
				t.Fatalf("seleção inesperada: %+v", got)
			}
			// Selection must not mutate or retain writable slices from cfg.
			got.Targets[0].Name = "changed"
			got.Targets[0].ExpectStatus[0] = 500
			got.Targets[0].Groups[0] = "changed"
		})
	}
	if cfg.Targets[0].Name != "Z API" || cfg.Targets[0].Groups[0] != "prod" || cfg.Targets[0].ExpectStatus[0] != 200 || cfg.Targets[1].Groups[0] != "dev" {
		t.Fatal("seleção modificou configuração original")
	}
}
