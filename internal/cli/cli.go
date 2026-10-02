// Package cli connects command-line input, the checker and human/JSON output.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fbottega-dev/ronda-http/internal/checker"
	"github.com/fbottega-dev/ronda-http/internal/config"
	"github.com/fbottega-dev/ronda-http/internal/demo"
)

const help = `RONDA HTTP · verificações de serviços pelo terminal

Uso:
  ronda init [--config ronda.json]
  ronda check [--config ronda.json] [--parallel 4] [--only NOME]
              [--format text|json] [--output report.json]
  ronda demo [--addr 127.0.0.1:8787]
  ronda version

Comece em dois terminais:
  1. ronda demo
  2. ronda init
     ronda check

init cria uma configuração de demonstração sem substituir arquivos.
check verifica status, conteúdo opcional e tempo de resposta.
--only seleciona um destino pelo nome exato da configuração.
--output salva o relatório JSON em um arquivo novo, além da saída normal.
demo inicia serviços fictícios locais; Ctrl+C encerra o servidor.

Saídas: 0 = tudo aprovado; 1 = verificação falhou;
        2 = configuração, comando ou arquivo inválido; 130 = interrompido.
Use "ronda COMANDO --help" para consultar as opções.
`

const initialConfig = `{
  "version": 1,
  "targets": [
    {
      "name": "Saúde da API",
      "url": "http://127.0.0.1:8787/health",
      "contains": "\"status\":\"ok\"",
      "max_latency_ms": 1000
    },
    {
      "name": "Catálogo de demonstração",
      "url": "http://127.0.0.1:8787/catalog",
      "contains": "Caderno"
    }
  ]
}
`

// Run returns a process exit code, keeping os.Exit outside the testable logic.
func Run(ctx context.Context, args []string, out, errOut io.Writer, version string) int {
	if len(args) == 0 {
		return printResult(out, errOut, help)
	}
	switch args[0] {
	case "help", "--help", "-h":
		return printResult(out, errOut, help)
	case "version":
		if len(args) != 1 {
			return fail(errOut, "version não recebe argumentos")
		}
		return printResult(out, errOut, fmt.Sprintf("Ronda HTTP %s\n", version))
	case "init":
		return initialize(args[1:], out, errOut)
	case "check":
		return check(ctx, args[1:], out, errOut)
	case "demo":
		return serveDemo(ctx, args[1:], out, errOut)
	default:
		return fail(errOut, "comando desconhecido; consulte ronda --help")
	}
}

func flags(name string, out io.Writer) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(out)
	set.Usage = func() {
		fmt.Fprintf(out, "Uso: ronda %s [opções]\n", name)
		set.PrintDefaults()
	}
	return set
}

func parsed(set *flag.FlagSet, args []string, errOut io.Writer) (bool, int) {
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, 0
		}
		return false, 2
	}
	if set.NArg() != 0 {
		return false, fail(errOut, "argumento inesperado; use as opções indicadas em --help")
	}
	return true, 0
}

func initialize(args []string, out, errOut io.Writer) int {
	set := flags("init", errOut)
	path := set.String("config", "ronda.json", "caminho do novo arquivo de configuração")
	if ok, code := parsed(set, args, errOut); !ok {
		return code
	}
	if err := writeNewFile(*path, []byte(initialConfig)); err != nil {
		return fail(errOut, err.Error())
	}
	return printResult(out, errOut, "Configuração criada. Inicie ronda demo em outro terminal e execute ronda check.\n")
}

