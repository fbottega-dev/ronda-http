# Ronda HTTP

[![CI](https://github.com/fbottega-dev/ronda-http/actions/workflows/ci.yml/badge.svg)](https://github.com/fbottega-dev/ronda-http/actions/workflows/ci.yml)
[![Versão](https://img.shields.io/github/v/release/fbottega-dev/ronda-http)](https://github.com/fbottega-dev/ronda-http/releases)

**Confira seus serviços HTTP, guarde o resultado e descubra o que mudou.**

Depois de alterar uma API, abrir uma página no navegador não confirma que todos os serviços continuam respondendo como esperado. O Ronda HTTP executa verificações por arquivo de configuração e mostra status, conteúdo e tempo de resposta. Foi pensado para desenvolvedores e pequenas equipes em ambientes locais ou de homologação.

A versão 1.0 entrega o [escopo planejado](docs/EVOLUCAO.md): CLI, seleção por nome ou grupo, histórico local, comparação de execuções, JSON/JUnit e executáveis para Windows, Linux e macOS. Usa somente a biblioteca padrão do Go; não exige banco, conta ou serviço externo.

## Começar em dois minutos

Baixe o arquivo da sua plataforma em [Releases](https://github.com/fbottega-dev/ronda-http/releases/latest), confira o SHA-256 e extraia. `amd64` corresponde a processadores x64; `arm64`, a ARM64, incluindo Apple Silicon. No PowerShell, use `.\ronda.exe`; no Linux/macOS, `./ronda`. Os exemplos abaixo usam `ronda` supondo que ele esteja no PATH.

Em dois terminais, na pasta extraída:

```sh
# Terminal 1: serviços fictícios locais; Ctrl+C encerra
ronda demo
```

```sh
# Terminal 2
ronda init
ronda validate
ronda list
ronda check
```

`init` cria `ronda.json` sem substituir arquivos. A demonstração usa a porta 8787; se estiver ocupada, use `demo --addr 127.0.0.1:8788` e ajuste as URLs na configuração.

Exemplo ilustrativo (tempos variam):

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

Para compilar a partir do código, instale [Go 1.26 ou superior](https://go.dev/dl/) e Git:

```sh
git clone https://github.com/fbottega-dev/ronda-http.git
cd ronda-http
go build -o bin/ ./cmd/ronda
```

O executável fica em `bin/ronda` ou `bin/ronda.exe`. Durante o estudo, `go run ./cmd/ronda check` também funciona. **Use o binário em scripts:** `go run` envolve o processo e não preserva todos os códigos de saída.

## Comandos

| Comando | Uso |
| --- | --- |
| `init` | Criar a configuração de demonstração. |
| `validate --config ARQUIVO` | Validar sem rede, resolução de tokens ou relatórios. |
| `list --config ARQUIVO` | Mostrar nomes, métodos e grupos, na ordem do arquivo, sem URLs ou rede. |
| `check` | Executar verificações; aceita seleção, relatórios e histórico. |
| `history --dir PASTA` | Listar até 20 execuções recentes; `--limit` aceita 1 a 200. |
| `history --dir PASTA --show ID` | Consultar uma execução completa. |
| `compare --before ANTES.json --after DEPOIS.json` | Identificar regressões, recuperações, adições e remoções. |
| `demo` | Iniciar o servidor fictício em loopback. |
| `version` | Mostrar a versão do executável. |

`validate`, `list` e `check` usam `ronda.json` quando `--config` é omitido. Consulte `ronda COMANDO --help` para todas as opções.

## Configuração

```json
{
  "version": 1,
  "targets": [
    {
      "id": "api-health",
      "name": "Saúde da API",
      "groups": ["homologacao", "essencial"],
      "url": "https://api.example.com/health",
      "method": "GET",
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

`api.example.com` é ilustrativo: substitua por seu serviço. Omita `token_env` quando não houver autenticação. Nomes, IDs e grupos aparecem nas saídas; use rótulos sem segredos.

| Campo | Regra |
| --- | --- |
| `version` | Obrigatório; formato atual `1`. |
| `targets` | De 1 a 100 destinos. |
| `id` | Opcional e único: 1 a 64 letras ASCII, números, `_` ou `-`; começa com letra ou número. Identidade estável na comparação. |
| `name` | Nome único, de 1 a 60 caracteres, sem controles; espaços externos são removidos. |
| `groups` | Opcional; até 10 grupos distintos por destino, de 1 a 40 caracteres cada, sem controles. Espaços externos são removidos; `[]` significa sem grupo. |
| `url` | HTTP ou HTTPS absoluta, sem usuário/senha ou fragmento. |
| `method` | `GET` por padrão; aceita também `HEAD`. |
| `expect_status` | Códigos de 200 a 599; omitido ou `[]` significa `[200]`. |
| `timeout_ms` | 3000 quando omitido; se informado, entre 100 e 30000. |
| `max_latency_ms` | `0` desliga a regra; caso contrário, de 1 até `timeout_ms`. |
| `contains` | Texto literal de até 256 caracteres, diferenciando maiúsculas. Não aceita HEAD. |
| `retries` | Novas tentativas: 0 por padrão, máximo 2. |
| `token_env` | Nome da variável de ambiente com o token Bearer. |

O JSON deve ser UTF-8, até 1 MiB. Campos desconhecidos, duplicados, `null` e nomes de campos com caixa diferente são rejeitados. IDs, nomes e grupos diferenciam maiúsculas de minúsculas. Configurações anteriores sem `id` e `groups` continuam válidas.

No PowerShell, defina `$env:RONDA_API_TOKEN = 'seu-token-de-teste'`; no bash, `export RONDA_API_TOKEN='seu-token-de-teste'`. Valores ficam no ambiente, fora do repositório. `validate --config examples/auth.json` funciona sem definir esse token: valida o arquivo, não a autenticação.

## Selecionar serviços

Com `ronda demo` aberto:

```sh
ronda list --config examples/demo.json
ronda check --config examples/demo.json --only "Saúde da API"
ronda check --config examples/demo.json --group essencial
ronda check --config examples/demo.json --parallel 1
```

Sem filtro, todos os destinos são executados. `--only` e `--group` usam correspondência exata, são mutuamente exclusivos e rejeitam valores vazios ou inexistentes antes da rede. O arquivo inteiro é validado; depois, somente os destinos selecionados recebem requisições e têm seus tokens exigidos. A ordem e as contagens do relatório correspondem à seleção.

## JSON e JUnit

```sh
# JSON puro em stdout
ronda check --config examples/demo.json --format json

# Texto no terminal e arquivo JSON novo
ronda check --config examples/demo.json --output report.json

# XML JUnit em stdout e JSON em arquivo, simultaneamente
ronda check --config examples/failures.json --format junit --output failures.json > junit.xml
```

Mensagens de progresso ficam em stderr. **`--output` sempre grava JSON**, mesmo com `--format junit`; nunca substitui um arquivo existente. O redirecionamento `>` é do shell e pode substituir seu destino. No Windows PowerShell 5.1, use PowerShell 7 ou configure a gravação em UTF-8 para preservar a codificação declarada no XML.

O relatório JSON versão 1 contém início UTC, duração total, contagens, cancelamento e resultados com ID opcional, nome, aprovação e tentativas. Cada tentativa registra status, duração, código e mensagem genérica. Não inclui URL, cabeçalhos, tokens, corpo ou texto esperado.

JUnit representa cada destino como `testcase`: falha final vira `failure`; cancelamento vira `skipped`. A propriedade `ronda.canceled=true` indica execução interrompida. Tempos são segundos; o tempo de um caso soma suas tentativas, sem as esperas de repetição. O total mede a execução concorrente, portanto pode ser menor que a soma dos casos. Nomes são escapados como XML.

## Histórico local

```sh
ronda check --config examples/demo.json --history-dir .ronda/history
ronda history
ronda history --dir .ronda/history --limit 5 --format json
# Copie o ID exibido na listagem, sem .json:
ronda history --dir .ronda/history --show ID
```

O histórico é **opcional**: `check` só persiste execuções quando recebe `--history-dir`. Cada execução concluída, falha ou interrompida gera um arquivo JSON independente; erros de configuração não geram execução. O ID combina o instante UTC de início com um sufixo aleatório. A listagem ordena pelo início; empates usam o sufixo, sem prometer ordem de criação.

A publicação usa arquivo temporário sincronizado e hard link para evitar arquivos parciais e sobrescritas, inclusive entre processos. A pasta precisa estar em um sistema com hard links, como NTFS, ext4 ou APFS; FAT/exFAT não são suportados para o histórico. Os modos solicitados são 0700 para a pasta e 0600 para arquivos; no Windows, permissões efetivas dependem das ACLs da pasta.

Não há exclusão automática: arquive ou remova manualmente os JSON antigos quando desejar. `.ronda/` é ignorada pelo Git. Listar uma pasta inexistente retorna vazio, sem criá-la. Arquivos estranhos e temporários são ignorados; uma execução selecionada corrompida produz erro, sem ser escondida.

O formato é o mesmo JSON versão 1 dos relatórios. Versões incompatíveis são rejeitadas; ainda não existe migração porque esta é a primeira versão persistida. Relatórios antigos sem ID são aceitos. A leitura tem limite de 2 MiB por relatório. IDs não são caminhos: `--show` rejeita travessia de diretórios.

## Comparar execuções

Uma demonstração reproduzível, sem editar arquivos (escolha nomes de saída ainda inexistentes):

```sh
ronda check --config examples/demo.json --output before.json
ronda check --config examples/regression.json --output after.json
ronda compare --before before.json --after after.json
ronda compare --before after.json --after before.json --format json
```

O segundo exemplo altera o status esperado do catálogo, causando uma regressão intencional. A comparação inversa mostra uma recuperação. Também é possível passar os caminhos `.ronda/history/ID.json` de duas execuções salvas.

O mesmo `id` identifica o mesmo destino mesmo após renomear `name`. Sem ID, a identidade é o nome exato: renomeá-lo resulta em remoção e adição. Acrescentar ou retirar ID também muda a identidade. Mantenha os IDs e o conjunto selecionado consistentes ao comparar.

Regressão significa **aprovado → falhou**; recuperação, **falhou → aprovado**. Adicionados/removidos são mostrados separadamente. Dois resultados que continuam falhando são classificados como sem mudança, mesmo se o diagnóstico mudou; não há análise de tendência de latência. Execuções interrompidas são recusadas para evitar conclusões com dados incompletos.

## Códigos de saída e comportamento

| Código | Significado |
| --- | --- |
| `0` | Comando concluído; em `check`, tudo passou; em `compare`, nenhuma regressão. |
| `1` | `check` teve falhas ou `compare` encontrou pelo menos uma regressão. |
| `2` | Configuração, argumento, token, formato, leitura ou escrita inválida. |
| `130` | `check` interrompido; relatório contém o estado do cancelamento. |

`history --show` consulta dados: retorna 0 mesmo que a execução salva tenha falhado. Adições, remoções e falhas que já existiam não tornam `compare` um erro. Consulte `$LASTEXITCODE` no PowerShell ou `echo $?` no bash.

Se uma saída falhar, o código 2 tem precedência. `--output` é salvo antes do histórico, e ambos antes de stdout: falhas posteriores preservam arquivos já salvos; não há transação entre as três saídas. A existência de `--output` é conferida na gravação, após as requisições.

- GET e HEAD, até 8 verificações concorrentes, ordem preservada no relatório.
- Redirecionamentos não são seguidos; configure o status 301/302 esperado ou use a URL final.
- Repetições somente para falhas de rede/timeout ou verificações reprovadas com resposta 5xx; TLS inválido não é repetido. Uma resposta aprovada, inclusive 5xx esperado, não é repetida.
- Timeout vale por tentativa; esperas de 100/200 ms entram na duração total.
- Sem `contains`, a latência mede até receber cabeçalhos; com ele, inclui a leitura do corpo, limitada a 1 MiB. O limite usa duração precisa; a exibição trunca milissegundos.
- HTTPS verifica certificados. Variáveis `HTTP_PROXY`, `HTTPS_PROXY` e `NO_PROXY` são respeitadas. Use HTTPS para enviar tokens em rede e revise configurações de terceiros antes de executá-las.

## Verificar downloads e desenvolver

Os arquivos da release acompanham `checksums.txt`. Compare o hash do arquivo baixado **antes** de extrair:

```powershell
Get-FileHash .\ronda-http_1.0.0_windows_amd64.zip -Algorithm SHA256
Get-Content .\checksums.txt
```

No Linux: `sha256sum ronda-http_1.0.0_linux_amd64.tar.gz`. No macOS: `shasum -a 256 ronda-http_1.0.0_darwin_arm64.tar.gz`. Compare com a linha correspondente de `checksums.txt`. Os binários não têm assinatura de código Apple/Microsoft.

```sh
go test ./... -count=1 -cover
go vet ./...
go build -o bin/ ./cmd/ronda
# Pacotes para as seis combinações de SO/arquitetura, em pasta vazia:
go run ./scripts/package --version 1.0.0 --output dist
```

Os testes usam servidores locais, sem depender de sites externos. A [CI](https://github.com/fbottega-dev/ronda-http/actions/workflows/ci.yml) testa Windows/Linux com Go 1.26 e 1.27, macOS com Go 1.27 e usa o detector de condições de corrida no Linux. Os seis executáveis são compilados; testes nativos cobrem os runners da matriz, não todas as arquiteturas distribuídas. `-race` local requer compilador C compatível.

Leia o [guia de estudo](docs/ESTUDO.md) e as [decisões de evolução](docs/EVOLUCAO.md). Go permite praticar rede, arquivos, cancelamento e concorrência com distribuição simples. Os pacotes separam configuração, motor HTTP, terminal, relatórios, histórico e demonstração. JSON imutável mantém o histórico pequeno e inspecionável; SQLite só faria sentido com consultas/volume mais complexos.

## Limites do projeto

Não é monitoramento contínuo: não possui agendador, notificações ou painel web. Aceita somente GET/HEAD e Bearer; não faz login, mantém cookies nem envia corpo. `contains` busca texto literal, sem interpretar JSON ou HTML. Uma verificação aprovada não comprova todo o funcionamento de uma aplicação. Histórico tem retenção manual e não é banco de métricas.

Projeto de estudo desenvolvido com apoio de IA na implementação, revisão e testes. Não representa experiência profissional anterior. O [guia](docs/ESTUDO.md) ajuda a reproduzir e entender as decisões antes de apresentar o projeto.
