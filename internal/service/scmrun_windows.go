//go:build windows

package service

import (
	"context"

	"golang.org/x/sys/windows/svc"
)

// RunSupervised lets a long-running command (gate daemon, gate relay)
// participate in the host's service supervisor when one is in charge of
// this process.
//
// On Windows, `eidos.exe gate daemon` may be launched either by a human
// from a shell (signal-driven cancellation) or by the Service Control
// Manager (SCM-driven cancellation). RunSupervised auto-detects which by
// calling svc.IsWindowsService() and:
//
//   - If running under SCM: registers a service handler with svc.Run and
//     translates SERVICE_CONTROL_STOP / SHUTDOWN into a cancellation of
//     the supplied context. The runner sees the context become Done.
//   - Otherwise: just calls runner(ctx) directly so debug / dev runs are
//     unchanged.
//
// On non-Windows hosts this is a pure pass-through; see scmrun_other.go.
//
// `name` is the SCM service name (e.g. DaemonUnitName) that SCM hands the
// handler. `runner` should obey ctx and return when ctx is Done.
func RunSupervised(ctx context.Context, name string, runner func(context.Context) error) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService {
		return runner(ctx)
	}
	h := &scmHandler{ctx: ctx, runner: runner}
	if runErr := svc.Run(name, h); runErr != nil {
		return runErr
	}
	return h.runErr
}

// scmHandler wires SCM control messages to the runner's context so we can
// share one context lifetime across "user pressed Ctrl-C" and "SCM sent
// Stop".
type scmHandler struct {
	ctx    context.Context
	runner func(context.Context) error
	runErr error
}

func (h *scmHandler) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	changes <- svc.Status{State: svc.StartPending}
	runCtx, cancel := context.WithCancel(h.ctx)
	defer cancel()

	errc := make(chan error, 1)
	go func() { errc <- h.runner(runCtx) }()

	changes <- svc.Status{State: svc.Running, Accepts: accepted}

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				cancel()
				h.runErr = <-errc
				changes <- svc.Status{State: svc.StopPending}
				return false, 0
			}
		case err := <-errc:
			h.runErr = err
			changes <- svc.Status{State: svc.StopPending}
			if err != nil {
				return false, 1
			}
			return false, 0
		}
	}
}
