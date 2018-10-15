// Package stepselection decides which build steps should be run for a build.
package stepselection

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var re = regexp.MustCompile("^skipper (?:--id [^ ]+ )?-- ")

func StepFromSkipperArgs(s string) string {
	return re.ReplaceAllString(s, "")
}

func ShouldRunStep(buildReport *csv.Reader, updatedNodes map[string]bool, stepName string) (bool, error) {
	var (
		rr  []string
		err error
	)
	if len(updatedNodes) == 0 {
		fmt.Fprintf(os.Stderr, "skipper got an empty change set\n")
		return true, nil
	}
	// TODO speed: This is n^2 currently, but it could be much faster.

	stepSeen := false
	stepName = StepFromSkipperArgs(stepName)

	for {
		rr, err = buildReport.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			// Sometimes helpful data is put in the read record
			// even though an error happens, so we print it here.
			fmt.Fprintf(os.Stderr, "Could not parse record (%#v). Falling back to running steps\n", rr)
			return true, err
		}
		if len(rr) != 3 {
			fmt.Fprintf(os.Stderr, "Unexpected format for record (%#v). Falling back to running steps\n", rr)
			return true, nil
		}

		step, _, node := StepFromSkipperArgs(rr[0]), rr[1], rr[2]
		if step != stepName {
			continue
		}
		stepSeen = true
		for f := range updatedNodes {
			if strings.HasPrefix(f, node) {
				return true, nil
			}
		}
	}
	if !stepSeen {
		// If there are no nodes in the build graph that match with the
		// step being checked, then it's a new step it should be run.
		fmt.Fprintf(os.Stderr, "Step %q appears to be new. Falling back to running it\n", stepName)
		return true, nil
	}

	return false, nil
}
