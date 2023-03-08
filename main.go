package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

func (t *Timeout) GetWriteTimeout() time.Duration {
	r, err := time.ParseDuration(t.WriteTimeout)
	if err != nil {
		log.Fatalf("Error parsing write timeout: %v", err)
	}
	return r
}
func (t *Timeout) GetAdditionalTimeout(key string) (time.Duration, error) {
	if v, ok := t.Additional[key]; ok {
		r, err := time.ParseDuration(v)
		if err != nil {
			log.Fatalf("Error parsing write timeout: %v", err)
		}
		return r, nil
	}
	return time.Second * 0, fmt.Errorf("%s not found", key)
}

func (t *Timeout) GetReadTimeout() time.Duration {
	r, err := time.ParseDuration(t.WriteTimeout)
	if err != nil {
		log.Fatalf("Error parsing write timeout: %v", err)
	}
	return r
}

type FunctionResources struct {
	Memory string
	CPU    string
}

func (r *FunctionResources) GetMemory() string {
	if r.Memory == "0" {
		return "<none>"
	}
	return r.Memory
}

func (r *FunctionResources) GetCpu() string {
	if r.CPU == "0" {
		return "<none>"
	}
	return r.CPU
}

type Function struct {
	Name        string
	MaxInflight *int
	Replicas    int

	// https://docs.openfaas.com/tutorials/expanded-timeouts/
	// of-watchdog: exec_timeout
	// classic-watchdog:
	Timeout                *Timeout
	Scaling                *Scaling
	Requests               *FunctionResources
	Limits                 *FunctionResources
	ReadOnlyRootFilesystem bool
}

