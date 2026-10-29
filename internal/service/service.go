// Package service composes legal's SDK lifecycle, storage and adapters.
package service

import (
	"fmt"
	"net/http"
	"time"

	"github.com/backplane-labs/backplane/pkg/backplane"
	"github.com/backplane-labs/backplane/pkg/backplane/api"
	"github.com/backplane-labs/backplane/pkg/backplane/config"
	"github.com/backplane-labs/backplane/pkg/backplane/deps"
	"github.com/backplane-labs/backplane/pkg/backplane/infra/postgres"
	"google.golang.org/grpc"

	"github.com/backplane-labs/backplane-legal/internal/app"
	"github.com/backplane-labs/backplane-legal/internal/domain"
	"github.com/backplane-labs/backplane-legal/internal/infra/activities"
	"github.com/backplane-labs/backplane-legal/internal/infra/admin"
	"github.com/backplane-labs/backplane-legal/internal/infra/httpapi"
	"github.com/backplane-labs/backplane-legal/internal/infra/outbox"
	"github.com/backplane-labs/backplane-legal/internal/store"
	"github.com/backplane-labs/backplane-legal/internal/ui"
	legalconsolev1 "github.com/backplane-labs/backplane-legal/proto/legal/console/v1"
)

// Config is loaded by the SDK: file, then environment, then declared live fields.
type Config struct {
	config.Backplane `desc:"Backplane platform connections and lifecycle."         json:"backplane"`
	Postgres         postgres.Config `desc:"Legal PostgreSQL connection; owns the legal schema."   json:"postgres"`
	HTTP             HTTPConfig      `desc:"Read-only public HTTP API and document page security." json:"http"`

	DefaultLang string `desc:"Fallback BCP 47 language." json:"default_lang" schemapb:"default=en"`
}

// HTTPConfig controls the public listener and live iframe embedding policy.
type HTTPConfig struct {
	Listen string `desc:"Public read-only API listener." json:"listen" schemapb:"default=:8085"`

	FrameAncestors config.Live[FrameSources] `desc:"Iframe embedding policy; live." json:"frame_ancestors"`
}

// FrameSources validates each live candidate before the SDK makes it active.
type FrameSources []string

// Validate rejects CSP directive injection and contradictory source policies.
func (f FrameSources) Validate() error {
	if err := httpapi.ValidateAncestors(f); err != nil {
		return fmt.Errorf("frame policy: %w", err)
	}

	return nil
}

// Defaults supplies composite defaults that cannot be expressed as scalar schema tags.
func Defaults() config.Option {
	return config.Defaults(map[string]any{"http": map[string]any{"frame_ancestors": []string{"*"}}})
}

// Validate rejects invalid language and CSP values before applying configuration.
func (c Config) Validate() error {
	if !domain.ValidLanguage(c.DefaultLang) {
		return fmt.Errorf("%w: default_lang is not BCP 47", domain.ErrInvalid)
	}

	if err := httpapi.ValidateAncestors(c.HTTP.FrameAncestors.Get()); err != nil {
		return fmt.Errorf("frame_ancestors: %w", err)
	}

	return nil
}

// State is the installation-local dependency tree.
type State struct {
	Store      deps.Dependency[*store.Store]
	Legal      *app.Service
	Admin      *admin.Admin
	Activities *activities.Activities
	Relay      *outbox.Relay
	public     string
	frames     config.Live[FrameSources]
}

// New composes components without opening listeners or starting goroutines itself.
func New(root backplane.Root[Config]) (*State, error) {
	const probeTimeout = 2 * time.Second

	cfg := root.Config()
	dep := deps.NewDependency(root, store.New(cfg.Postgres), deps.ProbeTimeout(probeTimeout))
	repository := store.NewRepository(dep)
	legal := app.New(repository, cfg.DefaultLang)

	return &State{
		Store: dep, Legal: legal,
		Admin: admin.New(root, legal), Activities: activities.New(root, legal),
		Relay: outbox.New(root, repository), public: cfg.HTTP.Listen, frames: cfg.HTTP.FrameAncestors,
	}, nil
}

// Declare registers public HTTP, the protected internal API and an optional UI bundle.
func Declare(svc *backplane.Service[State]) {
	st := svc.State()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpapi.New(st.Legal, st.frames.Get()).ServeHTTP(w, r)
	})
	svc.HTTP("/legal/", handler, api.Listen("public", st.public))
	svc.Internal(func(reg grpc.ServiceRegistrar) { legalconsolev1.RegisterAdminServiceServer(reg, st.Admin) })

	if bundle := ui.Bundle(); bundle != nil {
		svc.UI(bundle)
	}
}
