package report

import (
	"bytes"
	"encoding/xml"
	"errors"
	"strings"
	"testing"

	"github.com/fbottega-dev/ronda-http/internal/checker"
)

func TestJUnitEscapesCountsTimesAndCancellation(t *testing.T) {
	good := sampleResult(`API <a> & "b"`, true)
	good.ID = "healthy"
	good.Attempts = append([]checker.Attempt{{Status: 503, DurationMS: 75, Code: "status", Message: "Tente novamente."}}, good.Attempts...)
	bad := sampleResult("Falhou", false)
	bad.Attempts[0].Message = `Texto "a" & <b> ausente.`
	canceled := sampleResult("Cancelado", false)
	canceled.Attempts[0] = checker.Attempt{Code: "canceled", Message: "Verificação cancelada."}
	input := sampleReport(good, bad, canceled)
	input.DurationMS = 500
	var buffer bytes.Buffer
	if err := JUnit(&buffer, input); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buffer.String(), xml.Header) || !strings.HasSuffix(buffer.String(), "\n") {
		t.Fatalf("cabeçalho ou newline ausente: %s", buffer.String())
	}
	var suite junitSuite
	if err := xml.Unmarshal(buffer.Bytes(), &suite); err != nil {
		t.Fatalf("XML inválido: %v", err)
	}
	if suite.Tests != 3 || suite.Failures != 1 || suite.Skipped != 1 || suite.Errors != 0 || suite.Time != "0.500" || suite.Timestamp != "2026-10-06T20:00:00Z" {
		t.Fatalf("suite incorreta: %+v", suite)
	}
	if len(suite.Cases) != 3 || suite.Cases[0].Name != good.Name || suite.Cases[0].Time != "0.125" || suite.Cases[0].Classname != "ronda-http.healthy" || suite.Cases[0].Failure != nil || suite.Cases[0].Skipped != nil {
		t.Fatalf("caso aprovado incorreto: %+v", suite.Cases)
	}
	if suite.Cases[1].Failure == nil || suite.Cases[1].Failure.Type != "status" || suite.Cases[1].Failure.Message != bad.Attempts[0].Message || suite.Cases[1].Skipped != nil {
		t.Fatalf("falha incorreta: %+v", suite.Cases[1])
	}
	if suite.Cases[2].Skipped == nil || suite.Cases[2].Failure != nil || suite.Cases[2].Time != "0.000" {
		t.Fatalf("cancelamento incorreto: %+v", suite.Cases[2])
	}
	if suite.Properties == nil || len(suite.Properties.Properties) != 1 || suite.Properties.Properties[0].Name != "ronda.canceled" || suite.Properties.Properties[0].Value != "true" {
		t.Fatalf("metadado de cancelamento ausente: %+v", suite.Properties)
	}
	if strings.Contains(buffer.String(), `<a>`) || strings.Contains(buffer.String(), `<b>`) {
		t.Fatal("texto tratado como XML")
	}
}

func TestJUnitLateCancellationAndLargeDuration(t *testing.T) {
	input := sampleReport()
	input.Canceled = true
	input.DurationMS = 9223372036854775807
	input.Results[0].Attempts = append(input.Results[0].Attempts, input.Results[0].Attempts[0])
	for index := range input.Results[0].Attempts {
		input.Results[0].Attempts[index].DurationMS = input.DurationMS
	}
	var buffer bytes.Buffer
	if err := JUnit(&buffer, input); err != nil {
		t.Fatal(err)
	}
	var suite junitSuite
	if err := xml.Unmarshal(buffer.Bytes(), &suite); err != nil || suite.Failures != 0 || suite.Skipped != 0 || suite.Properties == nil || strings.HasPrefix(suite.Cases[0].Time, "-") {
		t.Fatalf("cancelamento tardio/tempo longo incorreto: %+v; %v", suite, err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("caminho-secreto de escrita")
}

func TestJUnitRejectsInvalidReportBeforeWriting(t *testing.T) {
	input := sampleReport()
	input.Passed = 999
	var buffer bytes.Buffer
	if err := JUnit(&buffer, input); err == nil || buffer.Len() != 0 {
		t.Fatalf("relatório inválido parcialmente escrito: %v", err)
	}
	for _, writer := range []interface{ Write([]byte) (int, error) }{nil, failingWriter{}} {
		err := JUnit(writer, sampleReport())
		if err == nil || strings.Contains(err.Error(), "secreto") {
			t.Fatalf("erro de escrita exposto/ignorado: %v", err)
		}
	}
}

func TestJUnitSuccessfulRunHasNoFailureOrSkipped(t *testing.T) {
	var buffer bytes.Buffer
	if err := JUnit(&buffer, sampleReport()); err != nil {
		t.Fatal(err)
	}
	var suite junitSuite
	if err := xml.Unmarshal(buffer.Bytes(), &suite); err != nil || suite.Tests != 1 || suite.Failures != 0 || suite.Skipped != 0 || suite.Properties != nil {
		t.Fatalf("suite aprovada incorreta: %+v; %v", suite, err)
	}
}

func TestJUnitAcceptsObservedInformationalAndNonstandardStatuses(t *testing.T) {
	for _, status := range []int{101, 599, 999} {
		result := sampleResult("Status inesperado", false)
		result.Attempts[0].Status = status
		input, err := Decode(strings.NewReader(encodeReport(t, sampleReport(result))))
		if err != nil {
			t.Fatalf("status recebido %d rejeitado: %v", status, err)
		}
		var buffer bytes.Buffer
		if err := JUnit(&buffer, input); err != nil {
			t.Fatalf("status %d rejeitado no XML: %v", status, err)
		}
		var suite junitSuite
		if err := xml.Unmarshal(buffer.Bytes(), &suite); err != nil || suite.Failures != 1 || suite.Cases[0].Failure.Type != "status" {
			t.Fatalf("falha de status %d não preservada: %+v; %v", status, suite, err)
		}
	}
}

func TestJUnitReplacesRunesDisallowedByXML(t *testing.T) {
	result := sampleResult("Nome \ufffe \uffff", false)
	result.Attempts[0].Message = "Mensagem \ufffe \uffff"
	var buffer bytes.Buffer
	if err := JUnit(&buffer, sampleReport(result)); err != nil {
		t.Fatal(err)
	}
	var suite junitSuite
	if err := xml.Unmarshal(buffer.Bytes(), &suite); err != nil {
		t.Fatalf("caracteres Unicode geraram XML inválido: %v", err)
	}
	if suite.Cases[0].Name != "Nome \ufffd \ufffd" || suite.Cases[0].Failure.Message != "Mensagem \ufffd \ufffd" {
		t.Fatalf("caracteres não suportados não foram substituídos: %+v", suite.Cases[0])
	}
}
