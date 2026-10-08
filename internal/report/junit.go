package report

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strconv"

	"github.com/fbottega-dev/ronda-http/internal/checker"
)

type junitSuite struct {
	XMLName    xml.Name         `xml:"testsuite"`
	Name       string           `xml:"name,attr"`
	Tests      int              `xml:"tests,attr"`
	Failures   int              `xml:"failures,attr"`
	Errors     int              `xml:"errors,attr"`
	Skipped    int              `xml:"skipped,attr"`
	Time       string           `xml:"time,attr"`
	Timestamp  string           `xml:"timestamp,attr"`
	Properties *junitProperties `xml:"properties,omitempty"`
	Cases      []junitCase      `xml:"testcase"`
}

type junitProperties struct {
	Properties []junitProperty `xml:"property"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
}

type junitFailure struct {
	Type    string `xml:"type,attr"`
	Message string `xml:"message,attr"`
}

type junitSkipped struct {
	Message string `xml:"message,attr"`
}

// JUnit writes one UTF-8 JUnit testsuite using only report fields. Canceled
// targets are skipped; other unsuccessful targets are failures. Time is seconds,
// with per-target time equal to the sum of attempts and suite time equal to the
// elapsed run duration (concurrent requests therefore need not add up to it).
func JUnit(writer io.Writer, input checker.Report) error {
	if err := Validate(input); err != nil {
		return err
	}
	if writer == nil {
		return errors.New("não foi possível escrever o relatório JUnit")
	}
	suite := junitSuite{
		Name: "ronda-http", Tests: len(input.Results), Time: seconds(float64(input.DurationMS)),
		Timestamp: input.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Cases:     make([]junitCase, 0, len(input.Results)),
	}
	if input.Canceled {
		suite.Properties = &junitProperties{Properties: []junitProperty{{Name: "ronda.canceled", Value: "true"}}}
	}
	for _, result := range input.Results {
		var durationMS float64
		for _, attempt := range result.Attempts {
			durationMS += float64(attempt.DurationMS)
		}
		test := junitCase{Name: result.Name, Classname: "ronda-http", Time: seconds(durationMS)}
		if result.ID != "" {
			test.Classname += "." + result.ID
		}
		last := result.Attempts[len(result.Attempts)-1]
		switch {
		case last.Code == "canceled":
			test.Skipped = &junitSkipped{Message: last.Message}
			suite.Skipped++
		case !result.Passed:
			test.Failure = &junitFailure{Type: last.Code, Message: last.Message}
			suite.Failures++
		}
		suite.Cases = append(suite.Cases, test)
	}
	// Build the document before writing so validation and encoding failures cannot
	// leave the caller with a partial XML document.
	var buffer bytes.Buffer
	buffer.WriteString(xml.Header)
	encoder := xml.NewEncoder(&buffer)
	encoder.Indent("", "  ")
	if err := encoder.Encode(suite); err != nil {
		return errors.New("não foi possível gerar o relatório JUnit")
	}
	buffer.WriteByte('\n')
	if _, err := io.Copy(writer, &buffer); err != nil {
		return errors.New("não foi possível escrever o relatório JUnit")
	}
	return nil
}

func seconds(milliseconds float64) string {
	return strconv.FormatFloat(milliseconds/1000, 'f', 3, 64)
}
