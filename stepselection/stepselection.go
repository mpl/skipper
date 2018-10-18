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

type step struct {
	readFiles    map[string]bool
	writtenFiles map[string]bool
}

type DependencyGraph struct {
	steps map[string]*step
}

func NewDependencyGraph(buildReport *csv.Reader) (*DependencyGraph, error) {
	g := &DependencyGraph{
		steps: map[string]*step{},
	}
	var (
		rr  []string
		err error
	)

	for {
		rr, err = buildReport.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			// Sometimes helpful data is put in the read record
			// even though an error happens, so we show it here.
			return nil, fmt.Errorf("Could not parse record (%#v)", rr)
		}
		if len(rr) != 3 {
			return nil, fmt.Errorf("Unexpected format for record (%#v)", rr)
		}

		stepName, mode, node := StepFromSkipperArgs(rr[0]), rr[1], rr[2]
		s, ok := g.steps[stepName]
		if !ok {
			s = &step{readFiles: map[string]bool{},
				writtenFiles: map[string]bool{}}
		}
		if mode == "R" {
			s.readFiles[node] = true
		} else {
			s.writtenFiles[node] = true
		}
		// fmt.Println("step", s, stepName)
		g.steps[stepName] = s
	}
	return g, nil
}

func (g *DependencyGraph) String() string {
	for name, step := range g.steps {
		fmt.Println(name, step.readFiles, step.writtenFiles)
	}
	return fmt.Sprintf("graph with %d steps", len(g.steps))
}

// StepDependsOnFile returns true if the stepName depends on filePath, directly
// or indirectly.
func (g *DependencyGraph) StepDependsOnFile(stepName string, filePath string) (bool, error) {
	// TODO(nictuku): Make it faster. This is a naive implementation that
	// does a DFS over the dependency graph. If there is no match it will
	// end up reading the *entire* graph. The input is a single file and a
	// single step, but we'll have to repeat this for every file being
	// updated in the current change, and for every step in the build, so
	// this is super slow. The goal right now is to make it work.
	//
	// Possible way to make this fast without using a lot of memory:
	// keep maps or bloomfilters of files for each step, including all
	// transitive dependencies.

	step, ok := g.steps[stepName]
	if !ok {
		return false, fmt.Errorf("unknown step: %v", stepName)
	}
	if step.readFiles[filePath] {
		return true, nil
	}
	if step.writtenFiles[filePath] {
		return true, nil
	}
	return false, nil
}

// TODO: This is an older, simpler implementation. To be replaced with DependencyGraph and its methods.
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
		fmt.Printf("%q == %q ?\n", step, stepName)
		if step != stepName {
			continue
		}
		stepSeen = true
		for f := range updatedNodes {
			// TODO(nictuku): Use full paths for the check.
			// Requires tracking the cwd of processes in the
			// buildsnoop.
			if strings.HasPrefix(f, node) {
				fmt.Println("prefix", f, node)
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
