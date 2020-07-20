package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"

	"github.com/metal-stack/v"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	programName = "csilvmctl"
)

var (

	// will bind all viper flags to subcommands and
	// prevent overwrite of identical flag names from other commands
	// see https://github.com/spf13/viper/issues/233#issuecomment-386791444
	bindPFlags = func(cmd *cobra.Command, args []string) {
		viper.BindPFlags(cmd.Flags())
	}

	rootCmd = &cobra.Command{
		Use:          programName,
		Aliases:      []string{"m"},
		Short:        "cli to manage csi-driver-lvm peristent vlumes.",
		Long:         "",
		Version:      v.V.String(),
		SilenceUsage: true,
	}
)

// Execute is the entrypoint of the cient-go application
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		if viper.GetBool("debug") {
			st := errors.WithStack(err)
			fmt.Printf("%+v", st)
		}
		os.Exit(1)
	}
}

func init() {
	var kubeconfig string
	cobra.OnInitialize(initConfig)

	if kubeconfig = os.Getenv("KUBECONFIG"); kubeconfig == "" {
		if home := homeDir(); home != "" {
			kubeconfig = filepath.Join(home, ".kube", "config")
		}
	}

	rootCmd.PersistentFlags().String("kubeconfig", kubeconfig, "Path to the kube-config to use for authentication and authorization. Is updated by login.")
	rootCmd.PersistentFlags().StringP("namespace", "n", "", "namespace")
	rootCmd.PersistentFlags().String("provisioner", "lvm.csi.metal-stack.io", "csi-driver-lvm storage provisioner")
	rootCmd.PersistentFlags().String("vgname", "csi-lvm", "name of the lvm volume group")
	rootCmd.PersistentFlags().String("migrator-pod-image", "metalstack/lvmplugin:v0.3.5", "image used for the migratior pod")
	rootCmd.PersistentFlags().BoolP("yes", "y", false, "answer yes to all questions")

	//rootCmd.AddCommand(completionCmd)
	//rootCmd.AddCommand(zshCompletionCmd)
	rootCmd.AddCommand(migrateCmd)

	err := viper.BindPFlags(rootCmd.PersistentFlags())
	if err != nil {
		log.Fatalf("error setup root cmd:%v", err)
	}
}

func initConfig() {
	viper.SetEnvPrefix(strings.ToUpper(programName))
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