func (f *Function) GetMaxInflight() string {
	if f.MaxInflight != nil {
		return fmt.Sprintf("%d", *f.MaxInflight)
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
	Min          *int
	Max          *int
	Type         string
	Target       string
	Proportion   string
	Zero         string
	ZeroDuration string
}

func (s *Scaling) GetMax() string {
	if s.Max != nil {
		return fmt.Sprintf("%d", *s.Max)
	}
	return "<not set>"
}

func (s *Scaling) GetMin() string {
	if s.Min != nil {
		return fmt.Sprintf("%d", *s.Min)
	}
	return "<not set>"
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

func (s *Scaling) GetProportion() string {
	if len(s.Proportion) == 0 {
		return "<not set>"
	}
	return s.Proportion
}

func (s *Scaling) GetZero() string {
	if len(s.Zero) == 0 {
		return "<not set>"
	}
	return s.Zero
}

func (s *Scaling) GetZeroDuration() string {
	if len(s.ZeroDuration) == 0 {
		return "<not set>"
	}
	return s.ZeroDuration
}

func newTimeout() *Timeout {
	return &Timeout{
		Additional:   make(map[string]string),
		WriteTimeout: "",
		ReadTimeout:  "",
	}
}

func main() {

	// Load KUBECONFIG / clientset

	var (
		kubeconfig            string
		openfaasCoreNamespace string
	)

	flag.StringVar(&kubeconfig, "kubeconfig", "$HOME/.kube/config", "Path to KUBECONFIG")
	flag.StringVar(&openfaasCoreNamespace, "openfaas-namespace", "openfaas", "Namespace for the OpenFaaS installation")
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

	namespaces, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		panic(err)
	}

	istioDetected := false

	openfaasCoreNamespaceDetected := false
	functionNamespaces := []string{"openfaas-fn"}
	sort.Strings(functionNamespaces)

	for _, n := range namespaces.Items {
		if n.Name == openfaasCoreNamespace {
			openfaasCoreNamespaceDetected = true
		}
		if n.Name == "istio-system" {
			istioDetected = true
		}

		if _, ok := n.Annotations["openfaas"]; ok {
			functionNamespaces = append(functionNamespaces, n.Name)
		}
	}

	if !openfaasCoreNamespaceDetected {
		log.Fatalf("OpenFaaS Core namespace \"%s\" not found. Exiting", openfaasCoreNamespace)
	}

	gatewayReplicas := 0
	gatewayTimeout := newTimeout()
	controllerMode := ""
	controllerTimeout := newTimeout()
	controllerImage := ""
	controllerSetNonRootUser := false
	gatewayImage := ""
	proGateway := false

	asyncEnabled := false
	queueWorkerImage := ""
	queueWorkerReplicas := 0
	queueWorkerAckWait := ""
	queueWorkerMaxInflight := 0

	autoscalerImage := ""
	autoscalerReplicas := 0
	dashboardImage := ""
	dashboardJWTSecret := false

	directFunctions := false
	probeFunctions := false
	clusterRole := false
	jetstream := false
	internalNats := false

	for _, dep := range deps.Items {

		if dep.Name == "queue-worker" {
			for _, container := range dep.Spec.Template.Spec.Containers {
				if container.Name == "queue-worker" {
					asyncEnabled = true
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
					jetstream = strings.Contains(queueWorkerImage, "jetstream-queue-worker")
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
						if env.Name == "probe_functions" {
							probeFunctions, err = strconv.ParseBool(env.Value)
							if err != nil {
								log.Fatalf("Error parsing probe_functions: %v, value: %s", err, env.Value)
							}
						}
						if env.Name == "direct_functions" {
							directFunctions, err = strconv.ParseBool(env.Value)
							if err != nil {
								log.Fatalf("Error parsing direct_functions: %v, value: %s", err, env.Value)
							}
						}
					}
					gatewayImage = container.Image
					proGateway = isProComponent(container)
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
						if env.Name == "set_nonroot_user" {
							controllerSetNonRootUser, err = strconv.ParseBool(env.Value)
							if err != nil {
								log.Fatalf("Error parsing set_nonroot_user: %v, value: %s", err, env.Value)
							}
						}
						if env.Name == "cluster_role" {
							clusterRole, err = strconv.ParseBool(env.Value)
							if err != nil {
								log.Fatalf("Error parsing cluster_role: %v, value: %s", err, env.Value)
							}
						}
					}
					controllerImage = container.Image
				}
				if container.Name == "operator" {
					controllerMode = container.Name
					for _, env := range container.Env {
						if env.Name == "read_timeout" {
							controllerTimeout.ReadTimeout = env.Value
						}
						if env.Name == "write_timeout" {
							controllerTimeout.WriteTimeout = env.Value
						}
						if env.Name == "set_nonroot_user" {
							controllerSetNonRootUser, err = strconv.ParseBool(env.Value)
							if err != nil {
								log.Fatalf("Error parsing set_nonroot_user: %v, value: %s", err, env.Value)
							}
						}
						if env.Name == "cluster_role" {
							clusterRole, err = strconv.ParseBool(env.Value)
							if err != nil {
								log.Fatalf("Error parsing cluster_role: %v, value: %s", err, env.Value)
							}
						}
					}
					controllerImage = container.Image
				}
			}
		}

		if dep.Name == "autoscaler" {
			for _, container := range dep.Spec.Template.Spec.Containers {
				autoscalerReplicas = int(*dep.Spec.Replicas)
				if container.Name == "autoscaler" {
					autoscalerImage = container.Image
				}
			}
		}
		if dep.Name == "dashboard" {
			for _, container := range dep.Spec.Template.Spec.Containers {
				if container.Name == "dashboard" {
					dashboardImage = container.Image

					for _, volumeMount := range container.VolumeMounts {
						if volumeMount.Name == "dashboard-jwt" {
							dashboardJWTSecret = true
						}
					}
				}
			}
		}
		if dep.Name == "nats" {
			for _, container := range dep.Spec.Template.Spec.Containers {
				if container.Name == "nats" {
					internalNats = true
				}
			}
		}
	}

	var nsFunctions = make(map[string][]Function)

	for _, namespace := range functionNamespaces {
		functionDeps, err := clientset.AppsV1().
			Deployments(namespace).
			List(ctx, metav1.ListOptions{})

		if err != nil {
			panic(err)
		}

		nsFunctions[namespace] = readFunctions(functionDeps.Items)
	}

	k8sVer, err := clientset.ServerVersion()
	if err != nil {
		panic(err)
	}

	fmt.Printf("\nGateway\n\n")

	fmt.Printf("- gateway image: %s\n", gatewayImage)
	fmt.Printf("- controller image: %s\n", controllerImage)

	fmt.Printf("- gateway_replicas: %d\n", gatewayReplicas)
	fmt.Printf("- gateway_timeout - read: %s write: %s upstream: %s\n", gatewayTimeout.ReadTimeout, gatewayTimeout.WriteTimeout, gatewayTimeout.Additional["upstream_timeout"])
	fmt.Printf("- controller_mode: %s\n", controllerMode)
	fmt.Printf("- controller_timeout - read: %s write: %s\n", controllerTimeout.ReadTimeout, controllerTimeout.WriteTimeout)

	if asyncEnabled {
		fmt.Printf("\nQueue-worker\n\n")

		fmt.Printf("- queue_worker_image: %s\n", queueWorkerImage)
		fmt.Printf("- queue_worker_replicas: %d\n", queueWorkerReplicas)
		fmt.Printf("- queue_worker_ack_wait: %s\n", queueWorkerAckWait)
		fmt.Printf("- queue_worker_max_inflight: %d\n", queueWorkerMaxInflight)
	}

	fmt.Printf("\nFunction namespaces:\n\n")
	for _, namespace := range functionNamespaces {
		fmt.Printf("- %s\n", namespace)
	}

	if len(autoscalerImage) > 0 {
		fmt.Printf("\nAutoscaler\n\n")

		fmt.Printf("- autoscaler_image: %s\n", autoscalerImage)
	}

	if len(dashboardImage) > 0 {
		fmt.Printf("\nDashboard\n\n")

		fmt.Printf("- dashboard_image: %s\n", dashboardImage)

	}

	asyncIcon := "❌"
	if asyncEnabled {
		asyncIcon = "✅"
	}

	proGatewayIcon := "❌"
	if proGateway {
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
	gwHAIcon := "❌"
	if gatewayReplicas >= 3 {
		gwHAIcon = "✅"
	}
	istioIcon := "❌"
	if istioDetected {
		istioIcon = "✅"
	}
	jetstreamIcon := "❌"
	if jetstream {
		jetstreamIcon = "✅"
	}

	fmt.Printf(`
Features detected:

- %s Async
- %s Pro gateway
- %s HA Gateway
- %s Operator mode
- %s Autoscaler
- %s Dashboard
- %s JetStream
- %s Istio

`, asyncIcon, proGatewayIcon, gwHAIcon, operatorIcon, autoscalerIcon, dashboardIcon, jetstreamIcon, istioIcon)

	fmt.Printf(`Other:

- Kubernetes version: %s
- Asynchronous concurrency (cluster): %d
`, k8sVer,
		(queueWorkerReplicas * queueWorkerMaxInflight))

	fmt.Printf("\n")

	for _, namespace := range functionNamespaces {
		fmt.Printf("\nFunctions in (%s):\n\n", namespace)
		functions, ok := nsFunctions[namespace]
		if ok {
			if len(functions) == 0 {
				fmt.Printf("None detected\n")
			}

			for _, fn := range functions {
				printFunction(fn, len(autoscalerImage) > 0)
			}
		}
	}

	fmt.Printf("\nWarnings:\n\n")

	gwUpstreamTimeout, err := gatewayTimeout.GetAdditionalTimeout("upstream_timeout")
	if err != nil {
		log.Fatalf("unable to parse upstream_timeout: %s", err)
	}

	if asyncEnabled {
		ackWaitDuration, err := time.ParseDuration(queueWorkerAckWait)
		if err != nil {
			log.Fatalf("unable to parse queue-worker ack_wait: %s", err)
		}

		if ackWaitDuration > gwUpstreamTimeout {
			fmt.Printf("⚠️ queue-worker ack_wait (%s) must be <= gateway.upstream_timeout (%s)\n", queueWorkerAckWait, gwUpstreamTimeout)
		}

		if (queueWorkerReplicas * queueWorkerMaxInflight) < 100 {
			fmt.Printf("⚠️ queue-worker maximum concurrency is (%d), this may be too low\n", queueWorkerMaxInflight*queueWorkerReplicas)
		}

		if queueWorkerMaxInflight > 500 {
			fmt.Printf("⚠️ queue-worker max_inflight is (%d), this may be too high\n", queueWorkerMaxInflight)
		}

		if queueWorkerReplicas < 3 {
			fmt.Printf("⚠️ queue-worker replicas want >= %d but got %d, (not Highly Available (HA))\n", 3, queueWorkerReplicas)
		}

		if internalNats {
			fmt.Printf("⚠️ Use external NATS to ensure high-availability and persistence\n")
		}
	}

	if gatewayReplicas < 3 {
		fmt.Printf("⚠️ gateway replicas want >= %d but got %d, (not Highly Available (HA))\n", 3, gatewayReplicas)
	}

	if !jetstream {
		fmt.Printf("⚠️ NATS Streaming will be deprecated and replaced with NATS JetStream: https://www.openfaas.com/blog/jetstream-for-openfaas/\n")
	}

	if istioDetected && directFunctions == false {
		fmt.Printf("⚠️ Istio detected, but direct_functions is disabled\n")
	}

	if istioDetected && probeFunctions == false {
		fmt.Printf("⚠️ Istio detected, but probe_functions is disabled\n")
	}

	if len(autoscalerImage) > 0 && clusterRole == false {
		fmt.Printf("⚠️ Pro autoscaler detected, but cluster_role is disabled - unable to collect CPU/RAM metrics\n")
	}

	if autoscalerReplicas > 1 {
		fmt.Printf("⚠️ autoscaler replicas should be 1 to prevent double scaling actions\n")
	}

	if controllerMode != "operator" {
		fmt.Printf("⚠️ Operator mode is not enabled, OpenFaaS Pro customers should use the OpenFaaS operator\n")
	}

	if proGateway && len(autoscalerImage) == 0 {
		fmt.Printf("⚠️ Pro gateway detected, but autoscaler is not enabled\n")
	}

	if controllerSetNonRootUser == false {
		fmt.Printf("⚠️ Non-root flag is not set for the controller/operator\n")
	}

	if len(dashboardImage) > 0 && dashboardJWTSecret == false {
		fmt.Printf("⚠️ Dashboard uses auto generated signing keys: https://docs.openfaas.com/openfaas-pro/dashboard/#create-a-signing-key \n")
	}

	for _, namespace := range functionNamespaces {
		functions, ok := nsFunctions[namespace]
		if ok {
			printFunctionWarnings(functions, namespace, gwUpstreamTimeout)
		}
	}
}

