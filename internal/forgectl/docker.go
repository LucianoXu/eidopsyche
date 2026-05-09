package forgectl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// Client is the subset of Docker SDK operations forge uses. Defined as an
// interface so tests can provide a fake.
type Client interface {
	VolumeExists(ctx context.Context, name string) (bool, error)
	VolumeCreate(ctx context.Context, name string) error
	VolumeRemove(ctx context.Context, name string) error

	ContainerExists(ctx context.Context, name string) (bool, error)
	ContainerInspectState(ctx context.Context, name string) (string, error)
	ContainerCreate(ctx context.Context, opts CreateOpts) error
	ContainerStart(ctx context.Context, name string) error
	ContainerStop(ctx context.Context, name string, graceSeconds int) error
	ContainerRemove(ctx context.Context, name string) error

	// RunInit runs a one-shot container with the given image, mount, env,
	// and command. Pipes stdin into the container; returns combined
	// stdout+stderr and exit code. Container is auto-removed on exit.
	RunInit(ctx context.Context, opts RunInitOpts) (RunInitResult, error)

	// ContainerExec runs a command inside a running container and returns the
	// combined output and exit code.
	ContainerExec(ctx context.Context, name string, cmd []string) (ExecResult, error)

	// ImageExists reports whether ref is present in the local Docker image
	// store. forge create uses this to skip the pull when the operator has
	// already built the image locally (the common case for dev / staging
	// where the registry tag may not exist yet).
	ImageExists(ctx context.Context, ref string) (bool, error)

	ImagePull(ctx context.Context, ref string, w io.Writer) error

	// VolumeList returns the names of all volumes whose names start with
	// prefix. An empty prefix returns all volumes.
	VolumeList(ctx context.Context, prefix string) ([]string, error)

	// ContainerLogs streams the combined stdout+stderr of a container to w.
	// If follow is true, the stream continues until the container stops or
	// the context is cancelled.
	ContainerLogs(ctx context.Context, name string, follow bool, w io.Writer) error

	// CopyFromContainer copies a path from inside the container to w as a tar
	// stream.
	CopyFromContainer(ctx context.Context, name, srcPath string, w io.Writer) error
}

// ExecResult is the outcome of a docker exec.
type ExecResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// CreateOpts is the subset of container create the forge orchestrator uses.
type CreateOpts struct {
	Name       string
	Image      string
	Mount      Mount
	Env        []string
	Entrypoint []string
	Cmd        []string
}

// Mount is a named-volume mount target.
type Mount struct {
	VolumeName string
	Target     string
}

// RunInitOpts is the input for one-shot init container runs.
type RunInitOpts struct {
	Image     string
	Mount     Mount
	Env       []string
	Cmd       []string
	Stdin     io.Reader
	AttachTTY bool
	// User overrides the image's USER directive for this one-shot run.
	// Format matches docker's --user flag: "uid", "uid:gid", "name", or
	// "name:group". Empty leaves the image's USER in place.
	User string
}

// RunInitResult is the result of RunInit.
type RunInitResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// realClient is the production Client backed by the Docker SDK.
type realClient struct{ c *client.Client }

// New returns a real Docker client using env-default config (DOCKER_HOST,
// DOCKER_TLS_VERIFY, etc).
func New() (Client, error) {
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &realClient{c: c}, nil
}

func (r *realClient) VolumeExists(ctx context.Context, name string) (bool, error) {
	_, err := r.c.VolumeInspect(ctx, name)
	if err == nil {
		return true, nil
	}
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

func (r *realClient) VolumeCreate(ctx context.Context, name string) error {
	_, err := r.c.VolumeCreate(ctx, volume.CreateOptions{Name: name})
	return err
}

func (r *realClient) VolumeRemove(ctx context.Context, name string) error {
	return r.c.VolumeRemove(ctx, name, true)
}

func (r *realClient) ContainerExists(ctx context.Context, name string) (bool, error) {
	list, err := r.c.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("name", "^/"+name+"$")),
	})
	if err != nil {
		return false, err
	}
	return len(list) > 0, nil
}

func (r *realClient) ContainerInspectState(ctx context.Context, name string) (string, error) {
	resp, err := r.c.ContainerInspect(ctx, name)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return "absent", nil
		}
		return "", err
	}
	return resp.State.Status, nil
}

func (r *realClient) ContainerCreate(ctx context.Context, opts CreateOpts) error {
	_, err := r.c.ContainerCreate(ctx,
		&container.Config{
			Image:      opts.Image,
			Env:        opts.Env,
			Entrypoint: opts.Entrypoint,
			Cmd:        opts.Cmd,
		},
		&container.HostConfig{
			Mounts: []mount.Mount{{
				Type:   mount.TypeVolume,
				Source: opts.Mount.VolumeName,
				Target: opts.Mount.Target,
			}},
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		},
		nil, nil, opts.Name,
	)
	return err
}

func (r *realClient) ContainerStart(ctx context.Context, name string) error {
	return r.c.ContainerStart(ctx, name, container.StartOptions{})
}

func (r *realClient) ContainerStop(ctx context.Context, name string, grace int) error {
	g := grace
	return r.c.ContainerStop(ctx, name, container.StopOptions{Timeout: &g})
}