func check(ctx context.Context, args []string, out, errOut io.Writer) int {
	set := flags("check", errOut)
	path := set.String("config", "ronda.json", "arquivo de configuração JSON")
	parallel := set.Int("parallel", 4, "verificações simultâneas, de 1 a 8")
	format := set.String("format", "text", "formato da saída: text ou json")
	output := set.String("output", "", "salvar JSON em um arquivo novo (não substitui existentes)")
	only := set.String("only", "", "verificar apenas o destino com este nome exato")
	if ok, code := parsed(set, args, errOut); !ok {
		return code
	}
	var onlyProvided bool
	set.Visit(func(option *flag.Flag) {
		if option.Name == "only" {
			onlyProvided = true
		}
	})
	if onlyProvided && strings.TrimSpace(*only) == "" {
		return fail(errOut, "only exige um nome de destino não vazio")
	}
	if *format != "text" && *format != "json" {
		return fail(errOut, "format deve ser text ou json")
	}
	if *parallel < 1 || *parallel > 8 {
		return fail(errOut, "parallel deve estar entre 1 e 8")
	}
	file, err := os.Open(*path)
	if err != nil {
		return fail(errOut, "não foi possível abrir a configuração; confira --config ou execute ronda init")
	}
	cfg, loadErr := config.Load(file)
	closeErr := file.Close()
	if loadErr != nil {
		return fail(errOut, loadErr.Error())
	}
	if closeErr != nil {
		return fail(errOut, "não foi possível fechar o arquivo de configuração")
	}
	if onlyProvided {
		// Validate the complete file first, then resolve tokens and run checks
		// only for the selected target. An unrelated missing token must not block it.
		var selected []config.Target
		for _, target := range cfg.Targets {
			if target.Name == *only {
				selected = []config.Target{target}
				break
			}
		}
		if len(selected) == 0 {
			return fail(errOut, "nenhum destino corresponde a --only; use o nome exato da configuração")
		}
		cfg.Targets = selected
	}
	if *format == "text" {
		fmt.Fprintf(errOut, "Verificando %d destinos...\n", len(cfg.Targets))
	}
	report, err := checker.Run(ctx, cfg, checker.Options{Parallel: *parallel})
	if err != nil {
		return fail(errOut, err.Error())
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fail(errOut, "não foi possível gerar o relatório JSON")
	}
	data = append(data, '\n')
	if *output != "" {
		if err := writeNewFile(*output, data); err != nil {
			return fail(errOut, err.Error())
		}
		fmt.Fprintln(errOut, "Relatório JSON salvo.")
	}
	if *format == "json" {
		_, err = out.Write(data)
	} else {
		err = renderText(out, report)
	}
	if err != nil {
		return fail(errOut, "não foi possível escrever a saída")
	}
	if report.Canceled {
		return 130
	}
	if report.Failed > 0 {
		return 1
	}
	return 0
}

func serveDemo(ctx context.Context, args []string, out, errOut io.Writer) int {
	set := flags("demo", errOut)
	addr := set.String("addr", "127.0.0.1:8787", "IP de loopback e porta para a demonstração")
	if ok, code := parsed(set, args, errOut); !ok {
		return code
	}
	if err := demo.Serve(ctx, *addr, out); err != nil {
		return fail(errOut, err.Error())
	}
	return 0
}

func writeNewFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("o arquivo já existe; escolha outro caminho para preservar o conteúdo")
		}
		return errors.New("não foi possível criar o arquivo; confira o caminho e as permissões")
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		// Remove only the incomplete file just created by this operation.
		_ = os.Remove(path)
		return errors.New("não foi possível salvar o arquivo completo")
	}
	return nil
}

func fail(out io.Writer, message string) int {
	fmt.Fprintf(out, "Erro: %s.\n", message)
	return 2
}

func printResult(out, errOut io.Writer, message string) int {
	if _, err := io.WriteString(out, message); err != nil {
		return fail(errOut, "não foi possível escrever a saída")
	}
	return 0
}

func renderText(out io.Writer, report checker.Report) error {
	var text strings.Builder
	fmt.Fprintln(&text, "\nRONDA HTTP")
	fmt.Fprintln(&text, "Verificação de serviços · resultado da execução")
	fmt.Fprintln(&text, strings.Repeat("─", 66))
	for _, result := range report.Results {
		status := "FALHOU"
		if result.Passed {
			status = "OK"
		}
		fmt.Fprintf(&text, "\n  [%s] %s\n", status, result.Name)
		if len(result.Attempts) == 0 {
			fmt.Fprintln(&text, "  Sem tentativa concluída.")
			continue
		}
		last := result.Attempts[len(result.Attempts)-1]
		code := "sem resposta"
		if last.Status != 0 {
			code = fmt.Sprintf("HTTP %d", last.Status)
		}
		fmt.Fprintf(&text, "  %s · %d ms · tentativas: %d\n", code, last.DurationMS, len(result.Attempts))
		fmt.Fprintf(&text, "  %s\n", last.Message)
	}
	fmt.Fprintln(&text, "\n"+strings.Repeat("─", 66))
	fmt.Fprintf(&text, "%d aprovados · %d falharam · %d ms no total\n", report.Passed, report.Failed, report.DurationMS)
	if report.Canceled {
		fmt.Fprintln(&text, "Execução interrompida. O relatório inclui os destinos cancelados.")
	}
	if report.Failed > 0 {
		fmt.Fprintln(&text, "Confira os destinos que falharam e execute novamente após o ajuste.")
	}
	_, err := io.WriteString(out, text.String())
	return err
}
