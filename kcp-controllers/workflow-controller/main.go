package main

import (
	"context"
	"flag"
	"fmt"
	"kcp-controllers/workflow-controller/conductor"
	"os"
	"strings"
	"time"

	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	corev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"

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

// ─── Scheme ──────────────────────────────────────────────────────

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apisv1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1alpha1.AddToScheme(scheme))
}

// ─── GVKs ────────────────────────────────────────────────────────

var (
	workflowDefGVK = schema.GroupVersionKind{
		Group: "conductor.io", Version: "v1alpha1", Kind: "WorkflowDefinition",
	}
	workflowRunGVK = schema.GroupVersionKind{
		Group: "conductor.io", Version: "v1alpha1", Kind: "WorkflowRun",
	}
)

func main() {
	var (
		apiExportName string
		conductorURL  string
	)

	flag.StringVar(&apiExportName, "api-export-name", "order-fulfillment", "Name of the APIExport")
	flag.StringVar(&conductorURL, "conductor-url", "http://conductor-server:8080", "Conductor server URL")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	klog.InitFlags(nil)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("conductor-controller")

	cfg := ctrl.GetConfigOrDie()
	cc := conductor.NewClient(conductorURL)

	// ─── Provider + Manager ──────────────────────────────────────

	provider, err := apiexport.New(cfg, apiExportName, apiexport.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "Failed to create provider")
		os.Exit(1)
	}

	mgr, err := mcmanager.New(cfg, provider, manager.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "Failed to create manager")
		os.Exit(1)
	}

	// ─── WorkflowDefinition controller ───────────────────────────

	defObj := &unstructured.Unstructured{}
	defObj.SetGroupVersionKind(workflowDefGVK)

	err = mcbuilder.ControllerManagedBy(mgr).
		Named("workflowdefinition-controller").
		For(defObj).
		Complete(mcreconcile.Func(
			func(ctx context.Context, req mcreconcile.Request) (reconcile.Result, error) {
				return reconcileDefinition(ctx, mgr, cc, req)
			},
		))
	if err != nil {
		log.Error(err, "Failed to build WorkflowDefinition controller")
		os.Exit(1)
	}

	// ─── WorkflowRun controller ──────────────────────────────────

	runObj := &unstructured.Unstructured{}
	runObj.SetGroupVersionKind(workflowRunGVK)

	err = mcbuilder.ControllerManagedBy(mgr).
		Named("workflowrun-controller").
		For(runObj).
		Complete(mcreconcile.Func(
			func(ctx context.Context, req mcreconcile.Request) (reconcile.Result, error) {
				return reconcileRun(ctx, mgr, cc, req)
			},
		))
	if err != nil {
		log.Error(err, "Failed to build WorkflowRun controller")
		os.Exit(1)
	}

	// ─── Start ───────────────────────────────────────────────────

	log.Info("Starting conductor controller",
		"apiExport", apiExportName,
		"conductorURL", conductorURL,
	)

	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		log.Error(err, "Manager exited with error")
		os.Exit(1)
	}
}

// ═════════════════════════════════════════════════════════════════
// WorkflowDefinition Reconciler
// ═════════════════════════════════════════════════════════════════

