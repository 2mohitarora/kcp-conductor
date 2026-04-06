package main

import (
	"context"
	"flag"
	"os"

	apisv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	corev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
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

// ─── Scheme registration ─────────────────────────────────────────
// Register kcp API types so controller-runtime knows about
// APIExportEndpointSlice, LogicalCluster, etc.

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apisv1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1alpha1.AddToScheme(scheme))
}

// ─── Workflow GVK ────────────────────────────────────────────────
// Since we don't have generated Go types for our custom Workflow
// resource, we use unstructured.Unstructured with the GVK set.

var workflowGVK = schema.GroupVersionKind{
	Group:   "example.com",
	Version: "v1alpha1",
	Kind:    "Workflow",
}

func main() {

	var apiExportName string
	flag.StringVar(&apiExportName, "api-export-name", "workflows", "Name of the APIExport to watch")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	klog.InitFlags(nil)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("workflow-controller")

	// Use controller-runtime's built-in kubeconfig resolution
	cfg := ctrl.GetConfigOrDie()

	// ─── Create the kcp APIExport provider ───────────────────────
	//
	// This provider:
	// 1. Watches the APIExportEndpointSlice for your APIExport
	// 2. Discovers VW URLs (one per shard)
	// 3. Automatically connects to each shard's VW endpoint
	// 4. Surfaces Workflow resources from ALL consumer workspaces
	//    across all shards through a single reconcile loop

	provider, err := apiexport.New(
		cfg,
		apiExportName,
		apiexport.Options{
			Scheme: scheme,
		},
	)
	if err != nil {
		log.Error(err, "Failed to create apiexport provider")
		os.Exit(1)
	}

	// ─── Create the multicluster manager ─────────────────────────

	mgr, err := mcmanager.New(cfg, provider, manager.Options{
		Scheme: scheme,
	})
	if err != nil {
		log.Error(err, "Failed to create manager")
		os.Exit(1)
	}

	// ─── Register the Workflow reconciler ─────────────────────────

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

func reconcileWorkflow(
	ctx context.Context,
	mgr mcmanager.Manager,
	req mcreconcile.Request,
) (reconcile.Result, error) {
	log := ctrl.Log.WithName("workflow-controller").WithValues(
		"cluster", req.Cluster(),
		"namespace", req.Namespace,
		"name", req.Name,
	)

	// Get a client scoped to the specific workspace (cluster)
	cl, err := mgr.GetCluster(ctx, req.Cluster())
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

	// ─── Final logic goes here ───────────────────────────
	//
	// Examples of what you'd do in production:
	//
	// 1. Validate the workflow steps
	// 2. Call conductor API to create conductor workflow
	// 3. Update the Workflow status:
	//
	//    workflow.Object["status"] = map[string]interface{}{
	//        "phase": "Running",
	//        "startTime": time.Now().Format(time.RFC3339),
	//    }
	//    err = cl.GetClient().Status().Update(ctx, workflow)

	return reconcile.Result{}, nil
}
