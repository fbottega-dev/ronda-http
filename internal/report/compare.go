package report

import (
	"errors"

	"github.com/fbottega-dev/ronda-http/internal/checker"
)

// Comparison describes outcome changes, retaining the after-report order and
// appending removed targets in their before-report order.
type Comparison struct {
	Changes   []Change `json:"changes"`
	Added     int      `json:"added"`
	Removed   int      `json:"removed"`
	Regressed int      `json:"regressed"`
	Recovered int      `json:"recovered"`
	Unchanged int      `json:"unchanged"`
}

// Change matches an explicit ID when supplied, otherwise the exact target name.
// Nil outcome pointers mean the target was absent from that report, not failed.
type Change struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	BeforePassed *bool  `json:"before_passed"`
	AfterPassed  *bool  `json:"after_passed"`
}

// Compare refuses partial, canceled runs. It compares the final pass/fail
// outcome, not timing or individual retry attempts. Adding an ID to a previously
// unnamed identity, or renaming a legacy target without ID, is an addition and
// removal rather than an inferred match.
func Compare(before, after checker.Report) (Comparison, error) {
	if err := Validate(before); err != nil {
		return Comparison{}, err
	}
	if err := Validate(after); err != nil {
		return Comparison{}, err
	}
	if before.Canceled || after.Canceled {
		return Comparison{}, errors.New("não é possível comparar relatórios cancelados ou parciais")
	}
	previous := make(map[string]checker.Result, len(before.Results))
	for _, result := range before.Results {
		previous[resultKey(result)] = result
	}
	comparison := Comparison{Changes: make([]Change, 0, len(before.Results)+len(after.Results))}
	for _, result := range after.Results {
		key := resultKey(result)
		change := Change{Key: key, Name: result.Name, AfterPassed: boolPointer(result.Passed)}
		prior, present := previous[key]
		switch {
		case !present:
			change.Status = "added"
			comparison.Added++
		case prior.Passed && !result.Passed:
			change.Status = "regressed"
			comparison.Regressed++
		case !prior.Passed && result.Passed:
			change.Status = "recovered"
			comparison.Recovered++
		default:
			change.Status = "unchanged"
			comparison.Unchanged++
		}
		if present {
			change.BeforePassed = boolPointer(prior.Passed)
			delete(previous, key)
		}
		comparison.Changes = append(comparison.Changes, change)
	}
	for _, result := range before.Results {
		key := resultKey(result)
		if _, present := previous[key]; present {
			comparison.Changes = append(comparison.Changes, Change{
				Key: key, Name: result.Name, Status: "removed", BeforePassed: boolPointer(result.Passed),
			})
			comparison.Removed++
		}
	}
	return comparison, nil
}

func boolPointer(value bool) *bool {
	return &value
}
