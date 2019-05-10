/*
Copyright 2018 Craig Johnston <cjimti@gmail.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"log"
	"os"

	"fmt"

	"github.com/figassis/kubefwd/pkg/services"
	"github.com/spf13/cobra"
)

var globalUsage = ``
var Version = "0.0.0"

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kubefwd",
		Short: "Expose Kubernetes services for local development.",
		Example: " kubefwd services --help\n" +
			"  kubefwd svc -n the-project\n" +
			"  kubefwd svc -n the-project -l env=dev,component=api\n" +
			"  kubefwd svc -n default -l \"app in (ws, api)\"\n" +
			"  kubefwd svc -n default -n the-project\n",
		Long: globalUsage,
	}

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version of Kubefwd",
		Long:  ``,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Kubefwd version: %s\nhttps://github.com/txn2/kubefwd\n", Version)
		},
	}

	cmd.AddCommand(versionCmd, services.Cmd)

	return cmd
}

func main() {

	log.Print(` _          _           __             _`)
	log.Print(`| | ___   _| |__   ___ / _|_      ____| |`)
	log.Print(`| |/ / | | | '_ \ / _ \ |_\ \ /\ / / _  |`)
	log.Print(`|   <| |_| | |_) |  __/  _|\ V  V / (_| |`)
	log.Print(`|_|\_\\__,_|_.__/ \___|_|   \_/\_/ \__,_|`)
	log.Print("")
	log.Printf("Version %s", Version)
	log.Print("https://github.com/txn2/kubefwd")
	log.Print("")

	cmd := newRootCmd()

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
