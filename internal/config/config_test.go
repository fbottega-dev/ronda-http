package config

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

const minimalJSON = `{"version":1,"targets":[{"name":"Saúde","url":"https://example.test/health?key=private"}]}`

func TestLoadDefaultsAndKeepsQuery(t *testing.T) {
	got, err := Load(strings.NewReader(minimalJSON))
	if err != nil {
		t.Fatal(err)
	}
	target := got.Targets[0]
	if got.Version != 1 || target.Method != "GET" || target.TimeoutMS != 3000 || len(target.ExpectStatus) != 1 || target.ExpectStatus[0] != 200 {
		t.Fatalf("defaults incorretos: %+v", got)
	}
	if target.URL != "https://example.test/health?key=private" || target.Retries != 0 || target.MaxLatencyMS != 0 {
		t.Fatalf("configuração alterada: %+v", target)
	}
}

func TestLoadRejectsInvalidJSONAndExplicitTimeouts(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"empty", ""},
		{"syntax", `{"version":`},
		{"trailing_object", minimalJSON + `{}`},
		{"trailing_scalar", minimalJSON + ` true`},
		{"root_array", `[]`},
		{"root_null", `null`},
		{"missing_version", `{"targets":[{"name":"a","url":"https://example.test"}]}`},
		{"string_version", `{"version":"1","targets":[]}`},
		{"unknown_root", `{"version":1,"secret-field":"x","targets":[]}`},
		{"unknown_target", `{"version":1,"targets":[{"name":"a","url":"https://example.test","secret-field":"x"}]}`},
		{"root_case", strings.Replace(minimalJSON, `"version"`, `"Version"`, 1)},
		{"target_case", strings.Replace(minimalJSON, `"url"`, `"URL"`, 1)},
		{"duplicate_root", strings.Replace(minimalJSON, `"version":1`, `"version":1,"version":1`, 1)},
		{"duplicate_target", strings.Replace(minimalJSON, `"name":"Saúde"`, `"name":"Saúde","name":"Outra"`, 1)},
		{"escaped_duplicate", strings.Replace(minimalJSON, `"name":"Saúde"`, `"name":"Saúde","\u006eame":"Outra"`, 1)},
		{"null_target", `{"version":1,"targets":[null]}`},
		{"method_null", targetJSON(`"method":null`)},
		{"status_null", targetJSON(`"expect_status":null`)},
		{"latency_null", targetJSON(`"max_latency_ms":null`)},
		{"contains_null", targetJSON(`"contains":null`)},
		{"token_env_null", targetJSON(`"token_env":null`)},
		{"retries_null", targetJSON(`"retries":null`)},
		{"invalid_utf8", strings.Replace(minimalJSON, "Saúde", string([]byte{255}), 1)},
		{"timeout_zero", targetJSON(`"timeout_ms":0`)},
		{"timeout_null", targetJSON(`"timeout_ms":null`)},
		{"timeout_low", targetJSON(`"timeout_ms":99`)},
		{"timeout_high", targetJSON(`"timeout_ms":30001`)},
		{"timeout_fraction", targetJSON(`"timeout_ms":100.5`)},
		{"status_fraction", targetJSON(`"expect_status":[200.5]`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(strings.NewReader(test.json))
			if err == nil {
				t.Fatal("esperava erro")
			}
			for _, secret := range []string{"https://", "secret-field", "private"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("erro expõe entrada: %v", err)
				}
			}
		})
	}
}

func targetJSON(extra string) string {
	return `{"version":1,"targets":[{"name":"Exemplo","url":"https://example.test",` + extra + `}]}`
}

func TestLoadExactSizeLimit(t *testing.T) {
	exact := minimalJSON + strings.Repeat(" ", maxConfigBytes-len(minimalJSON))
	if _, err := Load(strings.NewReader(exact)); err != nil {
		t.Fatalf("arquivo com exatamente 1 MiB: %v", err)
	}
	if _, err := Load(strings.NewReader(exact + " ")); err == nil {
		t.Fatal("arquivo maior que 1 MiB foi aceito")
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) {
	return 0, errors.New("secret reader path https://private.test")
}

func TestLoadBrokenReader(t *testing.T) {
	for _, reader := range []io.Reader{nil, brokenReader{}, io.MultiReader(strings.NewReader(minimalJSON), brokenReader{})} {
		if _, err := Load(reader); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatalf("erro de leitura ausente ou com segredo: %v", err)
		}
	}
}

func validTarget() Target {
	return Target{Name: "Saúde", URL: "https://example.test/health"}
}

