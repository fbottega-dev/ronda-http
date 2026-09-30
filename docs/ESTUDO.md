# Como estudar a primeira etapa

Comece executando os dois exemplos: `examples/demo.json` passa e `examples/failures.json` produz diagnósticos. Depois altere um status esperado e observe o resultado. Use o binário para consultar o código de saída: `$LASTEXITCODE` no PowerShell ou `echo $?` no bash.

## Acompanhe uma verificação

1. `cmd/ronda/main.go` recebe os argumentos e cria um contexto cancelável por Ctrl+C. O código de saída vem de `cli.Run`.
2. `internal/cli/cli.go` interpreta as opções, abre a configuração e chama `config.Load`. Mantém mensagens de progresso em stderr para que stdout possa conter JSON puro.
3. `internal/config/config.go` aplica valores padrão e rejeita configurações ambíguas. Antes de estudar toda a validação, acompanhe apenas os campos `url`, `expect_status` e `timeout_ms`.
4. `internal/checker/checker.go` resolve os tokens e distribui índices dos destinos entre um número limitado de trabalhadores. Cada um escreve em uma posição diferente da lista. `WaitGroup` garante que todos terminaram antes de contar os resultados.
5. `checkOnce` envia a requisição e verifica status, conteúdo e latência. Falhas esperadas viram dados de um `Attempt`; erros de configuração impedem iniciar a execução.
6. A CLI apresenta o relatório, salva JSON quando solicitado e devolve o código apropriado.

O servidor de `internal/demo` é apenas um ambiente fictício para reproduzir esses caminhos. Ele escuta somente em loopback; não é a API de um produto real.

## Fundamentos novos em Go

| Conceito | Onde observar |
| --- | --- |
| Módulo e pacotes | `go.mod`, pastas `cmd` e `internal`; `internal` restringe importação por outros módulos. |
| Structs e tags JSON | `config.Target` e `checker.Report`; tags definem nomes no arquivo. |
| Retorno de erros | `Load` retorna `(Config, error)`; quem chama decide como apresentar a falha. |
| Interfaces pequenas | `io.Reader`/`io.Writer` permitem testar leitura e saída com buffers. |
| `defer` | Fecha arquivos, respostas e conexões mesmo ao retornar antes do fim. |
| `context.Context` | Propaga cancelamento até requisições e esperas entre tentativas. |
| Goroutines e canais | Trabalhadores recebem índices pelo canal `jobs`. |
| Sincronização | `WaitGroup` impede ler resultados antes de todos os trabalhadores terminarem. |
| Testes por tabela | Casos de validação compartilham preparação, mas verificam entradas e regras distintas. |

Referências oficiais: [começar com Go](https://go.dev/doc/tutorial/getting-started), [criar um módulo](https://go.dev/doc/tutorial/create-module), [escrever testes](https://go.dev/doc/tutorial/add-a-test), [context](https://pkg.go.dev/context) e [net/http](https://pkg.go.dev/net/http).

## Perguntas para uma entrevista

- Por que uma resposta 200 com conteúdo errado deve falhar?
- Qual a diferença entre timeout e limite de latência?
- Por que repetir GET pode ser aceitável, mas não basta aplicar a mesma regra a um pagamento com POST?
- Como a ordem do relatório é preservada mesmo executando destinos em paralelo?
- Por que não incluir o erro de transporte original no relatório? Ele pode conter a URL e seus parâmetros.
- Por que não seguir um redirecionamento automaticamente quando há um token?
- Por que usar JSON nesta etapa e deixar banco para quando houver histórico?

Uma apresentação honesta: “Estou construindo uma ferramenta em Go para conferir serviços HTTP. Na primeira etapa implementei configuração, execução concorrente limitada e relatórios. Os testes usam servidores locais para reproduzir timeout, erro e redirecionamento. Ainda vou adicionar histórico e filtros. Usei IA como apoio e estudei o fluxo desde o comando até a requisição.” Adapte isso ao que você realmente consegue demonstrar.

## Uma mudança pequena para fazer sozinho

Adicione `--only NOME` a `check` para selecionar exatamente um destino antes da execução. Reutilize a configuração validada e retorne erro 2 quando o nome não existir. Escreva um teste que conte requisições e confirme que os outros destinos não foram chamados. Atualize a ajuda e o README. É um primeiro passo útil antes de implementar grupos.
