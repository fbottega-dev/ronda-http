package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/fbottega-dev/ronda-http/internal/checker"
)

func sampleResult(name string, passed bool) checker.Result {
	code, message := "status", "Status HTTP diferente do esperado."
	if passed {
		code, message = "ok", "Verificação aprovada."
	}
	return checker.Result{Name: name, Passed: passed, Attempts: []checker.Attempt{
		{Status: 200, DurationMS: 50, Code: code, Message: message},
	}}
}

func sampleReport(results ...checker.Result) checker.Report {
	if len(results) == 0 {
		results = []checker.Result{sampleResult("Saúde da API", true)}
	}
	result := checker.Report{
		SchemaVersion: 1, StartedAt: time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC),
		DurationMS: 250, Results: results,
	}
	for _, target := range results {
		if target.Passed {
			result.Passed++
		} else {
			result.Failed++
		}
		if target.Attempts[len(target.Attempts)-1].Code == "canceled" {
			result.Canceled = true
		}
	}
	return result
}

func encodeReport(t *testing.T, report checker.Report) string {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDecodeLegacyAndIdentifiedReport(t *testing.T) {
	for _, id := range []string{"", "api-health"} {
		t.Run(id, func(t *testing.T) {
			want := sampleReport()
			want.Results[0].ID = id
			data := encodeReport(t, want)
			if id == "" && strings.Contains(data, `"id"`) {
				t.Fatal("fixture legado contém id")
			}
			got, err := Decode(strings.NewReader(" \n" + data + "\n"))
			if err != nil || encodeReport(t, got) != data {
				t.Fatalf("roundtrip diferente: %+v; erro %v", got, err)
			}
		})
	}
}

func TestDecodeRejectsMalformedStructureWithoutEchoingInput(t *testing.T) {
	valid := encodeReport(t, sampleReport())
	secret := "segredo-na-entrada"
	cases := map[string]string{
		"sem objeto":               "[]",
		"null":                     "null",
		"vazio":                    "",
		"JSON truncado":            valid[:len(valid)-1],
		"segundo objeto":           valid + valid,
		"texto extra":              valid + secret,
		"chave desconhecida":       strings.Replace(valid, `"schema_version":1`, `"`+secret+`":1`, 1),
		"chave diferente em caixa": strings.Replace(valid, `"schema_version":1`, `"SCHEMA_VERSION":1`, 1),
		"chave duplicada na raiz":  strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		"duplicada em resultado":   strings.Replace(valid, `"name":`, `"name":"`+secret+`","name":`, 1),
		"duplicada em tentativa":   strings.Replace(valid, `"code":`, `"code":"`+secret+`","code":`, 1),
		"campo null":               strings.Replace(valid, `"canceled":false`, `"canceled":null`, 1),
		"booleano omitido":         strings.Replace(valid, `"canceled":false,`, "", 1),
		"tipo incorreto":           strings.Replace(valid, `"schema_version":1`, `"schema_version":"`+secret+`"`, 1),
		"array de null":            strings.Replace(valid, `"results":[{`, `"results":[null,{`, 1),
		"objeto em scalar":         strings.Replace(valid, `"schema_version":1`, `"schema_version":{"x":"`+secret+`"}`, 1),
		"UTF8 inválido":            strings.Replace(valid, "Saúde", string([]byte{0xff}), 1),
		"controle no nome":         strings.Replace(valid, "Saúde", `Sa\u001búde`, 1),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(data))
			if err == nil {
				t.Fatal("entrada inválida foi aceita")
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "Saúde") {
				t.Fatalf("erro expôs entrada: %v", err)
			}
		})
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("erro secreto do reader")
}

type endlessReader struct{ count int }

func (reader *endlessReader) Read(data []byte) (int, error) {
	for index := range data {
		data[index] = ' '
	}
	reader.count += len(data)
	return len(data), nil
}

func TestDecodeBoundsInputAndRedactsIOErrors(t *testing.T) {
	endless := &endlessReader{}
	for _, input := range []io.Reader{nil, failingReader{}, endless} {
		_, err := Decode(input)
		if err == nil || strings.Contains(err.Error(), "secreto") {
			t.Fatalf("erro de leitura não tratado: %v", err)
		}
	}
	if endless.count != maxReportBytes+1 {
		t.Fatalf("leitura ilimitada: %d bytes", endless.count)
	}
	data := encodeReport(t, sampleReport())
	padding := maxReportBytes - len(data)
	if _, err := Decode(strings.NewReader(data + strings.Repeat(" ", padding))); err != nil {
		t.Fatalf("limite exato deveria ser aceito: %v", err)
	}
	if _, err := Decode(strings.NewReader(data + strings.Repeat(" ", padding+1))); err == nil {
		t.Fatal("entrada acima do limite foi aceita")
	}
}