func printFunctionWarnings(functions []Function, namespace string, gwUpstreamTimeout time.Duration) {
	noReadonlyRootfs := 0
	scalingDown := 0
	for _, fn := range functions {
		scalingConfigured := fn.Scaling != nil

		if !fn.ReadOnlyRootFilesystem {
			noReadonlyRootfs++
		}

		if scalingConfigured && fn.Scaling.GetZero() == "true" {
			scalingDown++
		}

		if scalingConfigured && fn.Scaling.GetZeroDuration() != "<not set>" {
			dur, err := time.ParseDuration(fn.Scaling.GetZeroDuration())
			if err == nil && dur < time.Minute*5 {
				fmt.Printf("⚠️ %s.%s scales down after %.2f minutes, this may be too soon, 5 minutes or higher is recommended\n", fn.Name, namespace, dur.Minutes())
			}
		}

		if len(fn.Timeout.ReadTimeout) == 0 {
			fmt.Printf("⚠️ %s.%s read_timeout is not set\n", fn.Name, namespace)
		} else if fn.Timeout.GetReadTimeout() > gwUpstreamTimeout {
			fmt.Printf("⚠️ %s.%s read_timeout (%s) is greater than gateway.upstream_timeout (%s)\n", fn.Name, namespace, fn.Timeout.ReadTimeout, gwUpstreamTimeout)
		}

		if len(fn.Timeout.WriteTimeout) == 0 {
			fmt.Printf("⚠️ %s.%s write_timeout is not set\n", fn.Name, namespace)
		} else if fn.Timeout.GetWriteTimeout() > gwUpstreamTimeout {
			fmt.Printf("⚠️ %s.%s write_timeout (%s) is greater than gateway.upstream_timeout (%s)\n", fn.Name, namespace, fn.Timeout.WriteTimeout, gwUpstreamTimeout)
		}

		execTimeout, err := fn.Timeout.GetAdditionalTimeout("exec_timeout")
		if err != nil {
			fmt.Printf("⚠️ %s.%s exec_timeout is not set\n", fn.Name, namespace)
		} else if execTimeout > gwUpstreamTimeout {
			fmt.Printf("⚠️ %s.%s exec_timeout (%s) is greater than gateway.upstream_timeout (%s)\n", fn.Name, namespace, execTimeout, gwUpstreamTimeout)
		}

		if fn.Requests.Memory == "0" {
			fmt.Printf("⚠️ %s.%s no memory requests set\n", fn.Name, namespace)
		}
	}

	if len(functions) > 0 && scalingDown == 0 {
		fmt.Printf("⚠️ no functions in namespace %s are configured to scale down, this may be inefficient\n", namespace)
	}

	if noReadonlyRootfs > 0 {
		fmt.Printf("⚠️ at least one function in namespace %s does not set the file system to read-only\n", namespace)
	}
}

