package service_test

import (
	"testing"

	"github.com/backplane-labs/backplane/pkg/backplane"
	"github.com/backplane-labs/backplane/pkg/backplane/config"

	"github.com/backplane-labs/backplane-legal/internal/service"
)

func TestConfigDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load[service.Config](t.Context(), config.Service("legal"), config.WithoutFile(), config.WithoutEnv(), service.Defaults())
	if err != nil {
		t.Fatal(err)
	}

	if cfg.HTTP.Listen != ":8085" || cfg.DefaultLang != "en" || len(cfg.HTTP.FrameAncestors.Get()) != 1 || cfg.HTTP.FrameAncestors.Get()[0] != "*" {
		t.Fatalf("wrong defaults: %+v", cfg)
	}

	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

type httpConfig struct {
	config.Backplane `json:"backplane"`
	HTTP             service.HTTPConfig `json:"http"`
}

func TestEmbeddingPolicyValidation(t *testing.T) {
	t.Parallel()

	runtime, err := config.Open[httpConfig](t.Context(), config.Service("legal"), config.WithoutFile(), config.WithoutEnv(), config.Defaults(map[string]any{"http": map[string]any{"frame_ancestors": []string{"'none'", "*"}}}))
	if err == nil {
		_ = runtime.Close()

		t.Fatal("invalid embedding configuration accepted")
	}
}

func TestDeclarations(t *testing.T) {
	t.Parallel()

	svc, err := backplane.Open(t.Context(), service.New, backplane.Name("legal"), backplane.ConfigOptions(config.WithoutFile(), config.WithoutEnv(), service.Defaults(), config.Defaults(map[string]any{"postgres": map[string]any{"dsn": "postgres://legal:legal@localhost:5457/legal?sslmode=disable"}})))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = svc.Close() })
	service.Declare(svc)

	if err := svc.Validate(); err != nil {
		t.Fatal(err)
	}
}
