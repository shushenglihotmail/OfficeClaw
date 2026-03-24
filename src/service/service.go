// Package service implements Windows Service integration for OfficeClaw.
// When running as a service, OfficeClaw receives proper shutdown notifications
// and can be configured with automatic recovery on failure.
package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"golang.org/x/sys/windows/svc"
)

const ServiceName = "OfficeClaw"

// Handler implements svc.Handler for Windows Service Control Manager.
type Handler struct {
	// Cancel triggers graceful shutdown of the application.
	Cancel context.CancelFunc
	// Done is closed when the application has finished shutting down.
	Done <-chan struct{}
	// Logger for service events.
	Logger *log.Logger
}

// Execute implements svc.Handler. It is called by the Windows SCM.
func (h *Handler) Execute(args []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	// Accept stop, shutdown, and preshutdown commands
	const accepts = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown

	status <- svc.Status{State: svc.StartPending}
	h.Logger.Printf("[service] Starting Windows service")

	status <- svc.Status{State: svc.Running, Accepts: accepts}
	h.Logger.Printf("[service] Windows service running")

	for {
		select {
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus

			case svc.Stop, svc.Shutdown:
				h.Logger.Printf("[service] Received stop/shutdown command")
				status <- svc.Status{State: svc.StopPending}
				h.Cancel()

				// Wait for application to finish with timeout
				select {
				case <-h.Done:
					h.Logger.Printf("[service] Application shutdown complete")
				case <-time.After(60 * time.Second):
					h.Logger.Printf("[service] Shutdown timeout exceeded")
				}
				return false, 0

			case svc.PreShutdown:
				// PreShutdown gives us more time before system shutdown
				h.Logger.Printf("[service] Received pre-shutdown notification")
				status <- svc.Status{State: svc.StopPending}
				h.Cancel()

				select {
				case <-h.Done:
					h.Logger.Printf("[service] Application shutdown complete (pre-shutdown)")
				case <-time.After(120 * time.Second):
					h.Logger.Printf("[service] Pre-shutdown timeout exceeded")
				}
				return false, 0
			}

		case <-h.Done:
			// Application shut down on its own (e.g., fatal error)
			h.Logger.Printf("[service] Application exited")
			return false, 0
		}
	}
}

// IsWindowsService returns true if the process is running as a Windows service.
func IsWindowsService() bool {
	is, err := svc.IsWindowsService()
	if err != nil {
		return false
	}
	return is
}

// Run starts the Windows service handler. This blocks until the service stops.
// The appStart function should start the application in a goroutine and return
// a cancel function and a done channel.
func Run(cancel context.CancelFunc, done <-chan struct{}, logger *log.Logger) error {
	h := &Handler{
		Cancel: cancel,
		Done:   done,
		Logger: logger,
	}

	err := svc.Run(ServiceName, h)
	if err != nil {
		return fmt.Errorf("service run failed: %w", err)
	}
	return nil
}
