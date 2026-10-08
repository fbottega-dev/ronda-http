// Package report validates, exports and compares the safe data produced by checks.
package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fbottega-dev/ronda-http/internal/checker"
)

const maxReportBytes = 2 << 20

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// Decode reads one UTF-8 JSON report, including schema 1 reports written before
// optional result IDs existed. Errors never include input, decoder diagnostics
// or underlying I/O errors. The reader is limited even when it never reaches EOF.
func Decode(reader io.Reader) (checker.Report, error) {
	if reader == nil {
		return checker.Report{}, errors.New("não foi possível ler o relatório")
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxReportBytes+1))
	if err != nil {
		return checker.Report{}, errors.New("não foi possível ler o relatório")
	}
	if len(data) > maxReportBytes {
		return checker.Report{}, errors.New("o relatório excede o limite de 2 MiB")
	}
	if !utf8.Valid(data) || !json.Valid(data) {
		return checker.Report{}, errors.New("relatório JSON inválido; use um único objeto UTF-8")
	}
	if err := checkJSON(json.NewDecoder(bytes.NewReader(data)), reportFields); err != nil {
		return checker.Report{}, err
	}
	var result checker.Report
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return checker.Report{}, errors.New("relatório JSON inválido: campo desconhecido ou tipo incorreto")
	}
	if err := Validate(result); err != nil {
		return checker.Report{}, err
	}
	return result, nil
}

type objectKind int

const (
	reportFields objectKind = iota
	resultFields
	attemptFields
)

// checkJSON visits only the three known object shapes. Checking tokens first
// prevents encoding/json from silently accepting repeated or case-folded keys,
// null scalar values, and incomplete records whose zero values look legitimate.
func checkJSON(decoder *json.Decoder, kind objectKind) error {
	invalid := errors.New("relatório JSON inválido: campos ausentes, desconhecidos, duplicados ou null")
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return invalid
	}
	var required []string
	switch kind {
	case reportFields:
		required = []string{"schema_version", "started_at", "duration_ms", "passed", "failed", "canceled", "results"}
	case resultFields:
		required = []string{"name", "passed", "attempts"}
	case attemptFields:
		required = []string{"status", "duration_ms", "code", "message"}
	}
	allowed := make(map[string]bool, len(required)+1)
	for _, key := range required {
		allowed[key] = true
	}
	if kind == resultFields {
		allowed["id"] = true
	}
	seen := make(map[string]bool, len(allowed))
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || !allowed[key] || seen[key] {
			return invalid
		}
		seen[key] = true
		if (kind == reportFields && key == "results") || (kind == resultFields && key == "attempts") {
			token, err = decoder.Token()
			if err != nil || token != json.Delim('[') {
				return invalid
			}
			for decoder.More() {
				if err := checkJSON(decoder, kind+1); err != nil {
					return err
				}
			}
			token, err = decoder.Token()
			if err != nil || token != json.Delim(']') {
				return invalid
			}
		} else {
			token, err = decoder.Token()
			if err != nil || token == nil {
				return invalid
			}
			if _, delimiter := token.(json.Delim); delimiter {
				return invalid
			}
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return invalid
	}
	for _, key := range required {
		if !seen[key] {
			return invalid
		}
	}
	return nil
}

// Validate checks schema, identities, counts and outcomes without changing the
// report. This also protects callers creating reports directly rather than via
// Decode. It cannot establish that a user-supplied label or message is truthful;
// callers should only share reports from trusted sources.
func Validate(input checker.Report) error {
	if input.SchemaVersion != 1 {
		return errors.New("schema_version do relatório deve ser 1")
	}
	if input.StartedAt.IsZero() || input.DurationMS < 0 {
		return errors.New("relatório contém data ou duração inválida")
	}
	if len(input.Results) < 1 || len(input.Results) > 100 {
		return errors.New("o relatório deve conter de 1 a 100 resultados")
	}
	names := make(map[string]bool, len(input.Results))
	keys := make(map[string]bool, len(input.Results))
	passed := 0
	for _, result := range input.Results {
		if !validText(result.Name, 1, 60) || strings.TrimSpace(result.Name) != result.Name {
			return errors.New("relatório contém nome de destino inválido")
		}
		if result.ID != "" && !safeID.MatchString(result.ID) {
			return errors.New("relatório contém ID de destino inválido")
		}
		key := resultKey(result)
		if names[result.Name] || keys[key] {
			return errors.New("relatório contém nomes ou IDs repetidos")
		}
		names[result.Name], keys[key] = true, true
		if len(result.Attempts) < 1 || len(result.Attempts) > 4 {
			return errors.New("cada resultado deve conter de 1 a 4 tentativas")
		}
		for _, attempt := range result.Attempts {
			// Received status codes may be informational or non-standard. They
			// remain useful failed-check evidence even though configuration only
			// accepts expected statuses from 200 through 599.
			if attempt.DurationMS < 0 || (attempt.Status != 0 && (attempt.Status < 100 || attempt.Status > 999)) {
				return errors.New("relatório contém status HTTP ou duração inválida")
			}
			switch attempt.Code {
			case "ok", "status", "timeout", "network", "tls", "body_limit", "content", "latency", "canceled":
			default:
				return errors.New("relatório contém código de tentativa inválido")
			}
			if !validText(attempt.Message, 0, 256) {
				return errors.New("relatório contém mensagem inválida")
			}
		}
		last := result.Attempts[len(result.Attempts)-1]
		if result.Passed != (last.Code == "ok") || (last.Code == "canceled" && !input.Canceled) {
			return errors.New("relatório contém resultado ou cancelamento inconsistente")
		}
		if result.Passed {
			passed++
		}
	}
	if input.Passed != passed || input.Failed != len(input.Results)-passed {
		return errors.New("relatório contém contagens inconsistentes")
	}
	return nil
}

func validText(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return utf8.ValidString(value) && length >= minimum && length <= maximum && !strings.ContainsFunc(value, unicode.IsControl)
}

func resultKey(result checker.Result) string {
	if result.ID != "" {
		return "id:" + result.ID
	}
	return "name:" + result.Name
}