func TestValidateRejectsInvalidReports(t *testing.T) {
	cases := map[string]func(*checker.Report){
		"schema":                 func(r *checker.Report) { r.SchemaVersion = 2 },
		"data zero":              func(r *checker.Report) { r.StartedAt = time.Time{} },
		"duração negativa":       func(r *checker.Report) { r.DurationMS = -1 },
		"nenhum resultado":       func(r *checker.Report) { r.Results = nil },
		"resultados demais":      func(r *checker.Report) { r.Results = make([]checker.Result, 101) },
		"nome vazio":             func(r *checker.Report) { r.Results[0].Name = "" },
		"nome grande":            func(r *checker.Report) { r.Results[0].Name = strings.Repeat("á", 61) },
		"nome inválido":          func(r *checker.Report) { r.Results[0].Name = string([]byte{0xff}) },
		"nome com controle":      func(r *checker.Report) { r.Results[0].Name = "nome\n" },
		"nome com bordas vazias": func(r *checker.Report) { r.Results[0].Name = " nome" },
		"ID inválido":            func(r *checker.Report) { r.Results[0].ID = "senha/confidencial" },
		"ID grande":              func(r *checker.Report) { r.Results[0].ID = strings.Repeat("a", 65) },
		"nome duplicado": func(r *checker.Report) {
			r.Results = append(r.Results, r.Results[0])
			r.Results[0].ID, r.Results[1].ID = "primeiro", "segundo"
			r.Passed = 2
		},
		"ID duplicado": func(r *checker.Report) {
			r.Results = append(r.Results, sampleResult("Outro", true))
			r.Results[0].ID, r.Results[1].ID = "api", "api"
			r.Passed = 2
		},
		"sem tentativa":           func(r *checker.Report) { r.Results[0].Attempts = nil },
		"tentativas demais":       func(r *checker.Report) { r.Results[0].Attempts = make([]checker.Attempt, 5) },
		"status baixo":            func(r *checker.Report) { r.Results[0].Attempts[0].Status = 99 },
		"status alto":             func(r *checker.Report) { r.Results[0].Attempts[0].Status = 1000 },
		"tentativa negativa":      func(r *checker.Report) { r.Results[0].Attempts[0].DurationMS = -1 },
		"código desconhecido":     func(r *checker.Report) { r.Results[0].Attempts[0].Code = "segredo" },
		"mensagem grande":         func(r *checker.Report) { r.Results[0].Attempts[0].Message = strings.Repeat("a", 257) },
		"mensagem com controle":   func(r *checker.Report) { r.Results[0].Attempts[0].Message = "segredo\x1b[0m" },
		"mensagem inválida":       func(r *checker.Report) { r.Results[0].Attempts[0].Message = string([]byte{0xff}) },
		"passed contradiz código": func(r *checker.Report) { r.Results[0].Passed = false; r.Passed, r.Failed = 0, 1 },
		"cancelamento contraditório": func(r *checker.Report) {
			r.Results[0].Passed, r.Results[0].Attempts[0].Code = false, "canceled"
			r.Passed, r.Failed = 0, 1
		},
		"contagem aprovada": func(r *checker.Report) { r.Passed = 0 },
		"contagem falhou":   func(r *checker.Report) { r.Failed = 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			input := sampleReport()
			mutate(&input)
			err := Validate(input)
			if err == nil || strings.Contains(err.Error(), "segredo") || strings.Contains(err.Error(), "confidencial") {
				t.Fatalf("relatório inválido aceito ou erro expôs entrada: %v", err)
			}
		})
	}
}

func TestValidateAllowsLimitsAndLateCancellation(t *testing.T) {
	input := sampleReport()
	input.Canceled = true // Cancellation can happen after all checks finish.
	input.Results[0].Name = strings.Repeat("á", 60)
	input.Results[0].ID = strings.Repeat("A", 64)
	attempt := input.Results[0].Attempts[0]
	attempt.Status, attempt.Message = 599, strings.Repeat("é", 256)
	input.Results[0].Attempts = []checker.Attempt{attempt, attempt, attempt, attempt}
	before := encodeReport(t, input)
	if err := Validate(input); err != nil {
		t.Fatal(err)
	}
	if after := encodeReport(t, input); after != before {
		t.Fatal("validação alterou relatório")
	}
}

func TestValidateAllowsNamesAndIDsInSeparateNamespaces(t *testing.T) {
	first, second := sampleResult("first", true), sampleResult("api", true)
	first.ID = "api"
	if err := Validate(sampleReport(first, second)); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeRejectsUnknownFieldsAtEveryLevel(t *testing.T) {
	data := encodeReport(t, sampleReport())
	for _, key := range []string{`"schema_version":1`, `"name":"Saúde da API"`, `"status":200`} {
		input := strings.Replace(data, key, key+`,"url":"https://segredo.test"`, 1)
		_, err := Decode(bytes.NewBufferString(input))
		if err == nil || strings.Contains(err.Error(), "segredo") {
			t.Fatalf("campo extra aceito ou exposto: %v", err)
		}
	}
}