func printFunction(fn Function, autoscaling bool) {
	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 1, ' ', 0)
	fmt.Fprintf(w, "* %s\t(%d replicas)\n\n", fn.Name, fn.Replicas)

	if len(fn.Timeout.ReadTimeout) > 0 {
		fmt.Fprintf(w, "- %s\t%s\n", "read_timeout", fn.Timeout.ReadTimeout)
	} else {
		fmt.Fprintf(w, "- %s\t%s\n", "read_timeout", "<not set>")
	}
	if len(fn.Timeout.WriteTimeout) > 0 {
		fmt.Fprintf(w, "- %s\t%s\n", "write_timeout", fn.Timeout.WriteTimeout)
	} else {
		fmt.Fprintf(w, "- %s\t%s\n", "write_timeout", "<not set>")
	}
	if v, ok := fn.Timeout.Additional["exec_timeout"]; ok {
		fmt.Fprintf(w, "- %s\t%s\n", "exec_timeout", v)
	} else {
		fmt.Fprintf(w, "- %s\t%s\n", "exec_timeout", "<not set>")
	}

	if autoscaling {

		if fn.Scaling == nil {
			fmt.Fprintf(w, "\nno scaling configuration was set\n")
		} else {
			fmt.Fprintf(w, "\nscaling configuration\n")

			fmt.Fprintf(w, "\n- %s\t%s\n", "min/max replicas", fmt.Sprintf("(%s / %s)", fn.Scaling.GetMin(), fn.Scaling.GetMax()))
			fmt.Fprintf(w, "- %s\t%s\n", "type", fn.Scaling.GetType())
			fmt.Fprintf(w, "- %s\t%s\n", "target", fn.Scaling.GetTarget())
			fmt.Fprintf(w, "- %s\t%s\n", "target-proportion", fn.Scaling.GetProportion())
			fmt.Fprintf(w, "\n")

			if fn.Scaling.GetZero() == "<not set>" || fn.Scaling.GetZero() == "false" {
				fmt.Fprintf(w, "- %s\t%s\n", "scale to zero", "disabled")
			} else {
				fmt.Fprintf(w, "- %s\t%s\n", "scale to zero", fn.Scaling.GetZero())
				fmt.Fprintf(w, "- %s\t%s\n", "scale to zero duration", fn.Scaling.GetZeroDuration())
			}
		}
	}

	fmt.Fprintf(w, "\nresources and limits\n\n")

	printResources(w, "- requests", fn.Requests)
	printResources(w, "- limits", fn.Limits)

	fmt.Fprintln(w)
	w.Flush()
	fmt.Print(b.String())
}

