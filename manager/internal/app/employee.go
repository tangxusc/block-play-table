package app

import (
	"context"
	"strings"
)

type Employee struct {
	ID   string
	Name string
}

type EmployeeProvider interface {
	EmployeeByID(ctx context.Context, id string) (*Employee, error)
	Employees(ctx context.Context, currentUserID string) ([]*Employee, error)
}

type LocalEmployeeProvider struct{}

func (p *LocalEmployeeProvider) EmployeeByID(_ context.Context, id string) (*Employee, error) {
	if id == "" {
		return nil, nil
	}
	return &Employee{ID: id, Name: MockEmployeeName(id)}, nil
}

func (p *LocalEmployeeProvider) Employees(_ context.Context, currentUserID string) ([]*Employee, error) {
	if currentUserID == "" {
		return []*Employee{}, nil
	}
	return []*Employee{{ID: currentUserID, Name: MockEmployeeName(currentUserID)}}, nil
}

func MockEmployeeName(userID string) string {
	switch userID {
	case "trust-mode-user-id":
		return "Trust Mode User"
	default:
		name := strings.ReplaceAll(userID, "-", " ")
		name = strings.ReplaceAll(name, "_", " ")
		words := strings.Fields(name)
		for i, w := range words {
			if len(w) > 0 {
				words[i] = strings.ToUpper(w[:1]) + w[1:]
			}
		}
		if len(words) == 0 {
			return "Unknown User"
		}
		return strings.Join(words, " ")
	}
}
