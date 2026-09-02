package testing

import (
	"testing"

	"github.com/railwayapp/railpack/core/app"
	"github.com/railwayapp/railpack/core/config"
	"github.com/railwayapp/railpack/core/generate"
	"github.com/railwayapp/railpack/core/logger"
	"github.com/railwayapp/railpack/core/resolver"
)

// CreateGenerateContext creates a new GenerateContext for testing purposes
func CreateGenerateContext(t *testing.T, path string) *generate.GenerateContext {
	t.Helper() // This marks the function as a test helper, which improves test output

	userApp, err := app.NewApp(path)
	if err != nil {
		t.Fatalf("error creating app: %v", err)
	}
	t.Cleanup(func() {
		if err := userApp.Close(); err != nil {
			t.Errorf("close app: %v", err)
		}
	})

	env := app.NewEnvironment(nil)

	config := config.EmptyConfig()

	ctx, err := generate.NewGenerateContextWithResolver(userApp, env, config, logger.NewLogger(), resolver.NewUnresolved())
	if err != nil {
		t.Fatalf("error creating generate context: %v", err)
	}

	return ctx
}
