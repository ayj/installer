package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sync/errgroup"
)

var (
	templateDir string
	valuesDir   string
	workDir     string
	helmOutDir  string
)

func init() {
	templateDir = os.Getenv("TEMPLATE_DIR")
	if templateDir == "" {
		templateDir = "/var/run/istio-templates"
	}

	valuesDir = os.Getenv("VALUES_DIR")
	if valuesDir == "" {
		valuesDir = "/var/run/istio-values"
	}

	workDir = os.Getenv("WORK_DIR")
	if workDir == "" {
		workDir = "/tmp/workdir"
	}

	helmOutDir = os.Getenv("HELM_OUT")
	if helmOutDir == "" {
		helmOutDir = path.Join(workDir, "helm-out")
	}
}

type component struct {
	name        string
	chartPath   string
	skipInstall bool
}

var (
	tarballs = []string{
		"crds.tar.gz",
		"gateways.tar.gz",
		"istio-cni.tar.gz",
		"istio-policy.tar.gz",
		"istiocoredns.tar.gz",
		"istio-control.tar.gz",
		"istio-telemetry.tar.gz",
		"security.tar.gz",
	}

	components = []component{
		{name: "citadel", chartPath: "security/citadel"},
		{name: "nodeagent", chartPath: "security/nodeagent", skipInstall: true},
		{name: "certmanager", chartPath: "security/certmanager"},

		{name: "istio-config", chartPath: "istio-control/istio-config"},
		{name: "istio-discovery", chartPath: "istio-control/istio-discovery"},
		{name: "istio-autoinject", chartPath: "istio-control/istio-autoinject"},

		{name: "istio-ingress", chartPath: "gateways/istio-ingress"},
		{name: "istio-egress", chartPath: "gateways/istio-egress"},

		{name: "istio-policy", chartPath: "istio-policy"},

		{name: "grafana", chartPath: "istio-telemetry/grafana"},
		{name: "kiali", chartPath: "istio-telemetry/kiali"},
		{name: "mixer-telemetry", chartPath: "istio-telemetry/mixer-telemetry"},
		{name: "prometheus", chartPath: "istio-telemetry/prometheus"},
		{name: "prometheus-operator", chartPath: "istio-telemetry/prometheus-operator", skipInstall: true},
		{name: "tracing", chartPath: "istio-telemetry/tracing", skipInstall: true},

		{name: "istiocoredns", chartPath: "istiocoredns"},
		{name: "istio-cni", chartPath: "istio-cni", skipInstall: true},
	}

	chartCRDs = "crds"
	namespace = "istio-system"
	// relative to the templateDir
	globalDefaultValues = "global.yaml"
)

func reconcileCRDs() error {
	fmt.Printf("Reconciling CRDs ...\n")
	args := []string{
		"kubectl",
		"apply",
		"-f",
		path.Join(workDir, chartCRDs, "files"),
	}
	if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to apply %v: %v %v", chartCRDs, err, string(out))
	}
	return nil
}

func extractCharts() error {
	// extract charts from configmap
	for _, tarball := range tarballs {
		args := []string{
			"tar",
			"xf",
			path.Join(templateDir, tarball),
			"-C",
			workDir,
		}
		if _, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("failed to extract %v: %v", tarball, err)
		}
	}
	return nil
}

func renderComponents() error {
	globalYAML := path.Join(templateDir, globalDefaultValues)
	valuesYAML := path.Join(valuesDir, "values.yaml")

	if _, err := os.Stat(valuesYAML); os.IsNotExist(err) {
		valuesYAML = ""
	}

	for _, component := range components {
		if component.skipInstall {
			continue
		}

		fmt.Printf("Rendering %v ...\n", component.chartPath)
		args := []string{
			"helm",
			"template",
			"--namespace",
			namespace,
			"-n",
			component.name,
			path.Join(workDir, component.chartPath),
			"-f",
			globalYAML,
			"--output-dir",
			helmOutDir,
		}
		if valuesYAML != "" {
			args = append(args, "-f", valuesYAML)
		}
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("failed render manifests %v: %v %v", component.name, err, string(out))
		}
	}
	return nil
}

func reconcileComponents() error {
	args := []string{
		"kubectl",
		"get",
		"namespace",
		namespace,
	}
	if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil || strings.Contains(string(out), "not found") {
		args := []string{
			"kubectl",
			"create",
			"namespace",
			namespace,
		}
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to create %v namespace: %v %v", namespace, err, string(out))
		}
	}

	var g errgroup.Group

	for _, component := range components {
		if component.skipInstall {
			continue
		}

		name := component.name
		g.Go(func() error {
			fmt.Printf("Reconciling %v ...\n", name)
			args := []string{
				"kubectl",
				"apply",
				"-f",
				path.Join(helmOutDir, name, "templates"),
				"--prune",
				"--selector",
				"release=" + name,
			}
			if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
				return fmt.Errorf("failed apply manifests %v: %v %v", name, err, string(out))
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	for _, component := range components {
		if component.skipInstall {
			continue
		}

		name := component.name
		g.Go(func() error {
			fmt.Printf("Checking status %v ...\n", name)

			deployment := path.Join(helmOutDir, name, "templates", "deployment.yaml")
			if _, err := os.Stat(deployment); err != nil || os.IsNotExist(err) {
				return nil
			}

			args := []string{
				"kubectl",
				"rollout",
				"status",
				"-f",
				deployment,
			}
			if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
				return fmt.Errorf("failed waiting for %s: %v %v", name, err, string(out))
			}
			fmt.Printf("%v is ready\n", name)
			return nil
		})
	}
	return g.Wait()
}

func main() {
	fmt.Println("Starting istio-operator")

	fmt.Printf("templateDir=%v\n", templateDir)
	fmt.Printf("valuesDir=%v\n", valuesDir)
	fmt.Printf("workDir=%v\n", workDir)

	// setup working directories
	if err := os.MkdirAll(workDir, os.ModePerm); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(helmOutDir, os.ModePerm); err != nil {
		log.Fatal(err)
	}

	if err := extractCharts(); err != nil {
		log.Fatal(err)
	}
	if err := renderComponents(); err != nil {
		log.Fatal(err)
	}
	if err := reconcileCRDs(); err != nil {
		log.Fatal(err)
	}
	if err := reconcileComponents(); err != nil {
		log.Fatal(err)
	}

	// basic reconcile loop; minimal error handling
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()
	if err := watcher.Add(templateDir); err != nil {
		log.Fatal(err)
	}
	if err := watcher.Add(valuesDir); err != nil {
		log.Fatal(err)
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			log.Println("event:", event)

			if err := extractCharts(); err != nil {
				log.Fatal(err)
			}
			if err := renderComponents(); err != nil {
				log.Fatal(err)
			}
			if err := reconcileCRDs(); err != nil {
				log.Fatal(err)
			}
			if err := reconcileComponents(); err != nil {
				log.Fatal(err)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Println("error:", err)
		case <-time.After(time.Minute):
			log.Println("Performing periodic reconcile")
			if err := reconcileCRDs(); err != nil {
				log.Fatal(err)
			}
			if err := reconcileComponents(); err != nil {
				log.Fatal(err)
			}
		}
	}
}
