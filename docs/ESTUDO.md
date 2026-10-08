# Guia de estudo do Ronda HTTP

Comece com `ronda demo` em um terminal e `ronda check --config examples/demo.json` em outro. Depois rode `examples/failures.json` e compare os diagnósticos. Use o executável para consultar o código de saída: `$LASTEXITCODE` no PowerShell ou `echo $?` no bash.

## Acompanhe uma verificação

1. `cmd/ronda/main.go` recebe argumentos e cria um contexto cancelável por Ctrl+C. O código de saída vem de `cli.Run`.
2. `internal/cli/cli.go` interpreta opções e abre a configuração. Mensagens de progresso ficam em stderr, deixando stdout disponível para JSON ou XML.
3. `internal/config/config.go` valida campos, aplica padrões e copia a configuração. Em `internal/cli/catalog.go`, a seleção por nome/grupo acontece antes da resolução de tokens e do HTTP.
4. `internal/checker/checker.go` confere tokens e distribui índices para trabalhadores. Cada trabalhador escreve numa posição diferente; `WaitGroup` impede agregar os resultados antes do fim.
5. `checkOnce` verifica status, conteúdo e duração. Falhas de serviços viram dados; erros de configuração impedem iniciar a execução.
6. A CLI grava os arquivos solicitados, apresenta o relatório e devolve o código de saída.

`internal/demo` fornece um ambiente fictício em loopback, sem precisar de APIs externas. Não representa uma API de produto.

## Reproduza as decisões novas

### Seleção sem efeitos colaterais

Rode `ronda validate --config examples/auth.json` e `ronda list --config examples/auth.json` sem definir token. Ambos devem funcionar sem rede. Compare com `check`, que exige o token selecionado. Leia `catalog_test.go`, `only_test.go` e `workflow_test.go`: contadores de requisições demonstram que serviços excluídos não são consultados. Conferir apenas a saída textual não provaria isso.

### Histórico sem banco

Rode `check --config examples/demo.json --history-dir .ronda/history` duas vezes. Confira os dois arquivos e use `history --show ID`. Leia `internal/history/history.go`: validação, arquivo temporário, sincronização, hard link e limpeza do temporário. O arquivo publicado é completo e nunca substitui outro, mesmo com dois processos simultâneos.

Um sufixo aleatório reduz colisões; o link exclusivo garante a preservação mesmo se uma colisão ocorrer. Retenção manual e suporte a hard links são limites explícitos desta solução. Os testes exercitam colisões e gravações concorrentes.

### Comparação por identidade

Salve `examples/demo.json` em `before.json` e `examples/regression.json` em `after.json` via `--output`. Execute `compare --before before.json --after after.json`: o catálogo deve regredir. Inverta os arquivos para observar a recuperação.

Em `internal/report/compare.go`, acompanhe os mapas por ID/nome. Renomear um destino com ID preserva sua identidade; sem ID, é remoção/adição. Repetir uma falha continua sendo resultado inalterado. Cancelamento não significa uma nova falha do serviço, por isso relatórios interrompidos são recusados na comparação.

### XML e validação de arquivos

Execute `check --config examples/failures.json --format junit --output failures.json > junit.xml`. O arquivo XML deve conter falhas; o arquivo de `--output` continua JSON. Leia `internal/report/junit.go`: o encoder faz escaping, sem concatenar XML manualmente. Destinos cancelados viram `skipped`, e a suíte registra o cancelamento.

Em `report.go`, veja por que `DisallowUnknownFields` sozinho não basta: o decoder padrão aceita chaves repetidas, nomes com outra caixa e alguns valores ausentes. A leitura estrita rejeita essas ambiguidades, limita o tamanho e confere contagens e identidades.

### Do código ao executável

Leia `.github/workflows/ci.yml`: matriz por sistema e Go, gofmt, vet, testes, build e detector de corrida. O empacotamento só acontece depois desses testes. `scripts/package` compila seis executáveis, insere a versão, inclui documentação/exemplos e calcula SHA-256. Testar um runner nativo e compilar para outra arquitetura são evidências diferentes.

## Fundamentos de Go

| Conceito | Onde observar |
| --- | --- |
| Módulo e pacotes internos | `go.mod`, `cmd` e `internal`. |
| Structs e tags JSON | `config.Target`, `checker.Report` e `history.Entry`. |
| Erros como retorno | `(valor, error)`; a CLI decide como exibir a falha. |
| Interfaces pequenas | `io.Reader` e `io.Writer` permitem testes com buffers. |
| `defer` | Fecha arquivos, respostas e conexões nos retornos antecipados. |
| Contexto | Cancelamento nas requisições e esperas entre tentativas. |
| Goroutines e canais | Trabalhadores recebem índices pelo canal `jobs`. |
| Sincronização | `WaitGroup` delimita o momento de ler os resultados. |
| Mapas e identidade | Correspondência estável entre duas execuções. |
| Testes por tabela | Mesma preparação, entradas e comportamentos distintos. |

Referências oficiais: [começar com Go](https://go.dev/doc/tutorial/getting-started), [testes](https://go.dev/doc/tutorial/add-a-test), [context](https://pkg.go.dev/context), [net/http](https://pkg.go.dev/net/http), [encoding/xml](https://pkg.go.dev/encoding/xml) e [os.Link](https://pkg.go.dev/os#Link).

## Perguntas para explicar o projeto

- Por que 200 com conteúdo errado pode falhar? Como timeout difere do limite de latência?
- Por que limitar concorrência e preservar ordem? Onde pode haver uma corrida de dados?
- Por que resolver todos os tokens selecionados antes de fazer a primeira requisição?
- Por que não seguir redirects automaticamente nem imprimir erros de transporte originais?
- Como evitar que um relatório parcialmente escrito apareça no histórico?
- O que acontece ao renomear um destino? Como evitar comparações enganosas após trocar um ID?
- Por que JSON é suficiente aqui, e quando um banco seria uma escolha melhor?
- O que SHA-256 confere, e por que ele não substitui uma assinatura de código?

Uma apresentação possível: “Desenvolvi uma CLI em Go para verificar serviços HTTP e comparar execuções. Usei concorrência limitada, cancelamento, validação de arquivos e relatórios para terminal/pipelines. Mantive os testes independentes de serviços externos e publiquei executáveis. Usei IA como apoio e consigo reproduzir o fluxo desde o comando até a requisição e o histórico.” Adapte essa frase ao que você realmente consegue demonstrar.

## Exercício independente

Em uma cópia local, acrescente uma verificação HEAD ao exemplo e crie um grupo só para ela. Explique por que `contains` deve ser rejeitado nesse caso. Depois escreva um teste de seleção que conte requisições e prove que os outros destinos ficaram de fora. Não é uma funcionalidade faltante na entrega: é uma forma de praticar o código existente.
