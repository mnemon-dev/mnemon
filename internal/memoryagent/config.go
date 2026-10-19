package memoryagent

import (
	"os"
	"path/filepath"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// Open constructs only the explicitly configured models. All stores and HTTP
// clients remain owned by the returned Agent until Close, after its calls exit.
func Open(store *records.Store, directory string, config Config, readonly bool) (*Agent, error) {
	var budget *Budget
	var overlay *Overlay
	var indexError error
	var err error
	if config.BudgetFile != "" {
		budget, err = OpenBudgetWithCaps(config.BudgetFile, config.Budgets)
		if err != nil {
			return nil, err
		}
	}
	ok := false
	defer func() {
		if !ok {
			if budget != nil {
				budget.Close()
			}
			if overlay != nil {
				overlay.Close()
			}
		}
	}()
	models := Models{}
	if config.Planner != nil {
		models.Planner, err = NewProvider(*config.Planner, budget)
		if err != nil {
			return nil, err
		}
	}
	if config.Judge != nil {
		models.Judge, err = NewProvider(*config.Judge, budget)
		if err != nil {
			return nil, err
		}
	}
	if config.Consolidator != nil {
		models.Consolidator, err = NewProvider(*config.Consolidator, budget)
		if err != nil {
			return nil, err
		}
	}
	if config.Policy == nil || config.Policy.Consolidation {
		path := filepath.Join(directory, "memory-index.db")
		_, statErr := os.Stat(path)
		if !readonly || statErr == nil {
			overlay, err = OpenOverlay(path, readonly)
			if err != nil {
				// A rebuildable index cannot make original evidence unavailable.
				indexError = err
			}
		}
	}
	agent, err := New(store, config, models, overlay)
	if err != nil {
		return nil, err
	}
	agent.budget = budget
	agent.indexError = indexError
	ok = true
	return agent, nil
}
