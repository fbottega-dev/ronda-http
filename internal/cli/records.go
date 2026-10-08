package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fbottega-dev/ronda-http/internal/checker"
	"github.com/fbottega-dev/ronda-http/internal/history"
	reports "github.com/fbottega-dev/ronda-http/internal/report"
)

func showHistory(args []string, out, errOut io.Writer) int {
	set := flags("history", errOut)
	dir := set.String("dir", ".ronda/history", "diretório do histórico local")
	limit := set.Int("limit", 20, "quantidade de execuções recentes, de 1 a 200")
	show := set.String("show", "", "ID da execução que será exibida por completo")
	format := set.String("format", "text", "formato da saída: text ou json")
	if ok, code := parsed(set, args, errOut); !ok {
		return code
	}
	var showProvided bool
	set.Visit(func(option *flag.Flag) { showProvided = showProvided || option.Name == "show" })
	if showProvided && strings.TrimSpace(*show) == "" {
		return fail(errOut, "show exige um ID não vazio")
	}
	if strings.TrimSpace(*dir) == "" || *limit < 1 || *limit > 200 {
		return fail(errOut, "dir deve ser um caminho não vazio e limit deve estar entre 1 e 200")
	}
	if *format != "text" && *format != "json" {
		return fail(errOut, "format deve ser text ou json")
	}
	if *show != "" {
		report, err := history.Load(*dir, *show)
		if err != nil {
			return fail(errOut, err.Error())
		}
		if *format == "json" {
			return writeJSON(out, errOut, report)
		}
		if err := renderText(out, report); err != nil {
			return fail(errOut, "não foi possível escrever a saída")
		}
		return 0
	}
	entries, err := history.List(*dir, *limit)
	if err != nil {
		return fail(errOut, err.Error())
	}
	if *format == "json" {
		return writeJSON(out, errOut, entries)
	}
	var text strings.Builder
	fmt.Fprintln(&text, "HISTÓRICO LOCAL · execuções mais recentes primeiro")
	if len(entries) == 0 {
		fmt.Fprintln(&text, "Nenhuma execução salva. Use check --history-dir para começar.")
	}
	for _, entry := range entries {
		state := "concluída"
		if entry.Canceled {
			state = "interrompida"
		}
		fmt.Fprintf(&text, "%s  %d aprovados · %d falharam · %s\n", entry.ID, entry.Passed, entry.Failed, state)
	}
	return printResult(out, errOut, text.String())
}

func compareReports(args []string, out, errOut io.Writer) int {
	set := flags("compare", errOut)
	beforePath := set.String("before", "", "arquivo JSON da execução anterior")
	afterPath := set.String("after", "", "arquivo JSON da execução posterior")
	format := set.String("format", "text", "formato da saída: text ou json")
	if ok, code := parsed(set, args, errOut); !ok {
		return code
	}
	if strings.TrimSpace(*beforePath) == "" || strings.TrimSpace(*afterPath) == "" {
		return fail(errOut, "compare exige --before e --after com os caminhos dos relatórios JSON")
	}
	if *format != "text" && *format != "json" {
		return fail(errOut, "format deve ser text ou json")
	}
	before, err := readReport(*beforePath)
	if err != nil {
		return fail(errOut, err.Error())
	}
	after, err := readReport(*afterPath)
	if err != nil {
		return fail(errOut, err.Error())
	}
	comparison, err := reports.Compare(before, after)
	if err != nil {
		return fail(errOut, err.Error())
	}
	if *format == "json" {
		if code := writeJSON(out, errOut, comparison); code != 0 {
			return code
		}
	} else {
		labels := map[string]string{
			"added": "ADICIONADO", "removed": "REMOVIDO", "regressed": "REGRESSÃO",
			"recovered": "RECUPERADO", "unchanged": "SEM MUDANÇA",
		}
		var text strings.Builder
		fmt.Fprintln(&text, "COMPARAÇÃO · resultado final de cada destino")
		for _, change := range comparison.Changes {
			fmt.Fprintf(&text, "[%s] %s (%s)\n", labels[change.Status], change.Name, change.Key)
		}
		fmt.Fprintf(&text, "\nRegressões: %d · recuperações: %d · adicionados: %d · removidos: %d · sem mudança: %d\n",
			comparison.Regressed, comparison.Recovered, comparison.Added, comparison.Removed, comparison.Unchanged)
		if code := printResult(out, errOut, text.String()); code != 0 {
			return code
		}
	}
	if comparison.Regressed > 0 {
		return 1
	}
	return 0
}

func readReport(path string) (checker.Report, error) {
	file, err := os.Open(path)
	if err != nil {
		return checker.Report{}, errors.New("não foi possível abrir o relatório JSON")
	}
	report, decodeErr := reports.Decode(file)
	closeErr := file.Close()
	if decodeErr != nil {
		return checker.Report{}, decodeErr
	}
	if closeErr != nil {
		return checker.Report{}, errors.New("não foi possível fechar o relatório JSON")
	}
	return report, nil
}

func writeJSON(out, errOut io.Writer, value any) int {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fail(errOut, "não foi possível escrever a saída JSON")
	}
	return 0
}
