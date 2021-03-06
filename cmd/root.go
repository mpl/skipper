package cmd

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"time"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/oklog/ulid"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/yourbase/skipper/builddata"
	"github.com/yourbase/skipper/stepselection"
)

var (
	cfgFileFlag     string
	buildIDFlag     string
	graphFileFlag   string
	changesFileFlag string
)

// Skipper needs to be run with a --id <buildId>. If that flag wasn't set, we spawn a child skipper process with that flag.

func childSkipperArgs(buildID string, args []string) []string {
	i := 1
	args = append(args[:i], append([]string{"--id", buildID}, args[i:]...)...)
	return args
}

// buildULID is *not* safe for concurrent use because of math/rand.
func newBuildULID() (string, error) {

	t := time.Now()
	entropy := ulid.Monotonic(rand.New(rand.NewSource(t.UnixNano())), 0)
	id, err := ulid.New(ulid.Timestamp(t), entropy)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// TODO(nictuku): I don't recall why this is ~/yourbase.txt and not
// /yourbase.txt like the others. Move it?
// I don't love the .txt extension but I didn't want to use /yourbase because
// that would prevent us from having a directory with that name.
// Also this should probably be set in the flag definition and not here.
const buildIDFilePath = "~/yourbase.txt"

// buildULIDFromFile looks for a /yourbase file and check if it contains an
// ULID. If it contains anything but an ULID, it's an error. If the file
// doesn't exist or it's empty, returns an empty ULID and nil error.
func buildULIDFromFile() (string, error) {
	f, err := homedir.Expand(buildIDFilePath)
	if err != nil {
		return "", err
	}
	content, err := ioutil.ReadFile(f)
	if os.IsNotExist(err) || len(content) == 0 {
		return "", nil
	}
	// TrimSpace to avoid confusion during manual testing.
	return strings.TrimSpace(string(content)), nil
}

func saveBuildULID(id string) error {
	fp, err := homedir.Expand(buildIDFilePath)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(fp, os.O_TRUNC|os.O_RDWR|os.O_CREATE, 0700)
	if err != nil {
		return err
	}
	defer f.Close()
	// No trailing newline, makes things simpler for programs.
	_, err = io.WriteString(f, id)
	return err
}

func currentStepName(args []string) ([]string, error) {
		// XXX missing parents
		stepName := strings.Join(args, " ")
		return []string{stepName}, nil
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "skipper",
	Short: "A program that can skip unnecessary build steps",
	Long:  `A program that looks at a project's build graph and skips unnecessary build steps.`,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			return
		}

		parentSkipper := false
		// If we have trouble fork-bombing ourselves, we can add a
		// check to look at the parent process of the current process
		// and refusing to call skipper again if the parent process is
		// already a skipper. I don't expect that to happen unless we
		// mess-up on the flag-passing + flag-parsing logic.
		if buildIDFlag == "" {
			parentSkipper = true
			// TODO: Inspect /yourbase file first. Also update it otherwise.
			id, err := buildULIDFromFile()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Unexpected error reading from %v: %v\n", buildIDFilePath, err)
				os.Exit(1)
			}
			if id == "" {
				id, err = newBuildULID()
				if err != nil {
					fmt.Fprintf(os.Stderr, "Could not create a new build ID: %v\n", err)
					os.Exit(1)
				}
				if err = saveBuildULID(id); err != nil {
					fmt.Fprintf(os.Stderr, "Could not save build ULID to %v: %v\n", buildIDFilePath, err)
					os.Exit(1)
				}
			}
			// TODO: Write to buildULIDFilePath.

			// os.Args, not args because args is incomplete for us.
			args = childSkipperArgs(id, os.Args)
		}
		run := func() {
			cm := exec.Command(args[0], args[1:]...)
			cm.Stdout = os.Stdout
			cm.Stderr = os.Stderr
			if err := cm.Run(); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
		}
		if parentSkipper {
			run()
			return
		}
		stepName, err := currentStepName(args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipper: decided to run because could not determine current step name: %v\n", err)
			run()
			return
		}
		// TODO(nictuku): is there a better moment to create this?
		// Perhaps if the skipper becomes noticeably slow, we can move
		// steps like this to asynchronous ones.
		skipCheck, err := newStepSkipper(graphFileFlag, changesFileFlag)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Printf("skipper: defaulting to running command %q because the base dependency graph is missing\n", args)
			} else {
				fmt.Fprintf(os.Stderr, "skipper: defaulting to running command %q because could not open base dependency graph: %v\n", args, err)
			}
			run()
			return
		}
		shouldRun, err := skipCheck.shouldRun(stepName)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			fmt.Printf("skipper: could not decide if we should run %q. Falling back to running\n", stepName)
			run()
			return
		}
		if shouldRun {
			fmt.Printf("skipper: decided that we should run: %q\n", stepName)
			run()
			return
		}
		fmt.Printf("skipper: decided we should skip: %q\n", stepName)
		return
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFileFlag, "config", "", "config file (default is $HOME/.skipper.yaml)")
	rootCmd.PersistentFlags().StringVar(&buildIDFlag, "id", "", "ID for this build. If empty, it looks for a /yourbase file with a build ID otherwise it creates one with a random build ID. Once a build ID is determined, skipper spawns a child process of itself but passing --id <id> accordingly")
	rootCmd.PersistentFlags().StringVar(&graphFileFlag, "dep-graph", "/base-graph.gz", "build graph from the base build")
	rootCmd.PersistentFlags().StringVar(&changesFileFlag, "changes", "/changes", "changes to the current repo compared to the base build")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFileFlag != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFileFlag)
	} else {
		// Find home directory.
		home, err := homedir.Dir()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		// Search config in home directory with name ".skipper" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigName(".skipper")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
}

func updatedNodes(filePath string) (map[string]bool, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}

	m := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		m[scanner.Text()] = true
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return m, nil
}

// TODO(nictuku): Move this to stepselection.

type stepSkipper struct {
	buildReport  io.ReadCloser
	updatedNodes map[string]bool
	depGraph     *stepselection.DependencyGraph
}

func (s *stepSkipper) Close() {
	s.buildReport.Close()
}

func newStepSkipper(logFile string, upFile string) (*stepSkipper, error) {
	buildReport, err := builddata.OpenFile(logFile)
	if err != nil {
		return nil, err
	}
	updatedNodes, err := updatedNodes(upFile)
	if err != nil {
		return nil, err
	}
	depGraph, err := stepselection.NewDependencyGraph(buildReport)
	if err != nil {
		return nil, err
	}
	return &stepSkipper{
		buildReport:  buildReport,
		updatedNodes: updatedNodes,
		depGraph:     depGraph,
	}, nil
}

func (s *stepSkipper) shouldRun(stepName []string) (bool, error) {
	updatedFiles := []string{}
	for f := range s.updatedNodes {
		updatedFiles = append(updatedFiles, f)
	}
	depends, reason, err := s.depGraph.StepDependsOnFiles(stepName, updatedFiles)
	if err != nil {
		return true, err
	}
	if depends {
		// TODO: move this to the calling func?
		fmt.Println("skipper:", reason)
		return true, nil
	}
	return false, nil
}
