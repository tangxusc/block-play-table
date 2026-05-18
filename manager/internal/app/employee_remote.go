package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type RemoteEmployeeProvider struct {
	endpoint   string
	httpClient *http.Client
}

func NewRemoteEmployeeProvider(endpoint string) *RemoteEmployeeProvider {
	return &RemoteEmployeeProvider{
		endpoint:   endpoint,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

func (p *RemoteEmployeeProvider) EmployeeByID(ctx context.Context, id string) (*Employee, error) {
	if id == "" {
		return nil, nil
	}
	req := graphQLRequest{
		Query:     `query EmployeeByID($id: ID!) { employeeByID(id: $id) { id name } }`,
		Variables: map[string]any{"id": id},
	}
	var result struct {
		EmployeeByID *Employee `json:"employeeByID"`
	}
	if err := p.execute(ctx, req, &result); err != nil {
		return nil, err
	}
	return result.EmployeeByID, nil
}

func (p *RemoteEmployeeProvider) Employees(ctx context.Context, _ string) ([]*Employee, error) {
	req := graphQLRequest{
		Query: `query { employees { id name } }`,
	}
	var result struct {
		Employees []*Employee `json:"employees"`
	}
	if err := p.execute(ctx, req, &result); err != nil {
		return nil, err
	}
	return result.Employees, nil
}

func (p *RemoteEmployeeProvider) execute(ctx context.Context, gqlReq graphQLRequest, target any) error {
	body, err := json.Marshal(gqlReq)
	if err != nil {
		return fmt.Errorf("marshal graphql request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("employee service request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("employee service returned status %d", resp.StatusCode)
	}
	var gqlResp graphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if len(gqlResp.Errors) > 0 {
		return fmt.Errorf("employee service error: %s", gqlResp.Errors[0].Message)
	}
	return json.Unmarshal(gqlResp.Data, target)
}
