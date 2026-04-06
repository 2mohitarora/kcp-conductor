package conductor

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ConvertDefinition transforms a KRM WorkflowDefinition into a Conductor
// workflow definition JSON. It uses a passthrough approach:
//
//   - All fields are forwarded as-is to Conductor
//   - Only fields where KRM and Conductor conventions differ are transformed
//   - Unknown fields pass through untouched — nothing is dropped
//
// This ensures 100% Conductor compatibility regardless of Conductor version.
func ConvertDefinition(obj *unstructured.Unstructured, conductorName string) (map[string]interface{}, error) {
	spec, ok, _ := unstructured.NestedMap(obj.Object, "spec")
	if !ok {
		return nil, fmt.Errorf("missing spec")
	}

	// Start with a copy of the entire spec — nothing is lost
	def := deepCopyMap(spec)

	// Override the name with our tenant-scoped Conductor name
	def["name"] = conductorName

	// Ensure schemaVersion is set (Conductor requires this)
	if _, ok := def["schemaVersion"]; !ok {
		def["schemaVersion"] = 2
	}

	// Default version
	if _, ok := def["version"]; !ok {
		def["version"] = 1
	}

	// Transform tasks (recursive — handles nested FORK_JOIN, SWITCH, etc.)
	if tasksRaw, ok := def["tasks"].([]interface{}); ok {
		transformed, err := transformTasks(tasksRaw)
		if err != nil {
			return nil, fmt.Errorf("transforming tasks: %w", err)
		}
		def["tasks"] = transformed
	}

	return def, nil
}

// transformTasks recursively processes a list of tasks, applying only
// the minimal transformations where KRM format differs from Conductor.
func transformTasks(tasksRaw []interface{}) ([]interface{}, error) {
	var result []interface{}

	for i, taskRaw := range tasksRaw {
		taskMap, ok := taskRaw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("task[%d] is not an object", i)
		}

		transformed, err := transformTask(taskMap)
		if err != nil {
			name, _ := taskMap["name"].(string)
			return nil, fmt.Errorf("task[%d] (%s): %w", i, name, err)
		}
		result = append(result, transformed)
	}

	return result, nil
}

// transformTask applies minimal transformations to a single task.
// Only two things are transformed:
//
//  1. KRM `http` field → Conductor `inputParameters.http_request`
//  2. KRM `wait` field → Conductor `inputParameters.duration/until`
//
// Everything else passes through as-is.
func transformTask(taskMap map[string]interface{}) (map[string]interface{}, error) {
	task := deepCopyMap(taskMap)
	taskType, _ := task["type"].(string)

	// ─── Transform 1: http → inputParameters.http_request ────────
	//
	// In KRM, users write:
	//   http:
	//     uri: https://...
	//     method: POST
	//
	// Conductor expects:
	//   inputParameters:
	//     http_request:
	//       uri: https://...
	//       method: POST

	if httpConfig, ok := task["http"]; ok && taskType == "HTTP" {
		inputParams := getOrCreateMap(task, "inputParameters")
		inputParams["http_request"] = httpConfig
		task["inputParameters"] = inputParams
		delete(task, "http") // Remove KRM-only field
	}

	// ─── Transform 2: wait → inputParameters.duration/until ──────
	//
	// In KRM, users write:
	//   wait:
	//     duration: "30s"
	//
	// Conductor expects:
	//   inputParameters:
	//     duration: "30s"

	if waitConfig, ok := task["wait"].(map[string]interface{}); ok && taskType == "WAIT" {
		inputParams := getOrCreateMap(task, "inputParameters")
		if duration, ok := waitConfig["duration"]; ok {
			inputParams["duration"] = duration
		}
		if until, ok := waitConfig["until"]; ok {
			inputParams["until"] = until
		}
		task["inputParameters"] = inputParams
		delete(task, "wait") // Remove KRM-only field
	}

	// ─── Recurse into nested task structures ─────────────────────

	// FORK_JOIN: forkTasks is array of arrays of tasks
	if forkTasks, ok := task["forkTasks"].([]interface{}); ok {
		var transformedBranches []interface{}
		for branchIdx, branchRaw := range forkTasks {
			branch, ok := branchRaw.([]interface{})
			if !ok {
				return nil, fmt.Errorf("forkTasks[%d] is not an array", branchIdx)
			}
			transformedBranch, err := transformTasks(branch)
			if err != nil {
				return nil, fmt.Errorf("forkTasks[%d]: %w", branchIdx, err)
			}
			transformedBranches = append(transformedBranches, transformedBranch)
		}
		task["forkTasks"] = transformedBranches
	}

	// SWITCH/DECISION: decisionCases is map of case → task array
	if decisionCases, ok := task["decisionCases"].(map[string]interface{}); ok {
		transformedCases := make(map[string]interface{})
		for caseName, caseTasksRaw := range decisionCases {
			caseSlice, ok := caseTasksRaw.([]interface{})
			if !ok {
				// Might be a single task — wrap it
				caseSlice = []interface{}{caseTasksRaw}
			}
			transformedCase, err := transformTasks(caseSlice)
			if err != nil {
				return nil, fmt.Errorf("decisionCases[%s]: %w", caseName, err)
			}
			transformedCases[caseName] = transformedCase
		}
		task["decisionCases"] = transformedCases
	}

	// SWITCH/DECISION: defaultCase is array of tasks
	if defaultCase, ok := task["defaultCase"].([]interface{}); ok {
		transformedDefault, err := transformTasks(defaultCase)
		if err != nil {
			return nil, fmt.Errorf("defaultCase: %w", err)
		}
		task["defaultCase"] = transformedDefault
	}

	// DO_WHILE: loopOver is array of tasks
	if loopOver, ok := task["loopOver"].([]interface{}); ok {
		transformedLoop, err := transformTasks(loopOver)
		if err != nil {
			return nil, fmt.Errorf("loopOver: %w", err)
		}
		task["loopOver"] = transformedLoop
	}

	return task, nil
}

// ─── Utility ─────────────────────────────────────────────────────

// deepCopyMap creates a shallow copy of a map. For nested maps/slices,
// references are shared — this is fine because we only modify top-level
// keys in each transformation step.
func deepCopyMap(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// getOrCreateMap returns the map at key, creating it if it doesn't exist.
func getOrCreateMap(m map[string]interface{}, key string) map[string]interface{} {
	if existing, ok := m[key].(map[string]interface{}); ok {
		// Copy to avoid mutating the original
		result := make(map[string]interface{}, len(existing))
		for k, v := range existing {
			result[k] = v
		}
		return result
	}
	return make(map[string]interface{})
}
