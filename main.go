package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	v1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"k8s.io/client-go/tools/clientcmd"
)

type Timeout struct {
	WriteTimeout string
	ReadTimeout  string
	Additional   map[string]string
}

type Function struct {
	Name        string
	MaxInflight *int
	Replicas    int

	// https://docs.openfaas.com/tutorials/expanded-timeouts/
	// of-watchdog: exec_timeout
	// classic-watchdog:
	Timeout *Timeout
	Scaling *Scaling
}

func (f *Function) GetMaxInflight() string {
	if f.MaxInflight != nil {
		return fmt.Sprintf("%d", f.MaxInflight)
	}
	return "<not set>"
}

// --label com.openfaas.scale.max=10 \
// --label com.openfaas.scale.target=100 \
// --label com.openfaas.scale.type=cpu \
// --label com.openfaas.scale.target-proportion=0.50 \
// --label com.openfaas.scale.zero=true \
// --label com.openfaas.scale.zero-duration=30m
//
// https://docs.openfaas.com/architecture/autoscaling/
type Scaling struct {
	Min          int
	Max          int
	Type         string
	Target       string
	Proportion   string
	Zero         string
	ZeroDuration string
}

func (s *Scaling) GetType() string {
	if len(s.Type) == 0 {
		return "<not set>"
	}
	return s.Type
}

func (s *Scaling) GetTarget() string {
	if len(s.Target) == 0 {
		return "<not set>"
	}
	return s.Target
}

func newTimeout() *Timeout {
	return &Timeout{
		Additional: make(map[string]string),
	}
}

