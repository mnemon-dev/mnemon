package embed

import (
	"testing"
)

func TestNewClient_DefaultModel(t *testing.T) {
	t.Setenv("MNEMON_EMBED_MODEL", "")
	c := NewClient()
	if c.Model() != DefaultModel {
		t.Errorf("default model: want %q, got %q", DefaultModel, c.Model())
	}
}

func TestNewClient_EnvOverride(t *testing.T) {
	t.Setenv("MNEMON_EMBED_MODEL", "env-model:latest")
	c := NewClient()
	if c.Model() != "env-model:latest" {
		t.Errorf("env-derived model: want %q, got %q", "env-model:latest", c.Model())
	}
}

func TestNewClientWithModel_Explicit(t *testing.T) {
	t.Setenv("MNEMON_EMBED_MODEL", "")
	c := NewClientWithModel("explicit-model:v1")
	if c.Model() != "explicit-model:v1" {
		t.Errorf("explicit model: want %q, got %q", "explicit-model:v1", c.Model())
	}
}

func TestNewClientWithModel_ExplicitWinsOverEnv(t *testing.T) {
	t.Setenv("MNEMON_EMBED_MODEL", "env-model")
	c := NewClientWithModel("explicit-model")
	if c.Model() != "explicit-model" {
		t.Errorf("explicit-over-env: want %q, got %q", "explicit-model", c.Model())
	}
}

func TestNewClientWithModel_EmptyFallsBackToEnv(t *testing.T) {
	t.Setenv("MNEMON_EMBED_MODEL", "env-model")
	c := NewClientWithModel("")
	if c.Model() != "env-model" {
		t.Errorf("empty-falls-to-env: want %q, got %q", "env-model", c.Model())
	}
}

func TestNewClientWithModel_EmptyAndNoEnvFallsBackToDefault(t *testing.T) {
	t.Setenv("MNEMON_EMBED_MODEL", "")
	c := NewClientWithModel("")
	if c.Model() != DefaultModel {
		t.Errorf("empty-and-no-env: want %q, got %q", DefaultModel, c.Model())
	}
}

func TestNewClientWithModel_DefaultEndpoint(t *testing.T) {
	t.Setenv("MNEMON_EMBED_ENDPOINT", "")
	c := NewClientWithModel("any-model")
	if c.Endpoint() != DefaultEndpoint {
		t.Errorf("default endpoint: want %q, got %q", DefaultEndpoint, c.Endpoint())
	}
}
