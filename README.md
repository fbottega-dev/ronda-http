# Ronda HTTP

**Projeto em desenvolvimento — etapa 1: verificações pelo terminal.**

Depois de alterar uma API, abrir uma página no navegador não confirma que todos os serviços continuam respondendo como esperado. O Ronda HTTP executa uma lista de verificações e informa quais passaram ou falharam. Serve para desenvolvedores e pequenas equipes que precisam de uma conferência rápida em ambientes locais ou de homologação.

Esta é a base de um projeto que será construído em etapas. Ainda faltam histórico, seleção por grupos e comparação entre execuções. O [roteiro de evolução](docs/EVOLUCAO.md) define os próximos incrementos; cada etapa deverá acrescentar uma funcionalidade utilizável e testes correspondentes.

## O que funciona nesta etapa

- Validação da configuração com `validate`, sem consultar serviços ou exigir tokens.
- Requisições GET e HEAD, com status esperado, prazo por requisição e limite opcional de latência.
- Busca literal de um texto na resposta, com limite de leitura de 1 MiB.
- Até oito verificações simultâneas, mantendo a ordem da configuração no relatório.
- Seleção de um destino pelo nome com `--only`, sem executar os demais.
- Até duas novas tentativas para falhas de rede ou respostas 5xx; falhas de TLS não são repetidas.
- Token Bearer obtido de variável de ambiente, sem gravar o valor no JSON de configuração.
- Saída legível no terminal, relatório JSON e códigos de saída para scripts.
- Servidor de demonstração local com respostas previsíveis, inclusive falhas para praticar.

Não há tela gráfica ou banco nesta etapa. A configuração e os relatórios são arquivos JSON; o histórico pesquisável ainda será implementado.

## Executar