func printResources(w io.Writer, name string, resources *FunctionResources) {
	fmt.Fprintf(w, name+":")

	if resources.CPU == "0" && resources.Memory == "0" {
		fmt.Fprintln(w, "\t <none>")
		return
	}

	fmt.Fprintf(w, "\t RAM: %s CPU: %s\n", resources.GetMemory(), resources.GetCpu())

	return
}

func readFunctions(deps []v1.Deployment) []Function {

	var functions []Function

	for _, dep := range deps {
		function := Function{
			Name:     dep.Name,
			Timeout:  newTimeout(),
			Replicas: int(*dep.Spec.Replicas),
		}

		functionContainer := dep.Spec.Template.Spec.Containers[0]

		for _, env := range functionContainer.Env {
			if env.Name == "max_inflight" {
				maxInflight, err := strconv.Atoi(env.Value)
				if err == nil {
					function.MaxInflight = &maxInflight
				}
			}

			if env.Name == "read_timeout" {
				function.Timeout.ReadTimeout = env.Value
			}
			if env.Name == "write_timeout" {
				function.Timeout.WriteTimeout = env.Value
			}
			if env.Name == "exec_timeout" {
				function.Timeout.Additional["exec_timeout"] = env.Value
			}
		}

		labels := dep.Spec.Template.Labels
		scaleMax, ok := labels["com.openfaas.scale.max"]
		if ok {
			v, err := strconv.Atoi(scaleMax)
			if err == nil {
				if function.Scaling == nil {
					function.Scaling = &Scaling{}
				}
				function.Scaling.Max = &v
			}
		}
		scaleMin, ok := labels["com.openfaas.scale.min"]
		if ok {
			v, err := strconv.Atoi(scaleMin)
			if err == nil {
				if function.Scaling == nil {
					function.Scaling = &Scaling{}
				}
				function.Scaling.Min = &v
			}
		}
		scaleType, ok := labels["com.openfaas.scale.type"]
		if ok {
			if function.Scaling == nil {
				function.Scaling = &Scaling{}
			}
			function.Scaling.Type = scaleType
		}
		scaleTarget, ok := labels["com.openfaas.scale.target"]
		if ok {
			if function.Scaling == nil {
				function.Scaling = &Scaling{}
			}
			function.Scaling.Target = scaleTarget
		}
		scaleProportion, ok := labels["com.openfaas.scale.target-proportion"]
		if ok {
			if function.Scaling == nil {
				function.Scaling = &Scaling{}
			}
			function.Scaling.Proportion = scaleProportion
		}
		scaleZero, ok := labels["com.openfaas.scale.zero"]
		if ok {
			if function.Scaling == nil {
				function.Scaling = &Scaling{}
			}
			function.Scaling.Zero = scaleZero
		}
		scaleZeroDuration, ok := labels["com.openfaas.scale.zero-duration"]
		if ok {
			if function.Scaling == nil {
				function.Scaling = &Scaling{}
			}
			function.Scaling.ZeroDuration = scaleZeroDuration
		}

		req := &FunctionResources{
			Memory: functionContainer.Resources.Requests.Memory().String(),
			CPU:    functionContainer.Resources.Requests.Cpu().String(),
		}
		function.Requests = req

		lim := &FunctionResources{
			Memory: functionContainer.Resources.Limits.Memory().String(),
			CPU:    functionContainer.Resources.Limits.Cpu().String(),
		}
		function.Limits = lim

		function.ReadOnlyRootFilesystem = *functionContainer.SecurityContext.ReadOnlyRootFilesystem

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

func isProComponent(container corev1.Container) bool {
	return isProImage(container.Image) || hasLicenseMount(container)
}

func isProImage(imageName string) bool {
	return strings.Contains(imageName, "openfaasltd")
}

func hasLicenseMount(container corev1.Container) bool {
	for _, mount := range container.VolumeMounts {
		if mount.Name == "license" {
			return true
		}
	}

	return false
}
