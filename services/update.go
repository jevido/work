package services

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"

	"dev.jevido/work/internal/update"
)

// UpdateService exposes self-update to the frontend.
//
// It is two things: the owner of the background check's lifetime, and the one
// call the frontend makes when the user says yes. Everything else about
// updating -- what the latest release is, whether it is newer, fetching it,
// verifying it, swapping it in -- is in internal/update, and the frontend
// learns about it only through the update:available event.
type UpdateService struct {
	checker *update.Checker
}

// NewUpdateService wires the service to a checker.
func NewUpdateService(checker *update.Checker) *UpdateService {
	return &UpdateService{checker: checker}
}

// ServiceName names the service for logs and generated bindings.
func (s *UpdateService) ServiceName() string { return "UpdateService" }

// ServiceStartup starts polling for releases.
//
// The poll lives here rather than in main because this is where its lifetime
// is already defined: ctx is cancelled when the app shuts down, so the
// goroutine ends with the window instead of having to be shut down separately.
func (s *UpdateService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.checker.Start(ctx)
	return nil
}

// Version is the release this binary was built as, or "dev" for a build that
// was not stamped by the release workflow.
func (s *UpdateService) Version() string {
	return s.checker.Version()
}

// ApplyUpdate installs the newest release over the running binary and restarts
// into it.
//
// It returns once the new binary is verified and in place, and the restart
// follows a fraction of a second later -- so the caller does get a resolved
// promise, and should use it to show that Work is about to disappear and come
// back rather than to report success and carry on. An error means nothing was
// installed and the running Work is untouched.
//
// The download is not bound to the app's shutdown context: it has its own
// timeout, and the only thing that should abandon a user-initiated install
// halfway is the process ending, which takes it with it anyway.
func (s *UpdateService) ApplyUpdate() error {
	return s.checker.ApplyUpdate(context.Background())
}
