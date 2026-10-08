package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fbottega-dev/ronda-http/internal/history"
	reports "github.com/fbottega-dev/ronda-http/internal/report"
)

func TestGroupSelectionBeforeTokenResolutionAndRequests(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/public" {
			t.Error("destino excluído recebeu requisição")
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	public := onlyTarget("Público", server.URL+"/public")
	public.ID, public.Groups = "public", []string{"local", "smoke"}
	private := onlyTarget("Privado", server.URL+"/private")
	private.Groups, private.TokenEnv = []string{"private"}, "RONDA_GROUP_TOKEN"
	t.Setenv(private.TokenEnv, "")
	path := writeOnlyConfig(t, public, private)
	for _, tc := range []struct {
		args      []string
		code      int
		wantCalls int32
	}{
		{[]string{"--group", "smoke"}, 0, 1},
		{[]string{"--group", "private"}, 2, 0},
		{[]string{"--group", "Smoke"}, 2, 0},
		{[]string{"--group", " smoke "}, 2, 0},
		{[]string{"--group="}, 2, 0},
		{[]string{"--group", "smoke", "--only", "Público"}, 2, 0},
	} {
		calls.Store(0)
		args := append([]string{"check", "--config", path, "--format", "json"}, tc.args...)
		code, stdout, stderr := run(args...)
		if code != tc.code || calls.Load() != tc.wantCalls {
			t.Fatalf("%v: code=%d calls=%d stderr=%s", tc.args, code, calls.Load(), stderr)
		}
		if code == 0 {
			r, err := reports.Decode(strings.NewReader(stdout))
			if err != nil || len(r.Results) != 1 || r.Results[0].ID != "public" {
				t.Fatalf("relatório filtrado inválido: %+v %v", r, err)
			}
		} else if stdout != "" {
			t.Fatal("seleção inválida produziu relatório")
		}
	}
}

func TestJUnitOutputJSONHistoryAndComparisonWorkflow(t *testing.T) {
	var status atomic.Int32
	status.Store(200)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(int(status.Load()))
		fmtWrite(w, "PRIVATE_BODY")
	}))
	defer server.Close()
	target := onlyTarget(`Saúde & <API>`, server.URL+"?token=PRIVATE_QUERY")
	target.ID = "health"
	path := writeOnlyConfig(t, target)
	dir := filepath.Join(t.TempDir(), "history")
	beforePath, afterPath := filepath.Join(t.TempDir(), "before.json"), filepath.Join(t.TempDir(), "after.json")
	for i, output := range []string{beforePath, afterPath} {
		if i == 1 {
			status.Store(503)
		}
		code, stdout, stderr := run("check", "--config", path, "--format", "junit", "--output", output, "--history-dir", dir)
		if code != i {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
		var suite struct {
			Tests    int `xml:"tests,attr"`
			Failures int `xml:"failures,attr"`
			Cases    []struct {
				Name string `xml:"name,attr"`
			} `xml:"testcase"`
		}
		if err := xml.Unmarshal([]byte(stdout), &suite); err != nil || suite.Tests != 1 || suite.Failures != i || suite.Cases[0].Name != target.Name {
			t.Fatalf("JUnit inválido: %s (%v)", stdout, err)
		}
		r, err := readReport(output)
		if err != nil || r.Results[0].ID != "health" || r.Failed != i {
			t.Fatalf("--output deve ser JSON: %+v %v", r, err)
		}
		if strings.Contains(stdout+stderr, "PRIVATE") || strings.Contains(stdout+stderr, server.URL) {
			t.Fatal("saída vazou dados HTTP")
		}
	}
	code, stdout, stderr := run("history", "--dir", dir, "--format", "json")
	var entries []history.Entry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil || code != 0 || len(entries) != 2 {
		t.Fatalf("history: %s %s %v", stdout, stderr, err)
	}
	if entries[0].Failed != 1 || entries[1].Passed != 1 {
		t.Fatalf("ordem inválida: %+v", entries)
	}
	code, stdout, stderr = run("history", "--dir", dir, "--show", entries[0].ID, "--format", "json")
	r, err := reports.Decode(strings.NewReader(stdout))
	if code != 0 || err != nil || r.Failed != 1 {
		t.Fatalf("history --show: %s %s %v", stdout, stderr, err)
	}
	for _, tc := range []struct {
		before, after string
		code          int
		field         string
	}{
		{beforePath, afterPath, 1, `"regressed": 1`},
		{afterPath, beforePath, 0, `"recovered": 1`},
		{beforePath, beforePath, 0, `"unchanged": 1`},
	} {
		code, stdout, stderr = run("compare", "--before", tc.before, "--after", tc.after, "--format", "json")
		if code != tc.code || !strings.Contains(stdout, tc.field) {
			t.Fatalf("compare: code=%d %s %s", code, stdout, stderr)
		}
	}
	// Failed output must not replace previous JSON or publish an extra snapshot.
	original, _ := os.ReadFile(afterPath)
	code, _, _ = run("check", "--config", path, "--output", afterPath, "--history-dir", dir)
	current, _ := os.ReadFile(afterPath)
	entries, err = history.List(dir, 20)
	if code != 2 || !bytes.Equal(original, current) || err != nil || len(entries) != 2 {
		t.Fatal("falha de arquivo alterou histórico ou relatório anterior")
	}
}

func TestCanceledJUnitPersistsIdentityAndCannotBeCompared(t *testing.T) {
	target := onlyTarget("Cancelado", "http://127.0.0.1:1")
	target.ID = "stable"
	path := writeOnlyConfig(t, target)
	dir := filepath.Join(t.TempDir(), "history")
	output := filepath.Join(t.TempDir(), "cancel.json")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	code := Run(ctx, []string{"check", "--config", path, "--format", "junit", "--history-dir", dir, "--output", output}, &out, &errOut, "test")
	if code != 130 || !strings.Contains(out.String(), `skipped="1"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, &out, &errOut)
	}
	entries, err := history.List(dir, 20)
	if err != nil || len(entries) != 1 || !entries[0].Canceled {
		t.Fatalf("cancelamento perdido: %+v %v", entries, err)
	}
	r, err := history.Load(dir, entries[0].ID)
	if err != nil || r.Results[0].ID != "stable" {
		t.Fatalf("ID cancelado perdido: %+v %v", r, err)
	}
	code, stdout, stderr := run("compare", "--before", output, "--after", output)
	if code != 2 || stdout != "" || stderr == "" {
		t.Fatalf("comparação de cancelados: %d %s %s", code, stdout, stderr)
	}
}

func TestRecordsErrorsAndBrokenWriters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{
		{"history", "--limit", "0"}, {"history", "--dir="}, {"history", "--format", "junit"},
		{"history", "--show="}, {"history", "--show", " "},
		{"history", "--dir", dir, "--show", "../../private"},
		{"compare"}, {"compare", "--before", "missing", "--after", "missing"},
	} {
		if code, _, _ := run(args...); code != 2 {
			t.Fatalf("%v: code=%d", args, code)
		}
	}
	if code, _, _ := run("history", "--dir", dir); code != 0 {
		t.Fatal("histórico ausente deve ser vazio")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("leitura criou histórico")
	}
	var errOut bytes.Buffer
	if code := Run(context.Background(), []string{"history", "--dir", dir, "--format", "json"}, brokenWriter{}, &errOut, "test"); code != 2 {
		t.Fatal("stdout com erro foi ignorado")
	}
}
