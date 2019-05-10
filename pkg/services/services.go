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
package services

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/txn2/txeh"

	"github.com/figassis/kubefwd/pkg/fwdcfg"
	"github.com/figassis/kubefwd/pkg/fwdhost"
	"github.com/figassis/kubefwd/pkg/fwdnet"
	"github.com/figassis/kubefwd/pkg/fwdport"
	"github.com/figassis/kubefwd/pkg/fwdpub"
	"github.com/figassis/kubefwd/pkg/utils"

	"github.com/spf13/cobra"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	restclient "k8s.io/client-go/rest"
)

var namespaces []string
var contexts []string
var exitOnFail bool
var verbose bool

func init() {
	// override error output from k8s.io/apimachinery/pkg/util/runtime
	runtime.ErrorHandlers[0] = func(err error) {
		log.Printf("Runtime error: %s", err.Error())
	}

	cfgFilePath := ""

	if home := fwdhost.HomeDir(); home != "" {
		cfgFilePath = filepath.Join(home, ".kube", "config")
	}

	// if sudo -E is used and the KUBECONFIG environment variable is set
	// make it the default, override with command line.
	envCfg, ok := os.LookupEnv("KUBECONFIG")
	if ok {
		if envCfg != "" {
			cfgFilePath = envCfg
		}
	}

	Cmd.Flags().StringP("kubeconfig", "c", cfgFilePath, "absolute path to a kubectl config file")
	Cmd.Flags().StringSliceVarP(&contexts, "context", "x", []string{}, "specify a context to override the current context")
	Cmd.Flags().StringSliceVarP(&namespaces, "namespace", "n", []string{}, "Specify a namespace. Specify multiple namespaces by duplicating this argument.")
	Cmd.Flags().StringP("selector", "l", "", "Selector (label query) to filter on; supports '=', '==', and '!=' (e.g. -l key1=value1,key2=value2).")
	Cmd.Flags().BoolVarP(&exitOnFail, "exitonfailure", "", false, "Exit(1) on failure. Useful for forcing a container restart.")
	Cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output.")

}

var Cmd = &cobra.Command{
	Use:     "services",
	Aliases: []string{"svcs", "svc"},
	Short:   "Forward services",
	Long:    `Forward multiple Kubernetes services from one or more namespaces. Filter services with selector.`,
	Example: "  kubefwd svc -n the-project\n" +
		"  kubefwd svc -n the-project -l app=wx,component=api\n" +
		"  kubefwd svc -n default -n the-project\n" +
		"  kubefwd svc -n the-project -x prod-cluster\n",
	Run: func(cmd *cobra.Command, args []string) {

		hasRoot, err := utils.CheckRoot()

		if !hasRoot {
			fmt.Printf(`
This program requires superuser privileges to run. These
privileges are required to add IP address aliases to your
loopback interface. Superuser privileges are also needed
to listen on low port numbers for these IP addresses.

Try:
 - sudo -E kubefwd services (Unix)
 - Running a shell with administrator rights (Windows)

`)
			if err != nil {
				log.Fatalf("Root check failure: %s", err.Error())
			}
			return
		}

		log.Println("Press [Ctrl-C] to stop forwarding.")
		log.Println("'cat /etc/hosts' to see all host entries.")

		hostFile, err := txeh.NewHostsDefault()
		if err != nil {
			log.Fatalf("Hostfile error: %s", err.Error())
		}

		log.Printf("Loaded hosts file %s\n", hostFile.ReadFilePath)

		msg, err := fwdhost.BackupHostFile(hostFile)
		if err != nil {
			log.Fatalf("Error backing up hostfile: %s\n", err.Error())
		}

		log.Printf("Hostfile management: %s", msg)

		// NOTE: may be using the default set in init()
		cfgFilePath := cmd.Flag("kubeconfig").Value.String()
		if cfgFilePath == "" {
			log.Fatalf("No config found. Use --kubeconfig to specify one")
		}

		clientConfig, err := fwdcfg.GetConfig(cfgFilePath)
		if err != nil {
			log.Fatalf("Error reading configuration configuration: %s\n", err.Error())
		}

		// labels selector to filter services
		// see: https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/
		selector := cmd.Flag("selector").Value.String()
		listOptions := metav1.ListOptions{}
		if selector != "" {
			listOptions.LabelSelector = selector
		}

		// if no namespaces were specified, check config then
		// explicitly set one to "default"
		if len(namespaces) < 1 {
			namespaces = []string{"default"}
			x := clientConfig.CurrentContext

			// use the first context if specified
			if len(contexts) > 0 {
				x = contexts[0]
			}

			for _, ctx := range clientConfig.Contexts {
				if ctx.Name == x {
					if ctx.Context.Namespace != "" {
						log.Printf("Using namespace %s from current context %s.", ctx.Context.Namespace, ctx.Name)
						namespaces = []string{ctx.Context.Namespace}
						break
					}
				}
			}
		}

		// ipC is the class C for the local IP address
		// increment this for each cluster
		ipC := 27

		wg := &sync.WaitGroup{}

		// if no context override
		if len(contexts) < 1 {
			contexts = append(contexts, clientConfig.CurrentContext)
		}

		for i, ctx := range contexts {

			// k8s REST config
			restConfig, err := fwdcfg.GetRestConfig(cfgFilePath, ctx)
			if err != nil {
				log.Fatalf("Error generating REST configuration: %s\n", err.Error())
			}

			// create the k8s REST client set
			clientSet, err := kubernetes.NewForConfig(restConfig)
			if err != nil {
				log.Fatalf("Error creating k8s client: %s\n", err.Error())
			}

			for ii, namespace := range namespaces {
				err = fwdServices(FwdServiceOpts{
					Wg:           wg,
					ClientSet:    clientSet,
					Context:      ctx,
					Namespace:    namespace,
					ListOptions:  listOptions,
					Hostfile:     hostFile,
					ClientConfig: restConfig,
					// only use short name for the first namespace and context
					ShortName:  i < 1 && ii < 1,
					Remote:     i > 0,
					IpC:        byte(ipC),
					ExitOnFail: exitOnFail,
				})
				if err != nil {
					log.Printf("Error forwarding service: %s\n", err.Error())
				}

				ipC = ipC + 1
			}
		}

		wg.Wait()

		log.Printf("Done...\n")

		err = hostFile.Save()
		if err != nil {
			log.Fatalf("Error saving hostfile: %s\n", err.Error())
		}
	},
}

