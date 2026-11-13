// Package unit supervises one child process: capture its output to a log file,
// report status, and stop it gracefully with a hard-kill fallback.
package unit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type Proc struct {
	mu        sync.Mutex
	logPath   string
	cmd       *exec.Cmd
	running   bool
	stopping  bool
	stdin     io.WriteCloser
	pid       int
	startedAt time.Time
	exitAt    time.Time
	lastErr   string
}

type Status struct {
	Running   bool   `json:"running"`
	PID       int    `json:"pid,omitempty"`
	StartedAt string `json:"startedAt,omitempty"`
	ExitAt    string `json:"exitAt,omitempty"`
	LastError string `json:"lastError,omitempty"`
}

func New(logPath string) *Proc {
	return &Proc{logPath: logPath}
}

func (p *Proc) Start(bin string, args []string, dir string) error {
	return p.StartEnv(bin, args, dir, nil)
}

// StartEnv starts the child with extra environment variables appended.
func (p *Proc) StartEnv(bin string, args []string, dir string, extraEnv []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return errors.New("process already running")
	}
	if err := os.MkdirAll(filepath.Dir(p.logPath), 0o755); err != nil {
		return err
	}
	lf, err := os.OpenFile(p.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	fmt.Fprintf(lf, "\n=== start %s: %s %v (cwd=%s) ===\n", time.Now().Format(time.RFC3339), filepath.Base(bin), args, dir)

	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdout = lf
	cmd.Stderr = lf
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		lf.Close()
		return err
	}
	cmd.SysProcAttr = sysProcAttr()
	if err := cmd.Start(); err != nil {
		stdin.Close()
		lf.Close()
		p.lastErr = err.Error()
		return err
	}

	p.cmd = cmd
	p.stdin = stdin
	p.pid = cmd.Process.Pid
	p.running = true
	p.stopping = false
	p.startedAt = time.Now()
	p.exitAt = time.Time{}
	p.lastErr = ""
	go p.wait(lf)
	return nil
}

func (p *Proc) wait(lf *os.File) {
	err := p.cmd.Wait()
	lf.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running = false
	p.stdin = nil
	p.exitAt = time.Now()
	if err != nil && !p.stopping {
		p.lastErr = err.Error()
	}
}

// Stop asks the process to exit, waits up to grace, then kills it.
func (p *Proc) Stop(grace time.Duration) error {
	p.mu.Lock()
	cmd := p.cmd
	if cmd == nil || !p.running {
		p.mu.Unlock()
		return nil
	}
	p.stopping = true
	p.mu.Unlock()

	_ = signalStop(cmd.Process.Pid)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !p.Running() {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = killTree(cmd.Process.Pid)
	_ = cmd.Process.Kill()
	for range 40 {
		if !p.Running() {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("process did not exit after kill")
}

// WriteStdin sends a console command (DST: "c_shutdown()") to the child.
func (p *Proc) WriteStdin(s string) error {
	p.mu.Lock()
	w := p.stdin
	p.mu.Unlock()
	if w == nil {
		return errors.New("process has no stdin")
	}
	_, err := io.WriteString(w, s)
	return err
}

func (p *Proc) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *Proc) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := Status{
		Running:   p.running,
		PID:       p.pid,
		LastError: p.lastErr,
	}
	if !p.startedAt.IsZero() {
		st.StartedAt = p.startedAt.Format(time.RFC3339)
	}
	if !p.exitAt.IsZero() {
		st.ExitAt = p.exitAt.Format(time.RFC3339)
	}
	return st
}

// CommandContext builds a hidden, tree-killable child command.
func CommandContext(ctx context.Context, bin string, args []string, dir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = sysProcAttr()
	return cmd
}