Instale [Go 1.26 ou superior](https://go.dev/dl/) e Git. O módulo usa somente a biblioteca padrão: não há bibliotecas externas para baixar nem serviços de banco para configurar.

```sh
git clone https://github.com/fbottega-dev/ronda-http.git
cd ronda-http
go build -o bin/ ./cmd/ronda
```

Em **dois terminais**, dentro da pasta do projeto:

```sh
# Terminal 1: mantém o servidor fictício local aberto
go run ./cmd/ronda demo
```

```sh
# Terminal 2: cria a configuração inicial e verifica dois destinos
go run ./cmd/ronda init
go run ./cmd/ronda check

# Exemplo com quatro verificações aprovadas
go run ./cmd/ronda check --config examples/demo.json

# Exemplo com indisponibilidade, timeout e conteúdo inesperado
go run ./cmd/ronda check --config examples/failures.json
```

As falhas do último exemplo são respostas intencionais do servidor de demonstração para exercitar os diagnósticos. Encerre o servidor com Ctrl+C. A porta padrão é 8787; se estiver ocupada, use `demo --addr 127.0.0.1:8788` e ajuste as URLs de sua configuração.

Após compilar, substitua `go run ./cmd/ronda` por `./bin/ronda` no Linux/macOS ou `.\bin\ronda.exe` no PowerShell. **Para usar o código de saída em scripts, execute o binário**: `go run` envolve o processo e não preserva todos os códigos.

Exemplo ilustrativo de saída, com tempos que variam a cada execução:

```text
RONDA HTTP
Verificação de serviços · resultado da execução
──────────────────────────────────────────────────────────────────

  [OK] Saúde da API
  HTTP 200 · 2 ms · tentativas: 1
  Verificação aprovada.

  [FALHOU] Serviço indisponível
  HTTP 503 · 1 ms · tentativas: 2
  Status HTTP diferente do esperado.

──────────────────────────────────────────────────────────────────
1 aprovados · 1 falharam · 106 ms no total
```

## Configurar seus destinos

`ronda init` cria `ronda.json`, ignorado pelo Git, e nunca substitui um arquivo existente. Edite esse arquivo. Exemplo:

```json
{
  "version": 1,
  "targets": [
    {
      "name": "API de homologação",
      "url": "https://api.example.com/health",
      "expect_status": [200],
      "timeout_ms": 3000,
      "max_latency_ms": 1000,
      "contains": "ok",
      "retries": 1,
      "token_env": "RONDA_API_TOKEN"
    }
  ]
}
```

`api.example.com` é apenas um endereço ilustrativo. Substitua por seu serviço. Remova `token_env` quando não houver autenticação.

| Campo | Regra |
| --- | --- |
| `version` | Obrigatório; formato atual `1`. |
| `targets` | De 1 a 100 destinos. |
| `name` | Nome único, de 1 a 60 caracteres; aparece no relatório. |
| `url` | HTTP ou HTTPS absoluta, sem usuário/senha ou fragmento. |
| `method` | `GET` por padrão; aceita também `HEAD`. |
| `expect_status` | Lista de códigos de 200 a 599; omitida ou vazia significa `[200]`. |
| `timeout_ms` | 3000 quando omitido; se informado, entre 100 e 30000. |
| `max_latency_ms` | `0` desliga a regra; caso contrário, de 1 até `timeout_ms`. |
| `contains` | Texto literal de até 256 caracteres, diferenciando maiúsculas e minúsculas. Não aceita HEAD. |
| `retries` | Novas tentativas: 0 por padrão, máximo 2. |
| `token_env` | Nome da variável de ambiente com o token Bearer. |

A configuração deve ser UTF-8, até 1 MiB. Campos desconhecidos, duplicados, `null` e nomes de campos com caixa diferente são rejeitados. Erros indicam o número do destino, sem repetir seus valores.

No PowerShell, configure um token de teste com `$env:RONDA_API_TOKEN = 'seu-token-de-teste'`; no bash, use `export RONDA_API_TOKEN='seu-token-de-teste'`. Os valores devem ficar no ambiente, fora do repositório. Os tokens dos destinos selecionados são conferidos antes de iniciar qualquer requisição.

## Validar a configuração sem acessar a rede

Use `validate` depois de editar o arquivo ou antes de executar verificações em um pipeline:

```sh
go run ./cmd/ronda validate --config examples/demo.json
go run ./cmd/ronda validate --config examples/auth.json
```

Não é necessário iniciar o servidor de demonstração nem definir o token do segundo exemplo. O comando aplica as mesmas regras de configuração usadas por `check`, mas não resolve variáveis de token, envia requisições ou cria relatórios. Sem `--config`, lê `ronda.json`.

Quando o arquivo é válido, mostra somente a confirmação e a quantidade de destinos:

```text
Configuração válida. Destinos: 4.
```

Não exibe nomes, URLs, conteúdo esperado ou variáveis de token. O binário retorna `0` quando a configuração é válida e `2` em caso de erro de configuração, arquivo, argumento ou escrita da saída. Para consultar esse código em scripts, use o executável compilado. Essa validação verifica a estrutura e as regras do arquivo; não confirma disponibilidade do serviço, validade de um token ou conteúdo da resposta. Para isso, execute `check`.

## Verificar somente um destino

Com a demonstração aberta em outro terminal:

```sh
go run ./cmd/ronda check --config examples/demo.json --only "Saúde da API"
```

Use o nome exato do destino, respeitando maiúsculas, acentos e espaços internos. Nomes com espaços precisam de aspas. A configuração remove espaços externos de `name`; use esse nome sem acrescentar espaços ao argumento. Sem `--only`, todos os destinos continuam sendo executados.

O relatório e suas contagens incluem somente o destino escolhido. A opção funciona também com `--format json` e `--output`. Um nome vazio ou inexistente retorna código 2, sem iniciar requisições nem criar o relatório.

A configuração inteira continua sendo validada para detectar erros e nomes duplicados. Depois disso, somente o token do destino selecionado é exigido: não é necessário configurar tokens de serviços que não serão consultados.

## Relatórios e códigos de saída

```sh
# JSON puro em stdout, adequado para outro programa consumir
go run ./cmd/ronda check --config examples/demo.json --format json

# Terminal e arquivo JSON simultaneamente; escolha um arquivo ainda inexistente
go run ./cmd/ronda check --config examples/demo.json --output report.json

# Execução sequencial
go run ./cmd/ronda check --config examples/demo.json --parallel 1
```

O JSON tem `schema_version`, início em UTC, duração total, contagens e uma lista de resultados. Cada resultado contém nome, aprovação e tentativas com status, duração, código de diagnóstico e mensagem. Não inclui URL, cabeçalhos, token, corpo ou texto esperado. Use nomes descritivos **sem segredos**, pois `name` aparece nas saídas. Relatórios próprios podem conter informações do seu ambiente: revise antes de publicá-los.

| Código do binário | Significado |
| --- | --- |
| `0` | Todas as verificações passaram. |
| `1` | Pelo menos uma verificação falhou. |
| `2` | Configuração, comando, token ou operação de arquivo inválida. |
| `130` | Execução de `check` interrompida; relatório contém destinos cancelados. |

`--output` e `init` preservam arquivos existentes. Se o relatório não puder ser salvo, o comando termina com código 2. Cancelamento anterior ao início de um destino é registrado como uma entrada `canceled`, sem requisição HTTP; por isso o tamanho de `attempts` pode incluir essa entrada.

## Regras que afetam o resultado

- Redirecionamentos **não são seguidos**: configure o status 301/302 esperado ou use a URL final. Isso também evita encaminhar o token para outro endereço.
- Sem `contains`, a latência mede até receber os cabeçalhos. Com `contains`, inclui a leitura do corpo. O limite considera a duração precisa; a exibição arredonda para baixo em milissegundos.
- O prazo vale por tentativa. Esperas de 100 e 200 ms entre novas tentativas entram apenas na duração total.
- Um status 5xx configurado como esperado pode passar. Uma resposta aprovada não é repetida.
- Apenas status, timeout/rede, conteúdo e latência são conferidos. Um resultado aprovado não comprova o funcionamento completo de uma aplicação.
- Certificados HTTPS são verificados; não existe opção para desabilitar TLS. HTTP sem TLS é aceito para serviços locais: use HTTPS ao transmitir um token em uma rede.
- As variáveis de proxy reconhecidas pelo Go (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`) são respeitadas. A ferramenta acessa as URLs fornecidas; revise configurações de terceiros antes de executá-las.

## Testar e estudar

```sh
go test ./... -count=1 -cover
go vet ./...
go build -o bin/ ./cmd/ronda
```

Os testes usam servidores temporários locais (`httptest`), sem depender da disponibilidade de sites externos. A [verificação automática](https://github.com/fbottega-dev/ronda-http/actions/workflows/ci.yml) executa testes em Windows e Ubuntu com Go 1.26/1.27 e o detector de condições de corrida no Ubuntu. Para rodar `go test -race ./...` localmente, é necessário um compilador C compatível.

Comece pelo [guia de estudo](docs/ESTUDO.md). O [roteiro de evolução](docs/EVOLUCAO.md) descreve o que ainda falta e os critérios para cada etapa.

Go foi escolhido para praticar rede, cancelamento e concorrência com um executável simples e sem dependências externas. O portfólio já contém Java, C#, PHP, Kotlin e Python; aqui o problema pede outra experiência, voltada a ferramentas de desenvolvimento. Não há camadas de domínio artificiais: configuração, verificações, terminal e demonstração ficam em quatro pacotes internos.

## Limitações desta etapa

- Ainda não guarda histórico, compara execuções ou seleciona grupos de destinos.
- Sem agendamento, notificações, painel web ou monitoramento contínuo.
- Somente GET/HEAD e Bearer; sem fluxo de login, cookies persistentes ou corpo de requisição.
- `contains` procura texto literal, não interpreta JSON ou HTML.
- Relatório somente texto/JSON; sem formato JUnit e sem binários de release publicados.
- Configurações e relatórios ainda podem mudar de formato durante o desenvolvimento; alterações deverão ser documentadas.

Projeto de estudo desenvolvido com apoio de IA na implementação, revisão e testes. Não representa experiência profissional anterior. A responsabilidade de entender e verificar o código faz parte do trabalho de evolução.
