package library

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// Policies loads every policy in evaluation order.
func (s *Service) Policies(ctx context.Context) ([]policy.Policy, error) {
	rows, err := s.store.Policies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]policy.Policy, 0, len(rows))
	for _, r := range rows {
		p, err := fromRow(r)
		if err != nil {
			return nil, fmt.Errorf("policy %q: %w", r.Name, err)
		}
		out = append(out, p)
	}
	policy.Sort(out)
	return out, nil
}

// Policy loads one policy.
func (s *Service) Policy(ctx context.Context, id int64) (policy.Policy, error) {
	r, err := s.store.Policy(ctx, id)
	if err != nil {
		return policy.Policy{}, err
	}
	return fromRow(r)
}

// SavePolicy validates and stores a policy and returns its ID. Callers
// re-evaluate the library afterwards.
func (s *Service) SavePolicy(ctx context.Context, p policy.Policy) (int64, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	row, err := toRow(p)
	if err != nil {
		return 0, err
	}
	return s.store.SavePolicy(ctx, row)
}

// CreateStarterPolicies adds the starter policies if there are none yet.
func (s *Service) CreateStarterPolicies(ctx context.Context, tvLibraries []string) error {
	existing, err := s.store.Policies(ctx)
	if err != nil || len(existing) > 0 {
		return err
	}
	for _, p := range policy.Starter() {
		if p.Name == "Space-saving television" && len(tvLibraries) > 0 {
			p.Scope.Libraries = tvLibraries
		}
		if _, err := s.SavePolicy(ctx, p); err != nil {
			return fmt.Errorf("starter policy %q: %w", p.Name, err)
		}
	}
	return nil
}

func fromRow(r store.PolicyRow) (policy.Policy, error) {
	p := policy.Policy{ID: r.ID, Name: r.Name, Enabled: r.Enabled, Priority: r.Priority}
	if err := json.Unmarshal([]byte(r.Scope), &p.Scope); err != nil {
		return p, fmt.Errorf("scope: %w", err)
	}
	if err := json.Unmarshal([]byte(r.Conditions), &p.Conditions); err != nil {
		return p, fmt.Errorf("conditions: %w", err)
	}
	if err := json.Unmarshal([]byte(r.Action), &p.Action); err != nil {
		return p, fmt.Errorf("action: %w", err)
	}
	return p, nil
}

func toRow(p policy.Policy) (store.PolicyRow, error) {
	p.Scope.Version, p.Conditions.Version, p.Action.Version = 1, 1, 1
	if p.Conditions.All == nil {
		p.Conditions.All = []policy.Condition{}
	}
	scope, err := json.Marshal(p.Scope)
	if err != nil {
		return store.PolicyRow{}, err
	}
	conds, err := json.Marshal(p.Conditions)
	if err != nil {
		return store.PolicyRow{}, err
	}
	action, err := json.Marshal(p.Action)
	if err != nil {
		return store.PolicyRow{}, err
	}
	return store.PolicyRow{ID: p.ID, Name: p.Name, Enabled: p.Enabled, Priority: p.Priority,
		Scope: string(scope), Conditions: string(conds), Action: string(action)}, nil
}