type FwdServiceOpts struct {
	Wg           *sync.WaitGroup
	ClientSet    *kubernetes.Clientset
	Context      string
	Namespace    string
	ListOptions  metav1.ListOptions
	Hostfile     *txeh.Hosts
	ClientConfig *restclient.Config
	ShortName    bool
	Remote       bool
	IpC          byte
	ExitOnFail   bool
}

func fwdServices(opts FwdServiceOpts) error {

	services, err := opts.ClientSet.CoreV1().Services(opts.Namespace).List(opts.ListOptions)
	if err != nil {
		return err
	}
	if len(services.Items) < 1 {
		log.Printf("WARNING: No services found for namespace %s.\n", opts.Namespace)
		return nil
	}

	publisher := &fwdpub.Publisher{
		PublisherName: "Services",
		Output:        false,
	}

	d := 1

	// loop through the services
	for _, svc := range services.Items {
		selector := mapToSelectorStr(svc.Spec.Selector)

		if selector == "" {
			log.Printf("WARNING: No backing pods for service %s in %s on cluster %s.\n", svc.Name, svc.Namespace, svc.ClusterName)

			continue
		}

		pods, err := opts.ClientSet.CoreV1().Pods(svc.Namespace).List(metav1.ListOptions{LabelSelector: selector})

		if err != nil {
			log.Printf("WARNING: No pods found for %s: %s\n", selector, err.Error())

			// TODO: try again after a time

			continue
		}

		if len(pods.Items) < 1 {
			log.Printf("WARNING: No pods returned for service %s in %s on cluster %s.\n", svc.Name, svc.Namespace, svc.ClusterName)

			// TODO: try again after a time

			continue
		}

		if svc.Spec.ClusterIP != "None" {
			pods.Items = pods.Items[:1]
		}

		for _, pod := range pods.Items {

			podPort := ""
			svcName := ""

			localIp, dInc, err := fwdnet.ReadyInterface(127, 1, opts.IpC, d, podPort)
			d = dInc

			for _, port := range svc.Spec.Ports {

				podPort = port.TargetPort.String()
				localPort := strconv.Itoa(int(port.Port))

				if _, err := strconv.Atoi(podPort); err != nil {
					// search a pods containers for the named port
					if namedPodPort, ok := portSearch(podPort, pod.Spec.Containers); ok == true {
						podPort = namedPodPort
					}
				}

				_, err = opts.ClientSet.CoreV1().Pods(pod.Namespace).Get(pod.Name, metav1.GetOptions{})
				if err != nil {
					log.Printf("WARNING: Error getting pod: %s\n", err.Error())
					break
				}

				serviceHostName := svc.Name

				if svc.Spec.ClusterIP == "None" {
					serviceHostName = pod.Name + "." + serviceHostName
				}

				svcName = serviceHostName

				if opts.ShortName != true {
					serviceHostName = serviceHostName + "." + pod.Namespace
				}

				if opts.Remote {
					serviceHostName = fmt.Sprintf("%s.svc.cluster.%s", serviceHostName, opts.Context)
				}

				if verbose {
					log.Printf("Resolving:  %s%s to %s\n",
						svc.Name,
						serviceHostName,
						localIp.String(),
					)
				}

				log.Printf("Forwarding: %s:%d to pod %s:%s\n",
					serviceHostName,
					port.Port,
					pod.Name,
					podPort,
				)

				pfo := &fwdport.PortForwardOpts{
					Out:        publisher,
					Config:     opts.ClientConfig,
					ClientSet:  opts.ClientSet,
					Context:    opts.Context,
					Namespace:  pod.Namespace,
					Service:    svcName,
					PodName:    pod.Name,
					PodPort:    podPort,
					LocalIp:    localIp,
					LocalPort:  localPort,
					Hostfile:   opts.Hostfile,
					ShortName:  opts.ShortName,
					Remote:     opts.Remote,
					ExitOnFail: exitOnFail,
				}

				opts.Wg.Add(1)
				go func() {
					err := fwdport.PortForward(pfo)
					if err != nil {
						log.Printf("ERROR: %s", err.Error())
					}

					log.Printf("Stopped forwarding %s in %s.", pfo.Service, pfo.Namespace)

					opts.Wg.Done()
				}()

			}
		}
	}

	return nil
}

func portSearch(portName string, containers []v1.Container) (string, bool) {

	for _, container := range containers {
		for _, cp := range container.Ports {
			if cp.Name == portName {
				return fmt.Sprint(cp.ContainerPort), true
			}
		}
	}

	return "", false
}

func mapToSelectorStr(msel map[string]string) string {
	selector := ""
	for k, v := range msel {
		if selector != "" {
			selector = selector + ","
		}
		selector = selector + fmt.Sprintf("%s=%s", k, v)
	}

	return selector
}
