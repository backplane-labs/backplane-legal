// legal serves installation-local legal documents and acceptance evidence.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/backplane-labs/backplane/pkg/backplane"
	"github.com/backplane-labs/backplane/pkg/backplane/drivers/standard"

	"github.com/backplane-labs/backplane-legal/internal/service"
)

func run(ctx context.Context) error {
	svc, err := backplane.Open(ctx, service.New, standard.Drivers(), backplane.ConfigOptions(service.Defaults()))
	if err != nil {
		return fmt.Errorf("open legal: %w", err)
	}

	service.Declare(svc)

	if err := svc.Run(ctx); err != nil {
		return fmt.Errorf("run legal: %w", err)
	}

	return nil
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
