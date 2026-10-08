package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fbottega-dev/ronda-http/internal/checker"
	"github.com/fbottega-dev/ronda-http/internal/config"
)

func sampleReport() checker.Report {
	return checker.Report{
		SchemaVersion: 1,
		StartedAt:     time.Date(2026, 10, 6, 20, 30, 45, 123456789, time.UTC),
		DurationMS:    23,
		Passed:        1,
		Failed:        2,
		Canceled:      true,
		Results: []checker.Result{
			{Name: "Saúde da API", Passed: true, Attempts: []checker.Attempt{
				{Status: 200, DurationMS: 10, Code: "ok", Message: "Verificação aprovada."},
			}},
			{Name: "Serviço indisponível", Attempts: []checker.Attempt{
				{Status: 503, DurationMS: 11, Code: "status", Message: "Status HTTP diferente do esperado."},
			}},
			{Name: "Serviço cancelado", Attempts: []checker.Attempt{
				{Code: "canceled", Message: "Verificação cancelada."},
			}},
		},
	}
}

func mustSave(t *testing.T, dir string, r checker.Report) string {
	t.Helper()
	id, err := Save(dir, r)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSaveLoadRoundTripAndPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runs")
	original := sampleReport()
	id := mustSave(t, dir, original)
	if !strings.HasPrefix(id, "20261006T203045.123456789Z-") || !snapshotID.MatchString(id) {
		t.Fatalf("ID com formato incorreto: %s", id)
	}
	loaded, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, original) {
		t.Fatalf("relatório alterado: obtido %+v, esperado %+v", loaded, original)
	}
	entries, err := List(dir, 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("List = %+v, %v", entries, err)
	}
	if entries[0].ID != id || entries[0].Passed != 1 || entries[0].Failed != 2 || !entries[0].Canceled || !entries[0].StartedAt.Equal(original.StartedAt) {
		t.Fatalf("resumo incorreto: %+v", entries[0])
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 || files[0].Name() != id+".json" {
		t.Fatalf("arquivo temporário foi deixado: %+v, %v", files, err)
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{dir: 0700, filepath.Join(dir, id+".json"): 0600} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != want {
				t.Fatalf("permissões esperadas %o: %+v, %v", want, info, err)
			}
		}
	}
}

func TestSaveNormalizesTimezoneWithoutMutatingInput(t *testing.T) {
	r := sampleReport()
	r.StartedAt = r.StartedAt.In(time.FixedZone("BRT", -3*60*60))
	dir := t.TempDir()
	id := mustSave(t, dir, r)
	if !strings.HasPrefix(id, "20261006T203045.123456789Z-") {
		t.Fatalf("ID não usa UTC: %s", id)
	}
	loaded, err := Load(dir, id)
	if err != nil || !loaded.StartedAt.Equal(r.StartedAt) || loaded.StartedAt.Location() != time.UTC {
		t.Fatalf("UTC não preservado: %+v, %v", loaded, err)
	}
	if r.StartedAt.Location().String() != "BRT" {
		t.Fatal("relatório fornecido foi alterado")
	}
}

func TestConcurrentSavesAreUniqueAndComplete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runs")
	const count = 48
	type saved struct {
		id   string
		name string
		err  error
	}
	results := make(chan saved, count)
	var workers sync.WaitGroup
	for i := range count {
		workers.Add(1)
		go func() {
			defer workers.Done()
			r := sampleReport()
			r.Results[0].Name = fmt.Sprintf("API %02d", i)
			id, err := Save(dir, r)
			results <- saved{id, r.Results[0].Name, err}
		}()
	}
	workers.Wait()
	close(results)
	seen := make(map[string]bool, count)
	for saved := range results {
		if saved.err != nil {
			t.Fatal(saved.err)
		}
		if seen[saved.id] {
			t.Fatalf("ID duplicado: %s", saved.id)
		}
		seen[saved.id] = true
		r, err := Load(dir, saved.id)
		if err != nil || r.Results[0].Name != saved.name {
			t.Fatalf("execução substituída ou incompleta: %+v, %v", r, err)
		}
	}
	entries, err := List(dir, 200)
	if err != nil || len(entries) != count {
		t.Fatalf("execuções perdidas: %d, %v", len(entries), err)
	}
}