func main() {

	// Load KUBECONFIG / clientset

	var (
		kubeconfig string
	)

	flag.StringVar(&kubeconfig, "kubeconfig", "$HOME/.kube/config", "Path to KUBECONFIG")
	flag.Parse()

	clientset, err := getClientset(kubeconfig)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	deps, err := clientset.AppsV1().Deployments("openfaas").List(ctx, metav1.ListOptions{
		LabelSelector: "app=openfaas",
	})

	if err != nil {
		panic(err)
	}

	fmt.Printf("OpenFaaS Pro Report\n")

	gatewayReplicas := 0
	gatewayTimeout := newTimeout()
	controllerMode := ""
	controllerTimeout := newTimeout()
	controllerImage := ""
	gatewayImage := ""

	queueWorkerImage := ""
	queueWorkerReplicas := 0
	queueWorkerAckWait := ""
	queueWorkerMaxInflight := 0

	autoscalerImage := ""
	dashboardImage := ""

	for _, dep := range deps.Items {

		if dep.Name == "queue-worker" {
			for _, container := range dep.Spec.Template.Spec.Containers {
				if container.Name == "queue-worker" {
					queueWorkerReplicas = int(*dep.Spec.Replicas)
					for _, env := range container.Env {
						if env.Name == "ack_wait" {
							queueWorkerAckWait = env.Value
						}

						if env.Name == "max_inflight" {
							queueWorkerMaxInflight, _ = strconv.Atoi(env.Value)
						}
					}
					queueWorkerImage = container.Image
				}
			}
		}

		if dep.Name == "gateway" {
			for _, container := range dep.Spec.Template.Spec.Containers {
				if container.Name == "gateway" {
					gatewayReplicas = int(*dep.Spec.Replicas)
					for _, env := range container.Env {
						if env.Name == "read_timeout" {
							gatewayTimeout.ReadTimeout = env.Value
						}
						if env.Name == "write_timeout" {
							gatewayTimeout.WriteTimeout = env.Value
						}
						if env.Name == "upstream_timeout" {
							gatewayTimeout.Additional["upstream_timeout"] = env.Value
						}
					}
					gatewayImage = container.Image
				}
				if container.Name == "faas-netes" {
					controllerMode = container.Name
					for _, env := range container.Env {
						if env.Name == "read_timeout" {
							controllerTimeout.ReadTimeout = env.Value
						}
						if env.Name == "write_timeout" {
							controllerTimeout.WriteTimeout = env.Value
						}

					}
					controllerImage = container.Image
				}
				if container.Name == "operator" {
					controllerMode = container.Name
					for _, env := range container.Env {
						if env.Name == "read_timeout" {
							if env.Name == "read_timeout" {
								controllerTimeout.ReadTimeout = env.Value
							}
							if env.Name == "write_timeout" {
								controllerTimeout.WriteTimeout = env.Value
							}
						}
					}
					controllerImage = container.Image
				}
			}
		}

		if dep.Name == "autoscaler" {
			for _, container := range dep.Spec.Template.Spec.Containers {
				if container.Name == "autoscaler" {
					autoscalerImage = container.Image
				}
			}
		}
		if dep.Name == "dashboard" {
			for _, container := range dep.Spec.Template.Spec.Containers {
				if container.Name == "dashboard" {
					dashboardImage = container.Image
				}
			}
		}
	}

	functionDeps, err := clientset.AppsV1().
		Deployments("openfaas-fn").
		List(ctx, metav1.ListOptions{})
	if err != nil {
		panic(err)
	}

	functions := readFunctions(functionDeps.Items)

	k8sVer, err := clientset.ServerVersion()
	if err != nil {
		panic(err)
	}

	fmt.Printf("\nGateway\n\n")

	fmt.Printf("gateway image: %s\n", gatewayImage)
	fmt.Printf("controller image: %s\n", controllerImage)

	fmt.Printf("gateway_replicas: %d\n", gatewayReplicas)
	fmt.Printf("gateway_timeout: %s\n", gatewayTimeout)
	fmt.Printf("controller_mode: %s\n", controllerMode)
	fmt.Printf("controller_timeout: %s\n", controllerTimeout)

	fmt.Printf("\nQueue-worker\n\n")

	fmt.Printf("queue_worker_image: %s\n", queueWorkerImage)
	fmt.Printf("queue_worker_replicas: %d\n", queueWorkerReplicas)
	fmt.Printf("queue_worker_ack_wait: %s\n", queueWorkerAckWait)
	fmt.Printf("queue_worker_max_inflight: %d\n", queueWorkerMaxInflight)
	fmt.Printf("\n")

	if len(autoscalerImage) > 0 {
		fmt.Printf("\nautoscaler\n\n")

		fmt.Printf("autoscaler_image: %s", autoscalerImage)
	}

	if len(dashboardImage) > 0 {
		fmt.Printf("\ndashboard\n\n")

		fmt.Printf("dashboard_image: %s", dashboardImage)
	}

	fmt.Printf("\n")

	proGatewayIcon := "❌"
	if strings.Contains(gatewayImage, "openfaasltd") {
		proGatewayIcon = "✅"
	}

	operatorIcon := "❌"
	if controllerMode == "operator" {
		operatorIcon = "✅"
	}

	autoscalerIcon := "❌"
	if len(autoscalerImage) > 0 {
		autoscalerIcon = "✅"
	}

	dashboardIcon := "❌"
	if len(dashboardImage) > 0 {
		dashboardIcon = "✅"
	}

	fmt.Printf(`
Features detected:

- %s Pro gateway
- %s Operator mode
- %s Autoscaler
- %s Dashboard

`, proGatewayIcon, operatorIcon, autoscalerIcon, dashboardIcon)

	fmt.Printf(`Other:

- Kubernetes version: %s
- Asynchronous concurrency (cluster): %d
`, k8sVer,
		(queueWorkerReplicas * queueWorkerMaxInflight))

	fmt.Printf("\n")

	fmt.Printf("\nFunctions:\n\n")

	if len(functions) == 0 {
		fmt.Printf("None detected\n")
	}

	for _, fn := range functions {
		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 1, ' ', 0)
		fmt.Fprintf(w, "%s\t(%d replicas)\n\n", fn.Name, fn.Replicas)

		fmt.Fprintf(w, "- %s\t%s\n", "read_timeout", fn.Timeout.ReadTimeout)
		fmt.Fprintf(w, "- %s\t%s\n", "write_timeout", fn.Timeout.WriteTimeout)
		if v, ok := fn.Timeout.Additional["exec_timeout"]; ok {
			fmt.Fprintf(w, "- %s\t%s\n", "exec_timeout", v)
		} else {
			fmt.Fprintf(w, "- %s\t%s\n", "exec_timeout", "<not set>")
		}

		if len(autoscalerImage) > 0 {

			fmt.Fprintf(w, "- %s\t%s\n", "scaling type", fn.Scaling.GetType())
			fmt.Fprintf(w, "- %s\t%s\n", "scaling target", fn.Scaling.GetTarget())
		}

		// desc := item.Description
		// if !verbose {
		// 	desc = storeRenderDescription(desc)
		// }

		// fmt.Fprintf(w, "%s\t%s\n", "Description", desc)
		// fmt.Fprintf(w, "%s\t%s\n", "Image", item.GetImageName(platform))
		// fmt.Fprintf(w, "%s\t%s\n", "Process", item.Fprocess)
		// fmt.Fprintf(w, "%s\t%s\n", "Repo URL", item.RepoURL)

		fmt.Fprintln(w)
		w.Flush()
		fmt.Print(b.String())

	}

	// Query objects

	// read_ write_timeout
	// faas-netes / gateway deployment, the timeouts from both functions
	// timeouts for each function

	// ack_wait
	// queue-worker deployment

	// Print with check boxes

	// RBAC YAML

	// Deployment or job YAML

	// Dockerfile / Makefile

}

func readFunctions(deps []v1.Deployment) []Function {

	var functions []Function

	for _, dep := range deps {
		function := Function{
			Name:     dep.Name,
			Timeout:  newTimeout(),
			Scaling:  &Scaling{},
			Replicas: int(*dep.Spec.Replicas),
		}

		for _, container := range dep.Spec.Template.Spec.Containers {
			for _, env := range container.Env {
				if env.Name == "max_inflight" {
					// maxInflight, err := strconv.Atoi(env.Value)
					// if err != nil {

					// }
					// function.MaxInflight = stenv.Value
				}
			}
		}

		functions = append(functions, function)
	}

	return functions
}

func getClientset(kubeconfig string) (*kubernetes.Clientset, error) {

	kubeconfig = strings.ReplaceAll(kubeconfig, "$HOME", os.Getenv("HOME"))
	kubeconfig = strings.ReplaceAll(kubeconfig, "~", os.Getenv("HOME"))
	masterURL := ""

	var clientConfig *rest.Config
	if _, err := os.Stat(kubeconfig); err != nil {
		config, err := rest.InClusterConfig()
		if err != nil {
			log.Fatalf("Error building in-cluster config: %s", err.Error())
		}
		clientConfig = config
	} else {
		config, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
		if err != nil {
			log.Fatalf("Error building kubeconfig: %s %s", kubeconfig, err.Error())
		}
		clientConfig = config
	}

	clientset, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}
