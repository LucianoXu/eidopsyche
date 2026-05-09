//go:build windows

package service

import (
	"context"
	"time"

	"golang.org/x/sys/windows/svc"
)

// scmStopWaitHint is the time we promise SCM the daemon will take to
// finish shutting down after a Stop / Shutdown control. SCM uses the
// hint to decide when to consider the service hung — too short and a
// daemon doing a clean inbox flush gets killed; too long and the
// service-stop UX feels sluggish. Five seconds matches the relay's
// graceful shutdown timeout (cmd/eidos/gate/relay.go) and gives the
// daemon plenty of headroom to drain its goroutines.
const scmStopWaitHint = 5 * time.Second

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
				// Tell SCM we acknowledged the stop *before* waiting for
				// the runner to drain. SCM treats a service that doesn't
				// move out of Running within a few seconds of receiving
				// Stop as hung; reporting StopPending with a WaitHint
				// keeps SCM patient through a graceful shutdown.
				changes <- svc.Status{
					State:    svc.StopPending,
					WaitHint: uint32(scmStopWaitHint / time.Millisecond),
				}
				cancel()
				h.runErr = <-errc
				return false, 0
			}
		case err := <-errc:
			// Runner returned on its own (fatal startup error or clean
			// exit). Report StopPending so SCM transitions us cleanly
			// into Stopped.
			h.runErr = err
			changes <- svc.Status{State: svc.StopPending}
			if err != nil {
				return false, 1
			}
			return false, 0
		}
	}
}
