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
	"time"

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

	directFunctions := false
	probeFunctions := false
	clusterRole := false

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
						if env.Name == "probe_functions" {
							probeFunctions, err = strconv.ParseBool(env.Value)
							if err != nil {
								log.Fatalf("Error parsing probe_functions: %v, value: %s", err, env.Value)
							}
						}
						if env.Name == "cluster_role" {
							clusterRole, err = strconv.ParseBool(env.Value)
							if err != nil {
								log.Fatalf("Error parsing cluster_role: %v, value: %s", err, env.Value)
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

	namespaces, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		panic(err)
	}

	istioDetected := false
	functionNamespaces := []string{"openfaas-fn"}

	for _, n := range namespaces.Items {
		if n.Name == "istio-system" {
			istioDetected = true
		}

		if _, ok := n.Annotations["openfaas"]; ok {
			functionNamespaces = append(functionNamespaces, n.Name)
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
	fmt.Printf("gateway_timeout - read: %s write: %s upstream: %s\n", gatewayTimeout.ReadTimeout, gatewayTimeout.WriteTimeout, gatewayTimeout.Additional["upstream_timeout"])
	fmt.Printf("controller_mode: %s\n", controllerMode)
	fmt.Printf("controller_timeout - read: %s write: %s\n", controllerTimeout.ReadTimeout, controllerTimeout.WriteTimeout)

	fmt.Printf("\nQueue-worker\n\n")

	fmt.Printf("queue_worker_image: %s\n", queueWorkerImage)
	fmt.Printf("queue_worker_replicas: %d\n", queueWorkerReplicas)
	fmt.Printf("queue_worker_ack_wait: %s\n", queueWorkerAckWait)
	fmt.Printf("queue_worker_max_inflight: %d\n", queueWorkerMaxInflight)
	fmt.Printf("\n")
	fmt.Printf("\nFunction namespaces: %v\n\n", strings.TrimRight(strings.Join(functionNamespaces, ", "), ","))

	if len(autoscalerImage) > 0 {
		fmt.Printf("\nautoscaler\n\n")

		fmt.Printf("autoscaler_image: %s", autoscalerImage)
	}

	if len(dashboardImage) > 0 {
		fmt.Printf("\ndashboard\n\n")

		fmt.Printf("dashboard_image: %s", dashboardImage)
	}

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
	gwHAIcon := "❌"
	if gatewayReplicas >= 3 {
		gwHAIcon = "✅"
	}
	istioIcon := "❌"
	if istioDetected {
		istioIcon = "✅"
	}

	fmt.Printf(`
Features detected:

- %s Pro gateway
- %s HA Gateway
- %s Operator mode
- %s Autoscaler
- %s Dashboard
- %s Istio

`, proGatewayIcon, gwHAIcon, operatorIcon, autoscalerIcon, dashboardIcon, istioIcon)

	fmt.Printf(`Other:

- Kubernetes version: %s
- Asynchronous concurrency (cluster): %d
`, k8sVer,
		(queueWorkerReplicas * queueWorkerMaxInflight))

	fmt.Printf("\n")

	fmt.Printf("\nFunctions in (openfaas-fn):\n\n")

	if len(functions) == 0 {
		fmt.Printf("None detected\n")
	}

	for _, fn := range functions {
		printFunction(fn, len(autoscalerImage) > 0)
	}

	fmt.Printf("\nWarnings:\n\n")
	ackWaitDuration, err := time.ParseDuration(queueWorkerAckWait)
	if err != nil {
		log.Fatalf("unable to parse queue-worker ack_wait: %s", err)
	}

	gwUpstreamTimeout, err := gatewayTimeout.GetAdditionalTimeout("upstream_timeout")
	if err != nil {
		log.Fatalf("unable to parse upstream_timeout: %s", err)
	}

	if ackWaitDuration > gwUpstreamTimeout {
		fmt.Printf("⚠️ queue-worker ack_wait (%s) must be <= gateway.upstream_timeout (%s)\n", queueWorkerAckWait, gwUpstreamTimeout)
	}

	if (queueWorkerReplicas * queueWorkerMaxInflight) < 100 {
		fmt.Printf("⚠️ queue-worker maximum concurrency is (%d), this may be too low\n", queueWorkerMaxInflight*queueWorkerReplicas)
	}

	if gatewayReplicas < 3 {
		fmt.Printf("⚠️ gateway replicas want >= %d but got %d, (not Highly Available (HA))\n", 3, gatewayReplicas)
	}

	if queueWorkerReplicas < 3 {
		fmt.Printf("⚠️ queue-worker replicas want >= %d but got %d, (not Highly Available (HA))\n", 3, queueWorkerReplicas)
	}

	if istioDetected && directFunctions == false {
		fmt.Printf("⚠️ Istio detected, but direct_functions is disabled\n")
	}

	if istioDetected && probeFunctions == false {
		fmt.Printf("⚠️ Istio detected, but probe_functions is disabled\n")
	}

	if len(autoscalerImage) > 0 && clusterRole == false {
		fmt.Printf("⚠️ Autoscaler detected, but cluster_role is disabled - unable to collect CPU/RAM metrics\n")
	}

	if strings.Contains(gatewayImage, "openfaasltd") && len(autoscalerImage) == 0 {
		fmt.Printf("⚠️ Pro gateway detected, but autoscaler is not enabled\n")
	}

	for _, fn := range functions {
		if len(fn.Timeout.ReadTimeout) == 0 {
			fmt.Printf("⚠️ %s read_timeout is not set\n", fn.Name)
		} else if fn.Timeout.GetReadTimeout() > gwUpstreamTimeout {
			fmt.Printf("⚠️ function %s read_timeout (%s) is greater than gateway.upstream_timeout (%s)\n", fn.Name, fn.Timeout.ReadTimeout, gwUpstreamTimeout)
		}

		if len(fn.Timeout.WriteTimeout) == 0 {
			fmt.Printf("⚠️ %s write_timeout is not set\n", fn.Name)
		} else if fn.Timeout.GetWriteTimeout() > gwUpstreamTimeout {
			fmt.Printf("⚠️ function %s write_timeout (%s) is greater than gateway.upstream_timeout (%s)\n", fn.Name, fn.Timeout.WriteTimeout, gwUpstreamTimeout)
		}

		execTimeout, err := fn.Timeout.GetAdditionalTimeout("exec_timeout")
		if err != nil {
			fmt.Printf("⚠️ %s exec_timeout is not set\n", fn.Name)
		} else if execTimeout > gwUpstreamTimeout {
			fmt.Printf("⚠️ function %s exec_timeout (%s) is greater than gateway.upstream_timeout (%s)\n", fn.Name, execTimeout, gwUpstreamTimeout)
		}
	}

}

func printFunction(fn Function, autoscaling bool) {
	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 1, ' ', 0)
	fmt.Fprintf(w, "%s\t(%d replicas)\n\n", fn.Name, fn.Replicas)

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
			fmt.Fprintf(w, "\nNo scaling configuration was set\n")
		} else {
			fmt.Fprintf(w, "\n- %s\t%s\n", "scaling min/max", fmt.Sprintf("(%s / %s)", fn.Scaling.GetMin(), fn.Scaling.GetMax()))
			fmt.Fprintf(w, "- %s\t%s\n", "scaling type", fn.Scaling.GetType())
			fmt.Fprintf(w, "- %s\t%s\n", "scaling target", fn.Scaling.GetTarget())
			fmt.Fprintf(w, "- %s\t%s\n", "scaling target-proportion", fn.Scaling.GetProportion())
			fmt.Fprintf(w, "- %s\t%s\n", "scaling zero", fn.Scaling.GetZero())
			fmt.Fprintf(w, "- %s\t%s\n", "scaling zero-duration", fn.Scaling.GetZeroDuration())
		}
	}

	fmt.Fprintln(w)
	w.Flush()
	fmt.Print(b.String())
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
