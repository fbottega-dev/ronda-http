package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestLoadGroupsAndIDsRemainOptional(t *testing.T) {
	legacy, err := Load(strings.NewReader(minimalJSON))
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Targets[0].ID != "" || legacy.Targets[0].Groups != nil {
		t.Fatalf("metadados foram inventados para configuração antiga: %+v", legacy.Targets[0])
	}
	got, err := Load(strings.NewReader(targetJSON(`"id":"health_1-B","groups":[" produção ","Crítico"]`)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.Targets[0].ID != "health_1-B" || !reflect.DeepEqual(got.Targets[0].Groups, []string{"produção", "Crítico"}) {
		t.Fatalf("metadados incorretos: %+v", got)
	}
	if _, err := Load(strings.NewReader(targetJSON(`"groups":[]`))); err != nil {
		t.Fatalf("lista vazia é válida: %v", err)
	}
}

func TestLoadRejectsInvalidGroupsAndIDs(t *testing.T) {
	for _, field := range []string{
		`"id":null`, `"id":""`, `"id":1`, `"ID":"health"`,
		`"id":"health","id":"other"`,
		`"groups":null`, `"groups":"produção"`, `"groups":[null]`,
		`"groups":[1]`, `"Groups":[]`, `"groups":[],"groups":[]`,
	} {
		t.Run(field, func(t *testing.T) {
			if _, err := Load(strings.NewReader(targetJSON(field))); err == nil {
				t.Fatal("campo inválido aceito")
			}
		})
	}
}

func TestValidateGroupsBoundariesAndDuplicates(t *testing.T) {
	tenGroups := make([]string, 10)
	for i := range tenGroups {
		tenGroups[i] = fmt.Sprintf("group-%d", i)
	}
	tests := []struct {
		name   string
		groups []string
		valid  bool
	}{
		{"absent", nil, true},
		{"empty_list", []string{}, true},
		{"empty_name", []string{""}, false},
		{"blank_name", []string{" \u00a0 "}, false},
		{"forty_runes", []string{strings.Repeat("á", 40)}, true},
		{"over_forty_runes", []string{strings.Repeat("á", 41)}, false},
		{"ten_groups", tenGroups, true},
		{"eleven_groups", append(append([]string(nil), tenGroups...), "other"), false},
		{"duplicate", []string{"prod", "prod"}, false},
		{"duplicate_after_trim", []string{"prod", " prod "}, false},
		{"case_sensitive", []string{"prod", "Prod"}, true},
		{"newline", []string{"prod\n"}, false},
		{"control", []string{"prod\u0085"}, false},
		{"invalid_utf8", []string{string([]byte{255})}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := validTarget()
			target.Groups = test.groups
			_, err := Validate(Config{Version: 1, Targets: []Target{target}})
			if (err == nil) != test.valid {
				t.Fatalf("válido=%t, erro=%v", test.valid, err)
			}
		})
	}
}

func TestValidateIDBoundariesAndUniqueness(t *testing.T) {
	for _, id := range []string{"", "a", "0", "Health_1-prod", strings.Repeat("a", 64)} {
		target := validTarget()
		target.ID = id
		got, err := Validate(Config{Version: 1, Targets: []Target{target}})
		if err != nil || got.Targets[0].ID != id {
			t.Fatalf("ID válido %q: %v", id, err)
		}
	}
	for _, id := range []string{"-health", "_health", " health", "health ", "health.id", "saúde", strings.Repeat("a", 65), "health\n"} {
		target := validTarget()
		target.ID = id
		if _, err := Validate(Config{Version: 1, Targets: []Target{target}}); err == nil {
			t.Errorf("ID inválido %q aceito", id)
		}
	}
	first, second := validTarget(), validTarget()
	first.ID, second.ID = "api", "api"
	second.Name = "Outra API"
	if _, err := Validate(Config{Version: 1, Targets: []Target{first, second}}); err == nil {
		t.Fatal("IDs duplicados aceitos")
	}
	second.ID = "API"
	if _, err := Validate(Config{Version: 1, Targets: []Target{first, second}}); err != nil {
		t.Fatalf("IDs devem diferenciar maiúsculas: %v", err)
	}
	// An explicit ID equal to another target's name is valid: names and IDs
	// occupy different namespaces, and absent IDs are never inferred.
	first.Name, first.ID = "api", ""
	second.ID = "api"
	if _, err := Validate(Config{Version: 1, Targets: []Target{first, second}}); err != nil {
		t.Fatalf("ID explícito colidiu com um nome sem ID: %v", err)
	}
}

func TestValidateMetadataDoesNotMutateInput(t *testing.T) {
	input := Config{Version: 1, Targets: []Target{validTarget(), validTarget()}}
	input.Targets[1].Name = "Outra API"
	input.Targets[0].ID = "health"
	input.Targets[0].Groups = []string{" prod ", "critical"}
	input.Targets[1].Groups = []string{" prod "}
	before, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Validate(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Targets[0].Groups[0] != "prod" || got.Targets[1].Groups[0] != "prod" {
		t.Fatal("grupos não foram normalizados ou compartilhamento entre destinos rejeitado")
	}
	got.Targets[0].Groups[0] = "changed"
	got.Targets[0].ID = "changed"
	after, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("Validate modificou a entrada ou compartilhou o slice de grupos")
	}
}