func (r *realClient) ContainerRemove(ctx context.Context, name string) error {
	return r.c.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
}

func (r *realClient) ImageExists(ctx context.Context, ref string) (bool, error) {
	if _, err := r.c.ImageInspect(ctx, ref); err != nil {
		if cerrdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *realClient) ImagePull(ctx context.Context, ref string, w io.Writer) error {
	rc, err := r.c.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(w, rc)
	return err
}

// errInitFailed wraps a non-zero exit code from RunInit.
var errInitFailed = errors.New("init container exited non-zero")

func (r *realClient) RunInit(ctx context.Context, opts RunInitOpts) (RunInitResult, error) {
	cfg := &container.Config{
		Image:        opts.Image,
		Env:          opts.Env,
		Cmd:          opts.Cmd,
		User:         opts.User,
		AttachStdin:  opts.Stdin != nil,
		AttachStdout: true,
		AttachStderr: true,
		OpenStdin:    opts.Stdin != nil,
		StdinOnce:    opts.Stdin != nil,
		Tty:          opts.AttachTTY,
	}
	host := &container.HostConfig{
		AutoRemove: true,
		Mounts: []mount.Mount{{
			Type:   mount.TypeVolume,
			Source: opts.Mount.VolumeName,
			Target: opts.Mount.Target,
		}},
	}
	created, err := r.c.ContainerCreate(ctx, cfg, host, nil, nil, "")
	if err != nil {
		return RunInitResult{}, fmt.Errorf("create init: %w", err)
	}
	hijack, err := r.c.ContainerAttach(ctx, created.ID, container.AttachOptions{
		Stream: true, Stdin: opts.Stdin != nil, Stdout: true, Stderr: true,
	})
	if err != nil {
		return RunInitResult{}, fmt.Errorf("attach init: %w", err)
	}
	defer hijack.Close()
	if err := r.c.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return RunInitResult{}, fmt.Errorf("start init: %w", err)
	}
	if opts.Stdin != nil {
		go func() {
			_, _ = io.Copy(hijack.Conn, opts.Stdin)
			_ = hijack.CloseWrite()
		}()
	}
	var outBuf, errBuf writeBuffer
	if _, err := stdcopy.StdCopy(&outBuf, &errBuf, hijack.Reader); err != nil {
		return RunInitResult{}, fmt.Errorf("read init streams: %w", err)
	}
	statusCh, errCh := r.c.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case e := <-errCh:
		if e != nil {
			return RunInitResult{Stdout: outBuf.Bytes(), Stderr: errBuf.Bytes()}, fmt.Errorf("wait init: %w", e)
		}
	case s := <-statusCh:
		res := RunInitResult{ExitCode: int(s.StatusCode), Stdout: outBuf.Bytes(), Stderr: errBuf.Bytes()}
		if s.StatusCode != 0 {
			return res, fmt.Errorf("%w: code=%d stderr=%s", errInitFailed, s.StatusCode, errBuf.String())
		}
		return res, nil
	}
	return RunInitResult{Stdout: outBuf.Bytes(), Stderr: errBuf.Bytes()}, nil
}

func (r *realClient) ContainerExec(ctx context.Context, name string, cmdv []string) (ExecResult, error) {
	resp, err := r.c.ContainerExecCreate(ctx, name, container.ExecOptions{
		Cmd:          cmdv,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return ExecResult{}, fmt.Errorf("exec create: %w", err)
	}
	att, err := r.c.ContainerExecAttach(ctx, resp.ID, container.ExecStartOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("exec attach: %w", err)
	}
	defer att.Close()
	var outBuf, errBuf writeBuffer
	if _, err := stdcopy.StdCopy(&outBuf, &errBuf, att.Reader); err != nil {
		return ExecResult{}, err
	}
	insp, err := r.c.ContainerExecInspect(ctx, resp.ID)
	if err != nil {
		return ExecResult{}, err
	}
	return ExecResult{ExitCode: insp.ExitCode, Stdout: outBuf.Bytes(), Stderr: errBuf.Bytes()}, nil
}

func (r *realClient) VolumeList(ctx context.Context, prefix string) ([]string, error) {
	resp, err := r.c.VolumeList(ctx, volume.ListOptions{Filters: filters.NewArgs()})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range resp.Volumes {
		if prefix == "" || strings.HasPrefix(v.Name, prefix) {
			out = append(out, v.Name)
		}
	}
	return out, nil
}

func (r *realClient) ContainerLogs(ctx context.Context, name string, follow bool, w io.Writer) error {
	rc, err := r.c.ContainerLogs(ctx, name, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: follow,
	})
	if err != nil {
		return err
	}
	defer rc.Close()
	// Logs are stdcopy-multiplexed; demux to combined output.
	_, err = stdcopy.StdCopy(w, w, rc)
	return err
}

func (r *realClient) CopyFromContainer(ctx context.Context, name, srcPath string, w io.Writer) error {
	rc, _, err := r.c.CopyFromContainer(ctx, name, srcPath)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(w, rc)
	return err
}

// writeBuffer is a tiny buffer that is also a slice accessor.
type writeBuffer struct{ b []byte }

func (w *writeBuffer) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }
func (w *writeBuffer) Bytes() []byte               { return w.b }
func (w *writeBuffer) String() string              { return string(w.b) }
