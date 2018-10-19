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
	readFiles map[string]bool
}

type DependencyGraph struct {
	steps       map[string]*step
	fileWriters map[string][]*step
}

func NewDependencyGraph(buildReport *csv.Reader) (*DependencyGraph, error) {
	g := &DependencyGraph{
		steps:       map[string]*step{},
		fileWriters: map[string][]*step{},
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
			s = &step{readFiles: map[string]bool{}}
		}
		if mode == "R" {
			s.readFiles[node] = true
		} else {
			g.fileWriters[node] = append(g.fileWriters[node], s)
		}
		// fmt.Println("step", s, stepName)
		g.steps[stepName] = s
	}
	return g, nil
}

func (g *DependencyGraph) String() string {
	return fmt.Sprintf("graph with %d steps", len(g.steps))
}

func (g *DependencyGraph) fileDeps(filePath string) []string {
	var files []string
	for _, step := range g.fileWriters[filePath] {
		for file := range step.readFiles {
			files = append(files, file)
			files = append(files, g.fileDeps(file)...)
		}
	}
	return files
}

// StepDependsOnFile returns true if the stepName depends on filePath, directly
// or indirectly.
func (g *DependencyGraph) StepDependsOnFile(stepName string, filePath string) (bool, error) {
	// Assume the following build log in format "StepName, FilePath, Mode":
	// step1,F1,R
	// step1,F2,W
	// step2,F2,R
	// step2,F3,W
	// step3,F3,R
	//
	// We want to be able to say "true" when asked if step3 depends
	// (transitively) on F1.
	//
	// This implementation assumes there are no loops.
	//
	// A step depends on a file F if the step has read from F or if it
	// depends on another step who read from F.
	// A step A depends on step B if the files that A reads have been
	// written to by B or have been written by a step that B depends upon.
	//
	// We just need to store:
	// - what files each step has read
	// - what steps have written to a given file
	//
	// We are using maps, but could switch to more efficient structures
	// if/when the number of files we track becomes too large.

	step, ok := g.steps[stepName]
	if !ok {
		return false, fmt.Errorf("unknown step: %v", stepName)
	}
	for f := range step.readFiles {
		if f == filePath {
			return true, nil
		}
		for _, f2 := range g.fileDeps(f) {
			if f2 == filePath {
				return true, nil
			}
		}
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
