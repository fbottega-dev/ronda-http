// Package config reads and validates the small, versioned JSON configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxConfigBytes = 1 << 20

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Config describes a single run. Version is required so future formats can be
// introduced without silently changing the meaning of an existing file.
type Config struct {
	Version int      `json:"version"`
	Targets []Target `json:"targets"`
}

// Target describes one HTTP check. Times are integer milliseconds.
type Target struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	Method       string `json:"method"`
	ExpectStatus []int  `json:"expect_status"`
	TimeoutMS    int    `json:"timeout_ms"`
	MaxLatencyMS int    `json:"max_latency_ms"`
	Contains     string `json:"contains"`
	TokenEnv     string `json:"token_env"`
	Retries      int    `json:"retries"`
}

// Load accepts up to 1 MiB of UTF-8 JSON, rejects unknown or duplicate keys,
// explicit null fields and trailing values, and applies Validate. JSON field
// names are case-sensitive. Unlike a
// zero-valued Target passed directly to Validate, an explicit timeout_ms in a
// file must be between 100 and 30000; zero and null are not defaults in JSON.
// Error messages deliberately omit the input and underlying decoder errors:
// URLs, expected response text and even malformed field names may be secrets.
func Load(reader io.Reader) (Config, error) {
	if reader == nil {
		return Config{}, errors.New("não foi possível ler a configuração")
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxConfigBytes+1))
	if err != nil {
		return Config{}, errors.New("não foi possível ler a configuração")
	}
	if len(data) > maxConfigBytes {
		return Config{}, errors.New("a configuração excede o limite de 1 MiB")
	}
	if !utf8.Valid(data) || !json.Valid(data) {
		return Config{}, errors.New("configuração JSON inválida; use um único objeto")
	}
	if err := uniqueKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return Config{}, err
	}
	var input Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return Config{}, errors.New("configuração JSON inválida: campo desconhecido ou tipo incorreto")
	}
	// Raw keys retain presence and exact spelling, which encoding/json normally
	// loses when decoding into a struct (field matching is case-insensitive).
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Config{}, errors.New("a configuração deve ser um objeto JSON")
	}
	for key, value := range fields {
		if key != "version" && key != "targets" {
			return Config{}, errors.New("campo desconhecido na configuração")
		}
		if bytes.Equal(value, []byte("null")) {
			return Config{}, errors.New("campos da configuração não aceitam null")
		}
	}
	var targets []map[string]json.RawMessage
	if err := json.Unmarshal(fields["targets"], &targets); err != nil {
		return Config{}, errors.New("targets deve ser uma lista de destinos")
	}
	for i, fields := range targets {
		for key, value := range fields {
			switch key {
			case "name", "url", "method", "expect_status", "timeout_ms", "max_latency_ms", "contains", "token_env", "retries":
			default:
				return Config{}, targetError(i, "campo desconhecido")
			}
			if bytes.Equal(value, []byte("null")) {
				return Config{}, targetError(i, "campos não aceitam null; omita um campo opcional para usar seu padrão")
			}
		}
		if _, present := fields["timeout_ms"]; present && (input.Targets[i].TimeoutMS < 100 || input.Targets[i].TimeoutMS > 30000) {
			return Config{}, targetError(i, "timeout_ms explícito deve estar entre 100 e 30000")
		}
	}
	return Validate(input)
}

// uniqueKeys walks a syntactically valid JSON value, remembering keys separately
// for each object. Repeated keys are rejected instead of using the last value.
func uniqueKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return errors.New("configuração JSON inválida")
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	seen := make(map[string]bool)
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return errors.New("configuração JSON inválida")
			}
			name := key.(string)
			if seen[name] {
				return errors.New("a configuração contém uma chave JSON duplicada")
			}
			seen[name] = true
		}
		if err := uniqueKeys(decoder); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return errors.New("configuração JSON inválida")
	}
	return nil
}

// Validate returns an independent configuration with defaults applied. It does
// not modify its argument. Names are trimmed and their uniqueness is sensitive
// to case; method names must be GET or HEAD. A zero TimeoutMS defaults to 3000.
func Validate(input Config) (Config, error) {
	if input.Version != 1 {
		return Config{}, errors.New("version é obrigatório e deve ser 1")
	}
	if len(input.Targets) < 1 || len(input.Targets) > 100 {
		return Config{}, errors.New("targets deve conter de 1 a 100 destinos")
	}
	result := Config{Version: input.Version, Targets: make([]Target, len(input.Targets))}
	names := make(map[string]bool, len(input.Targets))
	for i, target := range input.Targets {
		if !utf8.ValidString(target.Name) || strings.ContainsFunc(target.Name, unicode.IsControl) {
			return Config{}, targetError(i, "name deve ser um texto válido sem caracteres de controle")
		}
		target.Name = strings.TrimSpace(target.Name)
		if length := utf8.RuneCountInString(target.Name); length < 1 || length > 60 {
			return Config{}, targetError(i, "name deve ter de 1 a 60 caracteres")
		}
		if names[target.Name] {
			return Config{}, targetError(i, "name deve ser único")
		}
		names[target.Name] = true
		address, err := url.Parse(target.URL)
		if err != nil || !address.IsAbs() || (address.Scheme != "http" && address.Scheme != "https") || address.Hostname() == "" || address.User != nil || strings.Contains(target.URL, "#") {
			return Config{}, targetError(i, "url deve ser HTTP ou HTTPS absoluta, sem credenciais ou fragmento")
		}
		if target.Method == "" {
			target.Method = "GET"
		}
		if target.Method != "GET" && target.Method != "HEAD" {
			return Config{}, targetError(i, "method deve ser GET ou HEAD")
		}
		if len(target.ExpectStatus) == 0 {
			target.ExpectStatus = []int{200}
		} else {
			target.ExpectStatus = append([]int(nil), target.ExpectStatus...)
		}
		for _, status := range target.ExpectStatus {
			if status < 200 || status > 599 {
				return Config{}, targetError(i, "expect_status deve conter códigos entre 200 e 599")
			}
		}
		if target.TimeoutMS == 0 {
			target.TimeoutMS = 3000
		}
		if target.TimeoutMS < 100 || target.TimeoutMS > 30000 {
			return Config{}, targetError(i, "timeout_ms deve estar entre 100 e 30000")
		}
		if target.MaxLatencyMS < 0 || target.MaxLatencyMS > target.TimeoutMS {
			return Config{}, targetError(i, "max_latency_ms deve ser 0 ou estar entre 1 e timeout_ms")
		}
		if !utf8.ValidString(target.Contains) || utf8.RuneCountInString(target.Contains) > 256 {
			return Config{}, targetError(i, "contains deve ser texto com até 256 caracteres")
		}
		if target.Method == "HEAD" && target.Contains != "" {
			return Config{}, targetError(i, "contains não pode ser usado com HEAD")
		}
		if target.Retries < 0 || target.Retries > 2 {
			return Config{}, targetError(i, "retries deve estar entre 0 e 2")
		}
		if target.TokenEnv != "" && !environmentName.MatchString(target.TokenEnv) {
			return Config{}, targetError(i, "token_env deve ser um nome válido de variável de ambiente")
		}
		result.Targets[i] = target
	}
	return result, nil
}

func targetError(index int, message string) error {
	return fmt.Errorf("destino %d: %s", index+1, message)
}
