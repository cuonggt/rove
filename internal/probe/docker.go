package probe

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"strings"

	rexec "github.com/cuonggt/rove/internal/exec"
	"github.com/cuonggt/rove/internal/model"
)

//go:embed docker.sh
var DockerScript string

const dockerHeader = "rove-docker "

// RunDocker detects a container runtime and lists what it is running.
func RunDocker(ctx context.Context, ex rexec.Executor, t model.Target) (model.ContainerList, error) {
	out, err := runScript(ctx, ex, t, DockerScript)
	if err != nil {
		return model.ContainerList{}, err
	}
	return ParseDocker(out)
}

// ParseDocker reads the container contract. Container fields are tab
// separated because an image reference, a status phrase and a port mapping
// all contain spaces.
func ParseDocker(out []byte) (model.ContainerList, error) {
	var l model.ContainerList

	body, err := scanHeader(out, dockerHeader)
	if err != nil {
		return l, err
	}

	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "docker.cli":
			l.CLI = val
		case "docker.source":
			l.Source = val
		case "docker.version":
			l.Version = val
		case "docker.error":
			l.Err = val
		case "container":
			if c, ok := parseContainer(val); ok {
				l.Containers = append(l.Containers, c)
			}
		}
	}
	return l, sc.Err()
}

func parseContainer(val string) (model.Container, bool) {
	f := strings.Split(val, "\t")
	if len(f) < 5 || f[0] == "" {
		return model.Container{}, false
	}
	c := model.Container{
		ID:     f[0],
		State:  f[1],
		Name:   f[2],
		Image:  f[3],
		Status: f[4],
	}
	if len(f) > 5 {
		c.Ports = f[5]
	}
	// Older runtimes have no State field; the status phrase still says it.
	if c.State == "" {
		switch {
		case strings.HasPrefix(c.Status, "Up"):
			c.State = "running"
		case strings.HasPrefix(c.Status, "Exited"):
			c.State = "exited"
		case strings.HasPrefix(c.Status, "Created"):
			c.State = "created"
		case strings.HasPrefix(c.Status, "Paused"):
			c.State = "paused"
		}
	}
	return c, true
}
