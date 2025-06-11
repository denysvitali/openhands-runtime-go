package executor

import (
	"context"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
)

// executeBrowseURL navigates to a URL (placeholder implementation)
func (e *Executor) executeBrowseURL(ctx context.Context, action models.BrowseURLAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "browse_url")
	defer span.End()

	return models.NewBrowserObservation(
		"Browser navigation not implemented in Go runtime",
		action.URL,
		"", // No screenshot
	), nil
}

// executeBrowseInteractive performs browser interaction (placeholder implementation)
func (e *Executor) executeBrowseInteractive(ctx context.Context, action models.BrowseInteractiveAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "browse_interactive")
	defer span.End()

	return models.NewBrowserObservation(
		"Browser interaction not implemented in Go runtime",
		"", // No URL for interactive browsing
		"", // No screenshot
	), nil
}
