package report

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompareTransitionsOrderAndIdentity(t *testing.T) {
	oldName, newName := sampleResult("Nome antigo", true), sampleResult("Nome novo", false)
	oldName.ID, newName.ID = "api", "api"
	before := sampleReport(
		sampleResult("Removido primeiro", false), oldName,
		sampleResult("Recuperado", false), sampleResult("Estável", false),
		sampleResult("Removido último", true), sampleResult("Aprovado", true),
	)
	after := sampleReport(
		sampleResult("Adicionado", false), sampleResult("Estável", false),
		sampleResult("Recuperado", true), newName, sampleResult("Aprovado", true),
	)
	comparison, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Added != 1 || comparison.Removed != 2 || comparison.Regressed != 1 || comparison.Recovered != 1 || comparison.Unchanged != 2 || len(comparison.Changes) != 7 {
		t.Fatalf("contagens incorretas: %+v", comparison)
	}
	wantStatuses := []string{"added", "unchanged", "recovered", "regressed", "unchanged", "removed", "removed"}
	wantNames := []string{"Adicionado", "Estável", "Recuperado", "Nome novo", "Aprovado", "Removido primeiro", "Removido último"}
	for index, change := range comparison.Changes {
		if change.Name != wantNames[index] || change.Status != wantStatuses[index] {
			t.Fatalf("posição %d incorreta: %+v", index, change)
		}
		switch change.Status {
		case "added":
			if change.BeforePassed != nil || change.AfterPassed == nil || *change.AfterPassed {
				t.Fatalf("adição confundida com falha: %+v", change)
			}
		case "removed":
			if change.BeforePassed == nil || change.AfterPassed != nil {
				t.Fatalf("remoção confundida com falha: %+v", change)
			}
		case "regressed":
			if change.Key != "id:api" || change.BeforePassed == nil || !*change.BeforePassed || change.AfterPassed == nil || *change.AfterPassed {
				t.Fatalf("regressão incorreta: %+v", change)
			}
		case "recovered":
			if change.BeforePassed == nil || *change.BeforePassed || change.AfterPassed == nil || !*change.AfterPassed {
				t.Fatalf("recuperação incorreta: %+v", change)
			}
		}
	}
	data, err := json.Marshal(comparison)
	if err != nil || !strings.Contains(string(data), `"before_passed":null`) || !strings.Contains(string(data), `"after_passed":false`) {
		t.Fatalf("JSON perde ausência/false: %s; %v", data, err)
	}
	// A second comparison must have exactly the same order, not map iteration order.
	for range 10 {
		next, err := Compare(before, after)
		nextJSON, _ := json.Marshal(next)
		if err != nil || string(nextJSON) != string(data) {
			t.Fatal("comparação não determinística")
		}
	}
}

func TestCompareLegacyRenameOrNewIDAreAddedAndRemoved(t *testing.T) {
	for _, scenario := range []string{"rename", "introduce ID"} {
		t.Run(scenario, func(t *testing.T) {
			before, after := sampleReport(), sampleReport()
			if scenario == "rename" {
				after.Results[0].Name = "Novo nome"
			} else {
				after.Results[0].ID = "api"
			}
			comparison, err := Compare(before, after)
			if err != nil || comparison.Added != 1 || comparison.Removed != 1 || comparison.Unchanged != 0 {
				t.Fatalf("inferiu identidade incorretamente: %+v; %v", comparison, err)
			}
		})
	}
}

func TestCompareMatchesNamesExactlyAndKeepsNamespacesSeparate(t *testing.T) {
	before := sampleReport(sampleResult("API", true))
	after := sampleReport(sampleResult("api", true))
	comparison, err := Compare(before, after)
	if err != nil || comparison.Added != 1 || comparison.Removed != 1 {
		t.Fatalf("nomes não são exatos: %+v; %v", comparison, err)
	}
	after.Results[0].ID = "API"
	comparison, err = Compare(before, after)
	if err != nil || comparison.Added != 1 || comparison.Removed != 1 {
		t.Fatalf("ID confundido com nome: %+v; %v", comparison, err)
	}
}

func TestCompareRejectsCanceledAndInvalidReports(t *testing.T) {
	for _, side := range []string{"before", "after"} {
		for _, invalid := range []bool{false, true} {
			before, after := sampleReport(), sampleReport()
			input := &before
			if side == "after" {
				input = &after
			}
			if invalid {
				input.Passed = 99
			} else {
				input.Canceled = true
			}
			comparison, err := Compare(before, after)
			if err == nil || len(comparison.Changes) != 0 {
				t.Fatalf("comparação parcial indevida: %+v; %v", comparison, err)
			}
			if !invalid && !strings.Contains(err.Error(), "cancelados") {
				t.Fatalf("motivo cancelamento ausente: %v", err)
			}
		}
	}
}
