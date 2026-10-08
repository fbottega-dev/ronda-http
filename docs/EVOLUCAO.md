# Evolução até a versão 1.0

O Ronda HTTP começou como uma base de verificações pelo terminal e evoluiu com incrementos funcionais. A versão 1.0 encerra o escopo deste projeto de portfólio; novas ideias não são requisitos pendentes desta entrega.

## Incrementos

- Base: configuração JSON, HTTP GET/HEAD, status/conteúdo/latência, concorrência limitada, cancelamento, tokens de ambiente, demonstração local e relatórios texto/JSON.
- 02/10/2026: `check --only NOME`, preservando a ordem e exigindo tokens apenas dos destinos selecionados.
- 05/10/2026: `validate --config`, sem rede nem resolução de tokens.
- 08/10/2026: conclusão do escopo 1.0, reunindo os recursos abaixo e distribuição portátil. A conclusão integral foi solicitada pelo usuário após a fase de incrementos separados.

| Recurso entregue | Como verificar |
| --- | --- |
| `list --config` | Lista nomes, métodos e grupos na ordem; testes contam zero requisições e não exigem tokens. |
| `groups` e `check --group` | Seleção exata, incompatível com `--only`; grupos vazios/inexistentes falham antes da rede. Configuração antiga continua válida. |
| Histórico local | `check --history-dir`, `history` e `history --show`; snapshots imutáveis com validação, concorrência e preservação de arquivos. |
| Comparação | `compare --before --after`; regressão, recuperação, adição, remoção e resultado inalterado. IDs opcionais estabilizam a identidade após renomear destinos. |
| JUnit | XML em stdout com escaping, contagens, falhas e cancelamento. `--output` continua sendo JSON. |
| Distribuição | Windows/Linux/macOS, amd64/arm64, arquivos compactados e SHA-256; empacotamento depende da matriz de testes da CI. |

## Decisões de escopo

**Persistência:** um JSON por execução mantém o formato inspecionável e evita dependências externas. A ferramenta é voltada a execuções pontuais, com até 100 destinos. SQLite passa a fazer sentido se consultas e volume justificarem a complexidade. A retenção é manual, sem exclusões silenciosas.

**Formato:** configuração `version: 1` e relatório `schema_version: 1`. Os novos campos são opcionais. Relatórios sem ID continuam legíveis; formatos incompatíveis são rejeitados. Esta é a primeira versão do histórico, portanto ainda não há migração de versões antigas de armazenamento.

**Identidade:** a comparação usa ID quando presente e nome exato quando ausente. Alterar a identidade aparece como remoção/adição. Ela compara aprovação final, não tendência estatística de latência. Execuções canceladas são recusadas.

**Arquivos:** temporários completos e sincronizados são publicados por hard link, sem sobrescrever outro snapshot. O histórico precisa de um filesystem com suporte a hard links. Saídas em arquivo, histórico e stdout não formam uma transação; arquivos concluídos sobrevivem a erros posteriores.

**Distribuição:** compilações cruzadas cobrem seis plataformas; testes nativos cobrem Windows/Linux/macOS nos runners da CI. Não se afirma execução nativa em todas as arquiteturas. Binários não têm assinatura de código Apple/Microsoft.

## Manutenção futura

Agendador, painel web, notificações e armazenamento de métricas ficam fora do escopo. Só devem entrar diante de um caso de uso concreto, sem manter artificialmente este projeto incompleto.

Possíveis ampliações, em ordem de prioridade:

1. Exportar uma tabela CSV do histórico, para analisar resultados em uma planilha sem programar consultas.
2. Oferecer limpeza por idade com prévia e confirmação explícita, para facilitar a retenção de históricos maiores.
3. Validar um campo JSON específico da resposta, quando a busca literal de `contains` não for suficiente.

Para qualquer mudança futura: reproduza o problema, preserve os fluxos atuais, acrescente testes úteis, execute gofmt/test/vet/build, atualize a documentação e acompanhe a CI do commit publicado. Use commits reais e datas reais.