func TestPublishRetriesCollisionsWithoutOverwriting(t *testing.T) {
	dir := t.TempDir()
	startedAt := sampleReport().StartedAt
	oldID := startedAt.Format(timestampFormat) + "-000000000000"
	oldName := filepath.Join(dir, oldID+".json")
	writeFile(t, oldName, []byte("original"))
	temporaryName := filepath.Join(dir, ".ronda-test.tmp")
	writeFile(t, temporaryName, []byte("novo"))
	calls := 0
	id, err := publish(dir, temporaryName, startedAt, func() (string, error) {
		calls++
		return fmt.Sprintf("%012x", calls-1), nil
	})
	if err != nil || calls != 2 || id == oldID {
		t.Fatalf("colisão não resolvida: %s, %d chamadas, %v", id, calls, err)
	}
	original, err := os.ReadFile(oldName)
	if err != nil || string(original) != "original" {
		t.Fatalf("execução existente substituída: %q, %v", original, err)
	}
	created, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil || string(created) != "novo" {
		t.Fatalf("novo arquivo incompleto: %q, %v", created, err)
	}
	_, err = publish(dir, temporaryName, startedAt, func() (string, error) { return "000000000000", nil })
	if err == nil {
		t.Fatal("colisões repetidas deveriam falhar")
	}
	original, _ = os.ReadFile(oldName)
	if string(original) != "original" {
		t.Fatal("execução existente substituída após colisões repetidas")
	}
}

func TestListMissingDoesNotCreateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	entries, err := List(dir, 10)
	if err != nil || entries == nil || len(entries) != 0 {
		t.Fatalf("List vazio = %+v, %v", entries, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("List criou a pasta: %v", err)
	}
}

func TestListOrderLimitsAndIgnoredFiles(t *testing.T) {
	dir := t.TempDir()
	var ids []string
	for _, offset := range []int{2, 0, 1} {
		r := sampleReport()
		r.StartedAt = r.StartedAt.Add(time.Duration(offset) * time.Hour)
		ids = append(ids, mustSave(t, dir, r))
	}
	for _, name := range []string{".ronda-interrupted.tmp", "report.json", "20269999T203045.123456789Z-000000000000.json", "20261006T203045.123456789Z-000000000000.json.bak"} {
		writeFile(t, filepath.Join(dir, name), []byte("arquivo incompleto ou não relacionado"))
	}
	if err := os.Mkdir(filepath.Join(dir, "20261006T233045.123456789Z-000000000000.json"), 0700); err != nil {
		t.Fatal(err)
	}
	entries, err := List(dir, 2)
	if err != nil || len(entries) != 2 || entries[0].ID != ids[0] || entries[1].ID != ids[2] {
		t.Fatalf("ordem ou limite incorreto: %+v, %v", entries, err)
	}
	for _, limit := range []int{0, -1, 201} {
		if _, err := List(dir, limit); err == nil {
			t.Fatalf("limite %d aceito", limit)
		}
	}
}

func TestLoadRejectsUnsafeOrMalformedIDs(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{
		"", ".", "..", "../secret", `..\secret`, "/secret", `C:\secret`,
		"20261006T203045.123456789Z-000000000000.json",
		"20261006T203045.123456789Z-00000000000G",
		"20261306T203045.123456789Z-000000000000",
		"20260230T203045.123456789Z-000000000000",
		"20261006T253045.123456789Z-000000000000",
		"00001006T203045.123456789Z-000000000000",
		"20261006T203045.123456789Z-000000000000\n",
	} {
		if _, err := Load(dir, id); err == nil || !strings.Contains(err.Error(), "identificador") {
			t.Errorf("ID inválido aceito ou erro inadequado: %q, %v", id, err)
		}
	}
}

func TestCorruptSnapshotsFailSafely(t *testing.T) {
	const secret = "segredo-que-nao-pode-aparecer"
	cases := map[string]func([]byte) []byte{
		"truncado": func([]byte) []byte { return []byte(`{"schema_version":1`) },
		"campo desconhecido": func(data []byte) []byte {
			return []byte(strings.Replace(string(data), `"schema_version": 1`, `"`+secret+`": "`+secret+`", "schema_version": 1`, 1))
		},
		"versão futura": func(data []byte) []byte {
			return []byte(strings.Replace(string(data), `"schema_version": 1`, `"schema_version": 999`, 1))
		},
		"timestamp diferente": func(data []byte) []byte {
			return []byte(strings.Replace(string(data), "2026-10-06T20:30:45", "2026-10-07T20:30:45", 1))
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), secret)
			id := mustSave(t, dir, sampleReport())
			path := filepath.Join(dir, id+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, path, corrupt(data))
			for _, load := range []func() error{
				func() error { _, err := Load(dir, id); return err },
				func() error { _, err := List(dir, 10); return err },
			} {
				err := load()
				if err == nil || strings.Contains(err.Error(), secret) {
					t.Fatalf("erro ausente ou sensível: %v", err)
				}
			}
		})
	}
}

