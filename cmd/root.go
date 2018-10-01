package cmd

import (
	"fmt"
	"os"
	"os/exec"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
	buildID string
)

// Skipper needs to be run with a -i <buildId>. If that flag wasn't set, we spawn a child skipper process with that flag.

func childSkipperArgs(buildID string, args []string) []string {
	i := 1
	args = append(args[:i], append([]string{"-i", buildID}, args[i:]...)...)

	return args
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
		cm := exec.Command(args[0], args[1:]...)
		cm.Stdout = os.Stdout
		cm.Stderr = os.Stderr
		if err := cm.Run(); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
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

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.skipper.yaml)")
	rootCmd.PersistentFlags().StringVar(&buildID, "i", "", "ID for this build. If empty, it looks for a /yourbase file with a build ID otherwise it creates one with a random build ID. Once a build ID is determined, skipper spawns a child process of itself but passing -i <id> accordingly")
	rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
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