func reconcileDefinition(
	ctx context.Context,
	mgr mcmanager.Manager,
	cc *conductor.Client,
	req mcreconcile.Request,
) (reconcile.Result, error) {
	log := ctrl.Log.WithName("workflowdefinition").WithValues(
		"cluster", req.Cluster(),
		"namespace", req.Namespace,
		"name", req.Name,
	)

	cl, err := mgr.GetCluster(ctx, req.Cluster())
	if err != nil {
		log.Error(err, "Failed to get cluster")
		return reconcile.Result{}, err
	}

	// Fetch the WorkflowDefinition
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(workflowDefGVK)
	if err := cl.GetClient().Get(ctx, req.NamespacedName, obj); err != nil {
		log.Info("WorkflowDefinition deleted or not found")
		return reconcile.Result{}, nil
	}

	// ── Generate tenant-scoped Conductor name ────────────────────
	conductorName := fmt.Sprintf("%s__%s__%s",
		req.Cluster(), req.Namespace, req.Name,
	)

	log.Info("Reconciling WorkflowDefinition", "conductorName", conductorName)

	// ── Convert KRM → Conductor JSON (passthrough) ───────────────
	conductorDef, err := conductor.ConvertDefinition(obj, conductorName)
	if err != nil {
		log.Error(err, "Failed to convert to Conductor format")
		updateStatus(obj, "Failed", conductorName, 0, err.Error())
		_ = cl.GetClient().Status().Update(ctx, obj)
		return reconcile.Result{}, nil
	}

	// ── Log what we're sending (for debugging) ───────────────────
	version := 1
	if v, ok := conductorDef["version"].(int); ok {
		version = v
	} else if v, ok := conductorDef["version"].(float64); ok {
		version = int(v)
	} else if v, ok := conductorDef["version"].(int64); ok {
		version = int(v)
	}

	tasks, _ := conductorDef["tasks"].([]interface{})
	log.Info("Registering in Conductor",
		"conductorName", conductorName,
		"version", version,
		"taskCount", len(tasks),
	)

	// ── Register in Conductor ────────────────────────────────────
	if err := cc.RegisterWorkflow(ctx, conductorDef); err != nil {
		log.Error(err, "Conductor registration failed")
		updateStatus(obj, "Failed", conductorName, version, err.Error())
		_ = cl.GetClient().Status().Update(ctx, obj)
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}

	log.Info("Registered successfully", "conductorName", conductorName, "version", version)

	updateStatus(obj, "Registered", conductorName, version, "")
	if err := cl.GetClient().Status().Update(ctx, obj); err != nil {
		log.Error(err, "Failed to update status")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// ═════════════════════════════════════════════════════════════════
// WorkflowRun Reconciler
// ═════════════════════════════════════════════════════════════════

func reconcileRun(
	ctx context.Context,
	mgr mcmanager.Manager,
	cc *conductor.Client,
	req mcreconcile.Request,
) (reconcile.Result, error) {
	log := ctrl.Log.WithName("workflowrun").WithValues(
		"cluster", req.Cluster(),
		"namespace", req.Namespace,
		"name", req.Name,
	)

	cl, err := mgr.GetCluster(ctx, req.Cluster())
	if err != nil {
		log.Error(err, "Failed to get cluster")
		return reconcile.Result{}, err
	}

	// Fetch the WorkflowRun
	run := &unstructured.Unstructured{}
	run.SetGroupVersionKind(workflowRunGVK)
	if err := cl.GetClient().Get(ctx, req.NamespacedName, run); err != nil {
		log.Info("WorkflowRun deleted or not found")
		return reconcile.Result{}, nil
	}

	// ── Already started? Poll for status. ────────────────────────
	existingId, _, _ := unstructured.NestedString(run.Object, "status", "conductorWorkflowId")
	if existingId != "" {
		return pollExecution(ctx, log, cl, cc, run, existingId)
	}

	// ── Look up the WorkflowDefinition ───────────────────────────
	defName, _, _ := unstructured.NestedString(run.Object, "spec", "workflowDefinition")
	if defName == "" {
		log.Error(nil, "Missing spec.workflowDefinition")
		return reconcile.Result{}, nil
	}

	defObj := &unstructured.Unstructured{}
	defObj.SetGroupVersionKind(workflowDefGVK)
	defKey := req.NamespacedName
	defKey.Name = defName

	if err := cl.GetClient().Get(ctx, defKey, defObj); err != nil {
		log.Error(err, "WorkflowDefinition not found", "definition", defName)
		setRunPhase(run, "Failed", "", "", fmt.Sprintf("WorkflowDefinition %q not found", defName))
		_ = cl.GetClient().Status().Update(ctx, run)
		return reconcile.Result{}, nil
	}

	// ── Check definition is registered ───────────────────────────
	conductorName, _, _ := unstructured.NestedString(defObj.Object, "status", "conductorName")
	defPhase, _, _ := unstructured.NestedString(defObj.Object, "status", "phase")

	if defPhase != "Registered" || conductorName == "" {
		log.Info("Definition not yet registered, retrying", "phase", defPhase)
		setRunPhase(run, "Pending", "", conductorName, "Waiting for WorkflowDefinition to be registered")
		_ = cl.GetClient().Status().Update(ctx, run)
		return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// ── Build start request (passthrough from spec) ──────────────
	startReq := map[string]interface{}{
		"name": conductorName,
	}

	// Pass through all spec fields that Conductor accepts
	if input, ok, _ := unstructured.NestedMap(run.Object, "spec", "input"); ok {
		startReq["input"] = input
	}
	if v, ok, _ := unstructured.NestedString(run.Object, "spec", "correlationId"); ok {
		startReq["correlationId"] = v
	}
	if v, ok, _ := unstructured.NestedInt64(run.Object, "spec", "priority"); ok {
		startReq["priority"] = v
	}
	if v, ok, _ := unstructured.NestedString(run.Object, "spec", "idempotencyKey"); ok {
		startReq["idempotencyKey"] = v
	}
	if v, ok, _ := unstructured.NestedString(run.Object, "spec", "idempotencyStrategy"); ok {
		startReq["idempotencyStrategy"] = v
	}
	if v, ok, _ := unstructured.NestedMap(run.Object, "spec", "taskToDomain"); ok {
		startReq["taskToDomain"] = v
	}

	// Version: from the run spec, or fall back to the definition's registered version
	if v, ok, _ := unstructured.NestedInt64(run.Object, "spec", "version"); ok && v > 0 {
		startReq["version"] = v
	} else if v, ok, _ := unstructured.NestedInt64(defObj.Object, "status", "conductorVersion"); ok {
		startReq["version"] = v
	}

	// ── Start the execution ──────────────────────────────────────
	log.Info("Starting Conductor workflow", "conductorName", conductorName)

	workflowId, err := cc.StartWorkflow(ctx, startReq)
	if err != nil {
		log.Error(err, "Failed to start workflow")
		setRunPhase(run, "Failed", "", conductorName, fmt.Sprintf("Start failed: %v", err))
		_ = cl.GetClient().Status().Update(ctx, run)
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}

	log.Info("Started successfully", "workflowId", workflowId)

	setRunPhase(run, "Running", workflowId, conductorName, "Execution started")
	if err := cl.GetClient().Status().Update(ctx, run); err != nil {
		log.Error(err, "Failed to update status")
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
}

// ─── Status polling ──────────────────────────────────────────────

func pollExecution(
	ctx context.Context,
	log interface {
		Info(string, ...interface{})
		Error(error, string, ...interface{})
	},
	cl interface {
		GetClient() interface {
			Status() interface {
				Update(context.Context, ...interface{}) error
			}
		}
	},
	cc *conductor.Client,
	run *unstructured.Unstructured,
	workflowId string,
) (reconcile.Result, error) {
	exec, err := cc.GetExecution(ctx, workflowId)
	if err != nil {
		// Transient error — retry
		return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Map Conductor status
	conductorStatus, _ := exec["status"].(string)
	phase := mapConductorStatus(conductorStatus)

	// Build task statuses from Conductor response
	var taskStatuses []interface{}
	var currentTask string
	if tasks, ok := exec["tasks"].([]interface{}); ok {
		for _, taskRaw := range tasks {
			taskMap, ok := taskRaw.(map[string]interface{})
			if !ok {
				continue
			}
			taskStatus := map[string]interface{}{
				"taskReferenceName": taskMap["referenceTaskName"],
				"taskType":          taskMap["taskType"],
				"status":            taskMap["status"],
			}
			if retryCount, ok := taskMap["retryCount"]; ok {
				taskStatus["retryCount"] = retryCount
			}
			if startTime, ok := taskMap["startTime"].(float64); ok && startTime > 0 {
				taskStatus["startTime"] = time.UnixMilli(int64(startTime)).Format(time.RFC3339)
			}
			if endTime, ok := taskMap["endTime"].(float64); ok && endTime > 0 {
				taskStatus["endTime"] = time.UnixMilli(int64(endTime)).Format(time.RFC3339)
			}
			taskStatuses = append(taskStatuses, taskStatus)

			if s, _ := taskMap["status"].(string); s == "IN_PROGRESS" || s == "SCHEDULED" {
				currentTask, _ = taskMap["referenceTaskName"].(string)
			}
		}
	}

	// Update status
	status := map[string]interface{}{
		"phase":                 phase,
		"conductorWorkflowId":   workflowId,
		"currentTask":           currentTask,
		"taskStatuses":          taskStatuses,
		"reasonForIncompletion": exec["reasonForIncompletion"],
	}

	// Preserve conductorWorkflowName from previous update
	if name, ok, _ := unstructured.NestedString(run.Object, "status", "conductorWorkflowName"); ok {
		status["conductorWorkflowName"] = name
	}

	if startTime, ok := exec["startTime"].(float64); ok && startTime > 0 {
		status["startTime"] = time.UnixMilli(int64(startTime)).Format(time.RFC3339)
	}
	if endTime, ok := exec["endTime"].(float64); ok && endTime > 0 {
		status["endTime"] = time.UnixMilli(int64(endTime)).Format(time.RFC3339)
	}
	if output, ok := exec["output"].(map[string]interface{}); ok {
		status["output"] = output
	}

	run.Object["status"] = status

	// NOTE: actual status update call
	// _ = cl.GetClient().Status().Update(ctx, run)

	// Still running? Keep polling.
	if phase == "Running" || phase == "Paused" {
		return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Terminal state — done
	return reconcile.Result{}, nil
}

// ─── Helpers ─────────────────────────────────────────────────────

func updateStatus(obj *unstructured.Unstructured, phase, conductorName string, version int, message string) {
	status := map[string]interface{}{
		"phase":            phase,
		"conductorName":    conductorName,
		"conductorVersion": int64(version),
		"message":          message,
	}
	if phase == "Registered" {
		status["lastRegisteredAt"] = time.Now().Format(time.RFC3339)
		status["message"] = "Successfully registered in Conductor"
	}
	obj.Object["status"] = status
}

func setRunPhase(run *unstructured.Unstructured, phase, workflowId, conductorName, message string) {
	status := map[string]interface{}{
		"phase":   phase,
		"message": message,
	}
	if workflowId != "" {
		status["conductorWorkflowId"] = workflowId
	}
	if conductorName != "" {
		status["conductorWorkflowName"] = conductorName
	}
	if phase == "Running" {
		status["startTime"] = time.Now().Format(time.RFC3339)
	}
	run.Object["status"] = status
}

func mapConductorStatus(status string) string {
	switch strings.ToUpper(status) {
	case "RUNNING":
		return "Running"
	case "COMPLETED":
		return "Completed"
	case "FAILED":
		return "Failed"
	case "TIMED_OUT":
		return "TimedOut"
	case "TERMINATED":
		return "Terminated"
	case "PAUSED":
		return "Paused"
	default:
		return "Pending"
	}
}
