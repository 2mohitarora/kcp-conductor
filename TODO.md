# KCP + Conductor Improvements Plan

This document outlines the planned architecture, security, scalability, and code quality improvements for the KCP + Netflix Conductor workflow orchestration setup.

## 1. Controller Optimizations & Scalability

* **Migrate to Event-Driven Execution**
  * **Issue**: The `reconcileRun` function actively polls Conductor for workflow status every 5 seconds (`RequeueAfter: 5 * time.Second`). With thousands of concurrent workflows across many tenants, this will overwhelm KCP’s API server with Etcd update events and bombard Conductor with read queries.
  * **Action**: Implement a webhook receiver alongside the controller that listens to Conductor execution events and patches the K8s `WorkflowRun` asynchronously, reducing the controller's active reconciliation to just the initial "Start" operation.
* **Reduce State Size in etcd**
  * **Issue**: Storing the exact state of all tasks directly in the `WorkflowRun` status (`taskStatuses: []interface{}`) will cause workflows with many dynamically generated tasks (e.g., `FORK` or `DYNAMIC` tasks) to exceed Kubernetes' hard 1.5MB etcd object limit.
  * **Action**: Sync only high-level summary metadata (Phase, Start/End times, correlation IDs) back to the CRD status. Leave the granular execution trace in Conductor, and potentially expose it via a custom subresource or a lightweight read-only UI for the tenant.

## 2. Resilience and Error Handling

* **Enforce Idempotency in Conductor Starts**
  * **Issue**: If the call to `StartWorkflow` succeeds but KCP fails to update the local `obj.Status` (due to K8s API server jitter or conflicts), the controller retries and accidentally starts a *second* workflow execution in Conductor. 
  * **Action**: Leverage Conductor runtime parameters such as `idempotencyKey` when starting the workflow. Map the `WorkflowRun` `metadata.uid` as the idempotency key to guarantee exactly-once execution.
* **Add Backoff Strategy for API Clashes**
  * **Issue**: The controller loops constantly during workflow execution, network flakiness to Conductor could spin controller loops out of control. 
  * **Action**: Incorporate exponential backoffs (`workqueue.DefaultControllerRateLimiter()`) rather than hardcoded `RequeueAfter` delays.

## 3. CRD Design & KRM Patterns

* **Adopt Structured Go Types**
  * **Issue**: The controller heavily uses `unstructured.Unstructured` to interact with KCP, sacrificing Go type-safety.
  * **Action**: Generate proper Go structs using `kubebuilder`/`code-generator` for the `workflows.com/v1alpha1` group to allow scale and maintainability during development.
* **Enforce CRD Schema Validation (OpenAPI v3)**
  * **Issue**: `WorkflowDefinition` currently permits any JSON structure that Conductor accepts.
  * **Action**: Add strict OpenAPI v3 schema block definitions inside standard KCP CRD definitions. This ensures malformed JSON stops at the edge and prevents bad inputs from crashing the controller map routines.

## 4. Deployment & Infrastructure Setup

* **Upgrade Storage Backend**
  * **Issue**: The manifest currently sets up Conductor backed by Redis + Elasticsearch. Conductor natively connects to Postgres or Cassandra for durable high scale.
  * **Action**: Ensure the storage backing Conductor is designed for substantial load and correctly supports massive parallel workflows, likely moving to a Postgres or Cassandra cluster instead of Redis.
* **Refactor Bootstrapping Trust**
  * **Issue**: Using bash scripts (`sed '/namespace:/d'`) to extract CA certs in `01-etcd-ca-issuer.yaml` is brittle for automated GitOps pipelines.
  * **Action**: Utilize native tools such as `cert-manager`'s `Trust` resource to securely and automatically synchronize CA certs across namespaces.
