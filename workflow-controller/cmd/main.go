package main

import (
	"context"
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/kcp-dev/multicluster-provider/apiexport"
)

// ─── Workflow GVK ────────────────────────────────────────────────
// Since we don't have generated Go types for our custom Workflow
// resource, we use unstructured.Unstructured with the GVK set.
// This is the simplest approach for a logging-only controller to prove the concept.

var workflowGVK = schema.GroupVersionKind{
	Group:   "example.com",
	Version: "v1alpha1",
	Kind:    "Workflow",
}

var workflowGVR = schema.GroupVersionResource{
	Group:    "example.com",
	Version:  "v1alpha1",
	Resource: "workflows",
}

func main() {
	var (
		kubeconfig    string
		apiExportName string
	)

	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kcp kubeconfig (points to the provider workspace)")
	flag.StringVar(&apiExportName, "api-export-name", "workflows", "Name of the APIExport to watch")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	klog.InitFlags(nil)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("workflow-controller")

	var cfg *rest.Config
	var err error

	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		log.Error(err, "Failed to build kubeconfig")
		os.Exit(1)
	}

	// ─── Create the kcp APIExport provider ───────────────────────
	//
	// This provider:
	// 1. Watches the APIExportEndpointSlice for your APIExport
	// 2. Discovers VW URLs (one per shard)
	// 3. Automatically connects to each shard's VW endpoint
	// 4. Surfaces Workflow resources from ALL consumer workspaces
	//    across all shards through a single reconcile loop
	//
	// When a new shard is added, the provider detects the new
	// endpoint and starts watching it automatically.

	provider, err := apiexport.New(
		cfg,
		apiExportName,
		apiexport.Options{},
	)
	if err != nil {
		log.Error(err, "Failed to create apiexport provider")
		os.Exit(1)
	}

	// ─── Create the multicluster manager ─────────────────────────
	//
	// This wraps controller-runtime's manager with multi-cluster
	// awareness. The provider tells it which clusters (workspaces)
	// exist and how to reach them.

	mgr, err := mcmanager.New(cfg, provider, manager.Options{
		// Standard controller-runtime manager options
		// HealthProbeBindAddress: ":8081",
		// MetricsBindAddress:    ":8080",
	})
	if err != nil {
		log.Error(err, "Failed to create manager")
		os.Exit(1)
	}

	// ─── Register the Workflow reconciler ─────────────────────────
	//
	// We use unstructured.Unstructured because we don't have
	// generated Go types for Workflow. This is fine for a
	// logging controller and common in kcp controllers that
	// work with dynamic CRDs.
	//
	// The mcbuilder.ControllerManagedBy wires everything together:
	// - Watches Workflow resources across all consumer workspaces
	// - Routes reconcile events to our WorkflowReconciler
	// - The reconcile.Request includes cluster information so
	//   we know which workspace the event came from

	workflowObj := &unstructured.Unstructured{}
	workflowObj.SetGroupVersionKind(workflowGVK)

	err = mcbuilder.ControllerManagedBy(mgr).
		Named("workflow-controller").
		For(workflowObj).
		Complete(mcreconcile.Func(
			func(ctx context.Context, req mcreconcile.Request) (reconcile.Result, error) {
				return reconcileWorkflow(ctx, mgr, req)
			},
		))
	if err != nil {
		log.Error(err, "Failed to build controller")
		os.Exit(1)
	}

	// ─── Start ───────────────────────────────────────────────────
	log.Info("Starting workflow controller",
		"apiExport", apiExportName,
	)

	ctx := signals.SetupSignalHandler()
	if err := mgr.Start(ctx); err != nil {
		log.Error(err, "Manager exited with error")
		os.Exit(1)
	}
}

// ─── Reconciler ──────────────────────────────────────────────────
//
// This function is called every time a Workflow resource is created,
// updated, or deleted in ANY consumer workspace that has bound to
// your APIExport.
//
// The mcreconcile.Request contains:
// - req.ClusterName: which workspace the event came from
// - req.Name: the Workflow's name
// - req.Namespace: the Workflow's namespace
//
// For now, we just log everything. In production, this is where
// you'd implement your workflow execution logic.

func reconcileWorkflow(
	ctx context.Context,
	mgr mcmanager.Manager,
	req mcreconcile.Request,
) (reconcile.Result, error) {
	log := ctrl.Log.WithName("workflow-controller").WithValues(
		"cluster", req.ClusterName,
		"namespace", req.Namespace,
		"name", req.Name,
	)

	// Get a client scoped to the specific workspace (cluster)
	cl, err := mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		log.Error(err, "Failed to get cluster client")
		return reconcile.Result{}, err
	}

	// Fetch the Workflow object
	workflow := &unstructured.Unstructured{}
	workflow.SetGroupVersionKind(workflowGVK)

	err = cl.GetClient().Get(ctx, req.NamespacedName, workflow)
	if err != nil {
		log.Error(err, "Failed to get Workflow")
		// If it's deleted, just log and return
		return reconcile.Result{}, nil
	}

	// ─── Log the Workflow details ────────────────────────────────

	spec, _, _ := unstructured.NestedMap(workflow.Object, "spec")
	status, _, _ := unstructured.NestedMap(workflow.Object, "status")

	description, _, _ := unstructured.NestedString(workflow.Object, "spec", "description")
	steps, _, _ := unstructured.NestedSlice(workflow.Object, "spec", "steps")

	log.Info("Reconciling Workflow",
		"description", description,
		"stepCount", len(steps),
		"spec", spec,
		"status", status,
		"resourceVersion", workflow.GetResourceVersion(),
		"creationTimestamp", workflow.GetCreationTimestamp(),
	)

	// Log each step
	for i, step := range steps {
		stepMap, ok := step.(map[string]interface{})
		if !ok {
			continue
		}
		stepName, _, _ := unstructured.NestedString(stepMap, "name")
		log.Info("  Step",
			"index", i,
			"name", stepName,
		)
	}

	// ─── Your business logic goes here ───────────────────────────
	//
	// Examples of what you'd do in production:
	//
	// 1. Validate the workflow steps
	// 2. Create execution pods on a workload cluster
	// 3. Update the Workflow status:
	//
	//    workflow.Object["status"] = map[string]interface{}{
	//        "phase": "Running",
	//        "startTime": time.Now().Format(time.RFC3339),
	//    }
	//    err = cl.GetClient().Status().Update(ctx, workflow)

	return reconcile.Result{}, nil
}
