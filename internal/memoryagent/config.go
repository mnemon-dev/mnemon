package memoryagent

import (
	"os"
	"path/filepath"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// Open constructs only the explicitly configured models. All stores and HTTP
// clients remain owned by the returned Agent until Close, after its calls exit.
func Open(store *records.Store, directory string, config Config, readonly bool) (*Agent, error) {
	models, budget, err := OpenModels(config)
	if err != nil {
		return nil, err
	}
	agent, err := OpenWithModels(store, directory, config, readonly, models)
	if err != nil {
		if budget != nil {
			budget.Close()
		}
		return nil, err
	}
	agent.budget = budget
	return agent, nil
}

// OpenModels constructs the configured models and the budget file they share
// (nil without one). The caller owns both: it closes the budget after the
// models' last call. Agents opened with OpenWithModels can share them.
func OpenModels(config Config) (Models, *Budget, error) {
	var budget *Budget
	var err error
	if config.BudgetFile != "" {
		budget, err = OpenBudgetWithCaps(config.BudgetFile, config.Budgets)
		if err != nil {
			return Models{}, nil, err
		}
	}
	models := Models{}
	for _, model := range []struct {
		config *ModelConfig
		set    func(*Provider)
	}{
		{config.Planner, func(p *Provider) { models.Planner = p }},
		{config.Judge, func(p *Provider) { models.Judge = p }},
		{config.Consolidator, func(p *Provider) { models.Consolidator = p }},
	} {
		if model.config == nil {
			continue
		}
		provider, err := NewProvider(*model.config, budget)
		if err != nil {
			if budget != nil {
				budget.Close()
			}
			return Models{}, nil, err
		}
		model.set(provider)
	}
	return models, budget, nil
}

// OpenWithModels is Open with models the caller owns, for example models
// shared by the agents of many stores; the agent's Close leaves them alone.
// It opens the derived index in directory like Open: a broken index leaves
// reads of the original evidence working and fails consolidation.
func OpenWithModels(store *records.Store, directory string, config Config, readonly bool, models Models) (*Agent, error) {
	var overlay *Overlay
	var indexError error
	if config.Policy == nil || config.Policy.Consolidation {
		path := filepath.Join(directory, "memory-index.db")
		_, statErr := os.Stat(path)
		if !readonly || statErr == nil {
			var err error
			overlay, err = OpenOverlay(path, readonly)
			if err != nil {
				// A rebuildable index cannot make original evidence unavailable.
				overlay, indexError = nil, err
			}
		}
	}
	agent, err := New(store, config, models, overlay)
	if err != nil {
		if overlay != nil {
			overlay.Close()
		}
		return nil, err
	}
	agent.indexError = indexError
	return agent, nil
}
