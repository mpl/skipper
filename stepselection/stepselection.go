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

func cleanStepName(s string) string {
	return re.ReplaceAllString(s, "")
}

func ShouldRunStep(buildReport *csv.Reader, updatedNodes map[string]bool, stepName string) (bool, error) {
	var (
		rr  []string
		err error
	)
	// TODO speed: This is n^2 currently, but it could be much faster.
	for {
		rr, err = buildReport.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return true, err
		}
		if len(rr) != 3 {
			fmt.Fprintf(os.Stderr, "Unexpected format for csv record: %#v", rr)
			continue
		}

		step, _, node := cleanStepName(rr[0]), rr[1], rr[2]
		if step != stepName {
			continue
		}
		for f := range updatedNodes {
			if strings.HasPrefix(f, node) {
				return true, nil
			}
		}
	}

	return false, nil
}
