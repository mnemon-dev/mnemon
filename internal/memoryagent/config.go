package memoryagent

import (
	"os"
	"path/filepath"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// Open constructs only the explicitly configured models. All stores and HTTP
// clients remain owned by the returned Agent until Close, after its calls exit.
func Open(store *records.Store, directory string, config Config, readonly bool) (*Agent, error) {
	models, budget, cache, err := OpenModels(config)
	if err != nil {
		return nil, err
	}
	agent, err := OpenWithModels(store, directory, config, readonly, models)
	if err != nil {
		closeModelFiles(budget, cache)
		return nil, err
	}
	agent.budget, agent.cache, agent.ownsModels = budget, cache, true
	return agent, nil
}

// OpenModels constructs the configured models, the budget file they share
// (nil without one) and the model cache they share (Config.ModelCache, nil
// without one). The caller owns the budget and the cache: it closes them
// after the models' last call. Agents opened with OpenWithModels can share
// them. The models send the host's keys (OpenModelsWith).
func OpenModels(config Config) (Models, *Budget, *ModelCache, error) {
	return OpenModelsWith(config, nil)
}

// OpenModelsWith is OpenModels whose providers read their credentials from
// keys: for each key kind the account's key over the host's, read per call
// (keys.go); nil for the host's alone. A paid model needs a key, either one,
// when it is made.
func OpenModelsWith(config Config, keys *ProviderKeys) (Models, *Budget, *ModelCache, error) {
	var budget *Budget
	var cache *ModelCache
	var err error
	if config.BudgetFile != "" {
		budget, err = OpenBudgetWithCaps(config.BudgetFile, config.Budgets)
		if err != nil {
			return Models{}, nil, nil, err
		}
	}
	if config.ModelCache != "" {
		cache, err = OpenModelCache(config.ModelCache)
		if err != nil {
			closeModelFiles(budget, nil)
			return Models{}, nil, nil, err
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
		provider, err := newProvider(*model.config, budget, keys)
		if err != nil {
			closeModelFiles(budget, cache)
			return Models{}, nil, nil, err
		}
		provider.cache = cache
		model.set(provider)
	}
	return models, budget, cache, nil
}

// closeModelFiles closes the files models share; either may be nil.
func closeModelFiles(budget *Budget, cache *ModelCache) {
	if budget != nil {
		budget.Close()
	}
	if cache != nil {
		cache.Close()
	}
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