func TestValidateTargetRules(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Target)
		valid  bool
	}{
		{"name_empty", func(t *Target) { t.Name = "" }, false},
		{"name_blank", func(t *Target) { t.Name = "  " }, false},
		{"name_newline", func(t *Target) { t.Name = "Saúde\n" }, false},
		{"name_tab", func(t *Target) { t.Name = "a\tb" }, false},
		{"name_control", func(t *Target) { t.Name = "a\u0085b" }, false},
		{"name_sixty_runes", func(t *Target) { t.Name = strings.Repeat("á", 60) }, true},
		{"name_too_long", func(t *Target) { t.Name = strings.Repeat("á", 61) }, false},
		{"name_invalid_utf8", func(t *Target) { t.Name = string([]byte{255}) }, false},
		{"relative_url", func(t *Target) { t.URL = "/health?secret" }, false},
		{"scheme_relative", func(t *Target) { t.URL = "//example.test/health" }, false},
		{"wrong_scheme", func(t *Target) { t.URL = "file:///secret" }, false},
		{"missing_host", func(t *Target) { t.URL = "https:///health" }, false},
		{"opaque_url", func(t *Target) { t.URL = "https:example.test" }, false},
		{"credentials", func(t *Target) { t.URL = "https://user:secret@example.test" }, false},
		{"fragment", func(t *Target) { t.URL = "https://example.test/#secret" }, false},
		{"empty_fragment", func(t *Target) { t.URL = "https://example.test/#" }, false},
		{"bad_escape", func(t *Target) { t.URL = "https://example.test/%secret" }, false},
		{"query_allowed", func(t *Target) { t.URL = "http://localhost:8080/?key=secret" }, true},
		{"ipv6_allowed", func(t *Target) { t.URL = "http://[::1]:8080/" }, true},
		{"post_disallowed", func(t *Target) { t.Method = "POST" }, false},
		{"head_allowed", func(t *Target) { t.Method = "HEAD" }, true},
		{"head_contains", func(t *Target) { t.Method, t.Contains = "HEAD", "secret" }, false},
		{"status_minimum", func(t *Target) { t.ExpectStatus = []int{200} }, true},
		{"status_maximum", func(t *Target) { t.ExpectStatus = []int{599} }, true},
		{"status_low", func(t *Target) { t.ExpectStatus = []int{199, 200} }, false},
		{"status_high", func(t *Target) { t.ExpectStatus = []int{600} }, false},
		{"timeout_minimum", func(t *Target) { t.TimeoutMS = 100 }, true},
		{"timeout_maximum", func(t *Target) { t.TimeoutMS = 30000 }, true},
		{"timeout_low", func(t *Target) { t.TimeoutMS = 99 }, false},
		{"timeout_high", func(t *Target) { t.TimeoutMS = 30001 }, false},
		{"timeout_negative", func(t *Target) { t.TimeoutMS = -1 }, false},
		{"latency_minimum", func(t *Target) { t.MaxLatencyMS = 1 }, true},
		{"latency_equal_timeout", func(t *Target) { t.TimeoutMS, t.MaxLatencyMS = 100, 100 }, true},
		{"latency_above_timeout", func(t *Target) { t.TimeoutMS, t.MaxLatencyMS = 100, 101 }, false},
		{"latency_above_default", func(t *Target) { t.MaxLatencyMS = 3001 }, false},
		{"latency_negative", func(t *Target) { t.MaxLatencyMS = -1 }, false},
		{"contains_limit", func(t *Target) { t.Contains = strings.Repeat("ç", 256) }, true},
		{"contains_above_limit", func(t *Target) { t.Contains = strings.Repeat("ç", 257) }, false},
		{"retries_maximum", func(t *Target) { t.Retries = 2 }, true},
		{"retries_above_limit", func(t *Target) { t.Retries = 3 }, false},
		{"retries_negative", func(t *Target) { t.Retries = -1 }, false},
		{"token_env_valid", func(t *Target) { t.TokenEnv = "_RONDA_TOKEN2" }, true},
		{"token_env_digit", func(t *Target) { t.TokenEnv = "2TOKEN" }, false},
		{"token_env_symbol", func(t *Target) { t.TokenEnv = "TOKEN-SECRET" }, false},
		{"token_env_unicode", func(t *Target) { t.TokenEnv = "SEGREDÓ" }, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := validTarget()
			test.change(&target)
			_, err := Validate(Config{Version: 1, Targets: []Target{target}})
			if (err == nil) != test.valid {
				t.Fatalf("válido=%t, erro=%v", test.valid, err)
			}
			if err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "https://")) {
				t.Fatalf("erro expõe entrada: %v", err)
			}
		})
	}
}

func TestValidateCollectionAndVersion(t *testing.T) {
	for _, version := range []int{0, 2, -1} {
		if _, err := Validate(Config{Version: version, Targets: []Target{validTarget()}}); err == nil {
			t.Errorf("version %d aceito", version)
		}
	}
	for _, count := range []int{0, 1, 100, 101} {
		t.Run(fmt.Sprintf("targets_%d", count), func(t *testing.T) {
			input := Config{Version: 1, Targets: make([]Target, count)}
			for i := range input.Targets {
				input.Targets[i] = validTarget()
				input.Targets[i].Name = fmt.Sprintf("Destino %d", i)
			}
			_, err := Validate(input)
			if (err == nil) != (count >= 1 && count <= 100) {
				t.Fatalf("quantidade %d, erro: %v", count, err)
			}
		})
	}
	duplicate := validTarget()
	duplicate.Name = " Saúde "
	if _, err := Validate(Config{Version: 1, Targets: []Target{validTarget(), duplicate}}); err == nil {
		t.Fatal("nomes duplicados após remover espaços foram aceitos")
	}
}

func TestValidateDoesNotMutateInput(t *testing.T) {
	input := Config{Version: 1, Targets: []Target{validTarget()}}
	input.Targets[0].Name = " Saúde "
	input.Targets[0].ExpectStatus = []int{200, 204}
	got, err := Validate(input)
	if err != nil {
		t.Fatal(err)
	}
	got.Targets[0].ExpectStatus[0] = 500
	if input.Targets[0].Name != " Saúde " || input.Targets[0].TimeoutMS != 0 || input.Targets[0].Method != "" || input.Targets[0].ExpectStatus[0] != 200 {
		t.Fatal("Validate alterou a configuração de entrada")
	}
}
