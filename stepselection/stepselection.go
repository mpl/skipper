// Package stepselection decides which build steps should be run for a build.
package stepselection

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
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

type CmdTree []string

func (c CmdTree) Name() string {
	b, err := json.Marshal(c)
	if err != nil {
		return "?"
	}
	return string(b)
}

type BuildLog struct {
	CmdTree []string
	Mode    string
	File    string
}

// walkUpStepTree runs f on each step of a step tree, identified in the build
// report as "p1,p2,p3" etc. The name(s) of a step's ancestors are also part of
// its name, to make it unique. So the name of the first step is `p1` and the
// name of the last step is `p1,p2,p3`.
func walkUpStepTree(step CmdTree, f func(subTree CmdTree)) {
	for i := range step {
		subTree := step[0 : i+1]
		f(subTree)
	}
}

// NewDependencyGraph creates a DependencyGraph which can be used for looking
// up whether a step depends on certain files. A buildReport must be provided,
// which is currently obtained by running `stepanalysis` on a build log. The
// buid log is the output of buildsnoop.py.
func NewDependencyGraph(buildReport io.Reader) (*DependencyGraph, error) {
	g := &DependencyGraph{
		steps:       map[string]*step{},
		fileWriters: map[string][]*step{},
	}
	start := time.Now()
	scanner := bufio.NewScanner(buildReport)
	for scanner.Scan() {
		bog := &BuildLog{}
		if err := json.Unmarshal(scanner.Bytes(), bog); err != nil {
			return nil, err
		}
		mode := bog.Mode
		node := bog.File
		steps := bog.CmdTree
		walkUpStepTree(steps, func(cmdTree CmdTree) {
			// We add this node to all ancestor steps to
			// effectively make them depend on these files, too.
			s, ok := g.steps[cmdTree.Name()]
			if !ok {
				s = &step{readFiles: map[string]bool{}, name: cmdTree.Name()}
			}
			if mode == "R" {
				s.readFiles[node] = true
			} else {
				g.fileWriters[node] = append(g.fileWriters[node], s)
			}
			g.steps[cmdTree.Name()] = s
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
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

// StepDependsOnFile returns true if the cmdTree depends on changedFiles,
// directly or indirectly. It also indicates _why_ it decided that way. The
// returned string is for the end user's benefit. It may change at any point
// and should not be used programmatically.
func (g *DependencyGraph) StepDependsOnFiles(cmdTree CmdTree, changedFiles []string) (bool, string, error) {
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

	step, ok := g.steps[cmdTree.Name()]
	if !ok {
		return false, "", fmt.Errorf("unknown step: %v", cmdTree)
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
