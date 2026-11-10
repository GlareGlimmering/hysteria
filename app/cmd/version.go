package cmd

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/apernet/hysteria/extras/v2/trafficlogger"
	"github.com/spf13/cobra"
)

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version",
	Long:  "Show version.",
	RunE:  runVersionCmd,
}

func init() {
	rootCmd.AddCommand(versionCmd)
	versionCmd.Flags().Bool("json", false, "Print build and compatibility information as JSON")
}

func runVersionCmd(cmd *cobra.Command, args []string) error {
	machine, err := cmd.Flags().GetBool("json")
	if err != nil {
		return err
	}
	if machine {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
			OS      string `json:"os"`
			Arch    string `json:"arch"`
			trafficlogger.Capabilities
		}{appVersion, appCommit, runtime.GOOS, runtime.GOARCH, trafficlogger.CurrentCapabilities()})
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), appAboutLong)
	return err
}
