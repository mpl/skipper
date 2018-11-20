// Package stepselection decides which build steps should be run for a build.
package stepselection

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
)

// TODO(nictuku): make this a flag?
const debug = false

var re = regexp.MustCompile("^skipper (?:--id [^ ]+ )?-- ")

func StepFromSkipperArgs(s string) string {
	return re.ReplaceAllString(s, "")
}

type step struct {
	name      string // for debugging
	readFiles map[string]bool
}

var ignoreFiles = map[string]bool{
	"/dev/null": true,
}

type DependencyGraph struct {
	steps       map[string]*step
	fileWriters map[string][]*step
}

func absoluteNodePath(node string) string {
	// TODO(nictuku): Remove this when the build log is fixed to only provide full paths.
	// This is not always correct because it relies on the current skipper working
	// directory to be the same as when the build log was created.
	if path.IsAbs(node) {
		return node
	}
	cwd, err := os.Getwd()
	if err != nil {
		return node
	}
	return path.Join(cwd, node)
}

func allSteps(steps string) ([]string, error) {
	r := csv.NewReader(strings.NewReader(steps))
	record, err := r.Read()
	if err != nil {
		return nil, err
	}
	return record, nil
}

// walkUpStepTree runs f on each step of a step tree, identified in the build
// report as "p1,p2,p3" etc. The name(s) of a step's ancestors are also part of
// its name, to make it unique. So the name of the first step is `p1` and the
// name of the last step is `p1,p2,p3`.
func walkUpStepTree(step []string, f func(step string)) {
	for i := range step {
		// Proper csv encoding to escape commas and stuff inside step names.
		b := new(bytes.Buffer)
		w := csv.NewWriter(b)
		//stepName := strings.Join(step[0:i+1], ",")
		if err := w.Write(step[0 : i+1]); err != nil {
			panic("walkUpStepTree very unexpected csv writing error: " + err.Error())
		}
		w.Flush()
		f(strings.TrimSpace(b.String()))
	}
}

// NewDependencyGraph creates a DependencyGraph which can be used for looking
// up whether a step depends on certain files. A buildReport must be provided,
// which is currently obtained by running `stepanalysis` on a build log. The
// buid log is the output of buildsnoop.py.
func NewDependencyGraph(buildReport *csv.Reader) (*DependencyGraph, error) {
	g := &DependencyGraph{
		steps:       map[string]*step{},
		fileWriters: map[string][]*step{},
	}
	var (
		rr  []string
		err error
	)
	start := time.Now()
	for {
		rr, err = buildReport.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			// Sometimes helpful data is put in the read record
			// even though an error happens, so we show it here.
			return nil, fmt.Errorf("could not parse record (%#v)", rr)
		}
		if len(rr) != 3 {
			return nil, fmt.Errorf("unexpected format for record (%#v)", rr)
		}

		stepEncoded, mode, node := StepFromSkipperArgs(rr[0]), rr[1], absoluteNodePath(rr[2])

		steps, err := allSteps(stepEncoded)
		if err != nil {
			return nil, fmt.Errorf("could not decode step name %q: %v", stepEncoded, err)
		}
		walkUpStepTree(steps, func(stepName string) {
			// We add this node to all ancestor steps to
			// effectively make them depend on these files, too.
			s, ok := g.steps[stepName]
			if !ok {
				s = &step{readFiles: map[string]bool{}, name: stepName}
			}
			if mode == "R" {
				s.readFiles[node] = true
			} else {
				g.fileWriters[node] = append(g.fileWriters[node], s)
			}
			g.steps[stepName] = s
		})
	}
	fmt.Println("dep graph build time:", time.Since(start))
	return g, nil
}

func (g *DependencyGraph) String() string {
	return fmt.Sprintf("graph with %d steps", len(g.steps))
}

type lookupState struct {
	stepChecked map[string]bool
}

func (g *DependencyGraph) fileDeps(s *lookupState, filePath string) []string {
	if _, ok := ignoreFiles[filePath]; ok {
		return nil
	}
	var files []string
	if debug {
		fmt.Printf("\tfileDeps(%v)\n", filePath)
	}
	for _, step := range g.fileWriters[filePath] {
		if debug {
			fmt.Printf("\t\tdepends on step %q (step writes to %v)\n", step.name, filePath)
		}
		// Note that the original filePath is irrelevant from here on,
		// so we can cache the step dependencies as a whole,
		// independently of which file is being checked.
		if s.stepChecked[step.name] {
			// This step and its dependencies have been checked.
			// Either they don't have any relevant file reads, or
			// it's already marked to be checked.
			// fmt.Printf("\t\t\tstep %q, skipped\n", step.name)
			continue
		}
		s.stepChecked[step.name] = true
		for file := range step.readFiles {
			if file == filePath {
				continue
			}
			if debug {
				fmt.Printf("\t\t\tstep %q, readFiles %v\n", step.name, file)
			}
			files = append(files, file)
			files = append(files, g.fileDeps(s, file)...)
		}
	}
	return files
}

// StepDependsOnFile returns true if the stepName depends on changedFiles,
// directly or indirectly. It also indicates _why_ it decided that way. The
// returned string is for the end user's benefit. It may change at any point
// and should not be used programmatically.
func (g *DependencyGraph) StepDependsOnFiles(stepName string, changedFiles []string) (bool, string, error) {
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

	// TODO(nictuku): We should require all inputs to be absolute because
	// relative paths obviously change when the cwd changes, and that's unreliable.
	for i, f := range changedFiles {
		changedFiles[i] = absoluteNodePath(f)
	}

	step, ok := g.steps[stepName]
	if !ok {
		return false, "", fmt.Errorf("unknown step: %v", stepName)
	}
	if debug {
		fmt.Printf("=> step %q\n", step.name)
	}
	s := &lookupState{stepChecked: map[string]bool{}}
	for stepReadFile := range step.readFiles {
		if debug {
			fmt.Printf("\tstep %q -> %v\n", step.name, stepReadFile)
		}
		for _, changedFile := range changedFiles {
			if changedFile == stepReadFile {
				return true, fmt.Sprintf("step %q reads file %q which is being updated", step.name, stepReadFile), nil
			}
		}
		for _, transitiveDep := range g.fileDeps(s, stepReadFile) {
			for _, changedFile := range changedFiles {
				if transitiveDep == changedFile {
					return true, fmt.Sprintf("step %q has a dependency that uses %q", step.name, changedFile), nil
				}
			}
		}
	}
	return false, "", nil
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
		if step != stepName {
			continue
		}
		stepSeen = true
		for f := range updatedNodes {
			// TODO(nictuku): Use full paths for the check.
			// Requires tracking the cwd of processes in the
			// buildsnoop.
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
