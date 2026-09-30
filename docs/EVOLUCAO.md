# Projeto em evolução

A entrega atual é a **etapa 1**, uma base executável. O projeto mais amplo está incompleto: as etapas abaixo ainda não foram implementadas. Não existem botões ou opções anunciando recursos que o programa não executa.

Quando for solicitado “mais commits”, continue neste repositório. Primeiro confira `git status`, histórico e testes. Escolha um incremento pequeno, implemente, valide e documente. Não complete todo o roteiro de uma vez; a intenção é acompanhar o aprendizado e a evolução entre conversas.

| Prioridade | Etapa pendente | Benefício e critério de conclusão |
| --- | --- | --- |
| 1 | Selecionar destinos com `--only` e depois grupos na configuração | Verificar somente uma parte do ambiente. Nomes/grupos inexistentes precisam produzir erro claro; testes devem provar que destinos excluídos não recebem requisições. |
| 2 | Histórico local de execuções | Consultar resultados anteriores sem organizar arquivos manualmente. Definir retenção, formato e migração; persistir apenas os campos seguros do relatório. Avaliar SQLite quando a consulta justificar uma dependência. |
| 3 | Comparação entre execuções | Identificar o que começou a falhar ou se recuperou. Comparar por identificador estável, tratando destinos adicionados/removidos e formatos incompatíveis. |
| 4 | Relatório JUnit | Anexar as verificações a pipelines que exibem resultados de testes. Validar XML, escaping e falhas sem expor tokens ou conteúdo. |
| 5 | Releases com binários e checksums | Executar sem instalar Go. Publicar artefatos de Windows/Linux/macOS após testes e documentar a verificação do download. |

Agendamento, painel web e notificações não têm implementação prevista nesta sequência. Só devem entrar quando houver um caso de uso concreto.

## Antes de cada commit

1. Reproduza o comportamento atual. Se for correção, confirme o defeito antes de alterar.
2. Preserve os fluxos aprovados e adicione um teste significativo para a regra nova.
3. Execute `go test ./...`, `go vet ./...` e o fluxo pelo terminal quando afetado.
4. Atualize os exemplos e a documentação do comportamento alterado.
5. Faça um commit coerente, com data real, e acompanhe a verificação remota.

Não há necessidade de inventar bugs ou deixar testes falhando para ter o que fazer depois. As etapas pendentes representam trabalho funcional real.
