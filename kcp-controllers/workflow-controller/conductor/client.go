package conductor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client wraps the Conductor REST API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a Conductor API client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ─── Workflow Definition API ─────────────────────────────────────

// RegisterWorkflow registers or updates a workflow definition.
// Takes raw map — no typed struct, so nothing gets lost in serialization.
func (c *Client) RegisterWorkflow(ctx context.Context, def map[string]interface{}) error {
	body, err := json.Marshal(def)
	if err != nil {
		return fmt.Errorf("marshaling workflow definition: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT",
		c.baseURL+"/api/metadata/workflow", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling Conductor API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Conductor API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// DeleteWorkflow removes a workflow definition.
func (c *Client) DeleteWorkflow(ctx context.Context, name string, version int) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE",
		fmt.Sprintf("%s/api/metadata/workflow/%s/%d", c.baseURL, name, version), nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling Conductor API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Conductor API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// ─── Workflow Execution API ──────────────────────────────────────

// StartWorkflow starts a new execution. Returns the workflow execution ID.
func (c *Client) StartWorkflow(ctx context.Context, req map[string]interface{}) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshaling start request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/api/workflow", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("calling Conductor API: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("Conductor API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return string(bytes.TrimSpace(respBody)), nil
}

// GetExecution fetches the current status of a workflow execution.
func (c *Client) GetExecution(ctx context.Context, workflowId string) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		c.baseURL+"/api/workflow/"+workflowId, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling Conductor API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Conductor API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return result, nil
}

// TerminateExecution terminates a running execution.
func (c *Client) TerminateExecution(ctx context.Context, workflowId string, reason string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE",
		fmt.Sprintf("%s/api/workflow/%s?reason=%s", c.baseURL, workflowId, reason), nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling Conductor API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Conductor API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