func TestListValidatesOnlySelectedSnapshots(t *testing.T) {
	dir := t.TempDir()
	r := sampleReport()
	older := mustSave(t, dir, r)
	r.StartedAt = r.StartedAt.Add(time.Hour)
	newer := mustSave(t, dir, r)
	writeFile(t, filepath.Join(dir, older+".json"), []byte("corrompido"))
	entries, err := List(dir, 1)
	if err != nil || len(entries) != 1 || entries[0].ID != newer {
		t.Fatalf("List não respeitou limite antes de validar: %+v, %v", entries, err)
	}
	if _, err := List(dir, 2); err == nil {
		t.Fatal("corrupção de execução selecionada foi ignorada")
	}
}

func TestInvalidReportDoesNotTouchFilesystem(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	r := sampleReport()
	r.SchemaVersion = 999
	if _, err := Save(dir, r); err == nil {
		t.Fatal("relatório inválido salvo")
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Save criou pasta antes de validar: %v", err)
	}
}

func TestFilesystemFailuresDoNotExposePaths(t *testing.T) {
	const secret = "nome-local-sensivel"
	dir := t.TempDir()
	file := filepath.Join(dir, secret)
	writeFile(t, file, []byte("preservar"))
	id := "20261006T203045.123456789Z-000000000000"
	for index, action := range []func() error{
		func() error { _, err := Save(file, sampleReport()); return err },
		func() error { _, err := List(file, 10); return err },
		func() error { _, err := Load(file, id); return err },
		func() error { _, err := Load(filepath.Join(dir, secret, "missing"), id); return err },
		func() error { _, err := publish(dir, secret, sampleReport().StartedAt, randomSuffix); return err },
		func() error {
			_, err := publish(dir, file, sampleReport().StartedAt, func() (string, error) { return "", errors.New(secret) })
			return err
		},
	} {
		if err := action(); err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("ação %d: erro ausente ou sensível: %v", index, err)
		}
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "preservar" {
		t.Fatalf("arquivo existente alterado: %q, %v", data, err)
	}
	if err := os.Mkdir(filepath.Join(dir, id+".json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, id); err == nil {
		t.Fatal("diretório aceito como execução")
	}
}

func TestHistoryExcludesRequestAndResponseSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token-sensivel" {
			t.Error("token de teste não recebido")
		}
		_, _ = w.Write([]byte("resposta-sensivel"))
	}))
	defer server.Close()
	cfg := config.Config{Version: 1, Targets: []config.Target{{
		Name: "API", URL: server.URL + "/privado?chave=query-sensivel", Method: "GET",
		ExpectStatus: []int{200}, TimeoutMS: 1000, Contains: "resposta-sensivel", TokenEnv: "TOKEN_SENSIVEL",
	}}}
	r, err := checker.Run(context.Background(), cfg, checker.Options{LookupEnv: func(string) (string, bool) { return "token-sensivel", true }})
	if err != nil || r.Passed != 1 {
		t.Fatalf("check local falhou: %+v, %v", r, err)
	}
	dir := t.TempDir()
	id := mustSave(t, dir, r)
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil || !json.Valid(data) {
		t.Fatalf("JSON inválido: %v", err)
	}
	for _, secret := range []string{server.URL, "privado", "query-sensivel", "resposta-sensivel", "token-sensivel", "TOKEN_SENSIVEL", "Authorization"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("segredo persistido: %s", secret)
		}
	}
}

func TestSymbolicLinksAreNotSnapshots(t *testing.T) {
	dir := t.TempDir()
	r := sampleReport()
	id := mustSave(t, dir, r)
	linkID := "20261006T213045.123456789Z-000000000000"
	if err := os.Symlink(filepath.Join(dir, id+".json"), filepath.Join(dir, linkID+".json")); err != nil {
		t.Skipf("links simbólicos indisponíveis: %v", err)
	}
	entries, err := List(dir, 10)
	if err != nil || len(entries) != 1 || entries[0].ID != id {
		t.Fatalf("link simbólico listado: %+v, %v", entries, err)
	}
	if _, err := Load(dir, linkID); err == nil {
		t.Fatal("link simbólico carregado")
	}
}
