package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalEmployeeProvider_EmployeeByID(t *testing.T) {
	p := &LocalEmployeeProvider{}

	emp, err := p.EmployeeByID(context.Background(), "john-doe")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emp.ID != "john-doe" {
		t.Errorf("got ID %q, want %q", emp.ID, "john-doe")
	}
	if emp.Name != "John Doe" {
		t.Errorf("got Name %q, want %q", emp.Name, "John Doe")
	}

	emp, err = p.EmployeeByID(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emp != nil {
		t.Errorf("expected nil for empty id, got %+v", emp)
	}
}

func TestLocalEmployeeProvider_Employees(t *testing.T) {
	p := &LocalEmployeeProvider{}

	employees, err := p.Employees(context.Background(), "trust-mode-user-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(employees) != 1 {
		t.Fatalf("got %d employees, want 1", len(employees))
	}
	if employees[0].Name != "Trust Mode User" {
		t.Errorf("got Name %q, want %q", employees[0].Name, "Trust Mode User")
	}

	employees, err = p.Employees(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(employees) != 0 {
		t.Errorf("got %d employees for empty user, want 0", len(employees))
	}
}

func TestRemoteEmployeeProvider_EmployeeByID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req graphQLRequest
		json.NewDecoder(r.Body).Decode(&req)
		resp := map[string]any{
			"data": map[string]any{
				"employeeByID": map[string]any{"id": "u1", "name": "Alice"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewRemoteEmployeeProvider(server.URL)
	emp, err := p.EmployeeByID(context.Background(), "u1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emp.ID != "u1" || emp.Name != "Alice" {
		t.Errorf("got %+v, want {u1 Alice}", emp)
	}
}

func TestRemoteEmployeeProvider_Employees(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data": map[string]any{
				"employees": []map[string]any{
					{"id": "u1", "name": "Alice"},
					{"id": "u2", "name": "Bob"},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewRemoteEmployeeProvider(server.URL)
	employees, err := p.Employees(context.Background(), "u1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(employees) != 2 {
		t.Fatalf("got %d employees, want 2", len(employees))
	}
}

func TestRemoteEmployeeProvider_Fallback(t *testing.T) {
	p := NewRemoteEmployeeProvider("http://127.0.0.1:1")
	_, err := p.EmployeeByID(context.Background(), "u1")
	if err == nil {
		t.Fatal("expected error for unreachable service")
	}
}
