package executor

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	posixpath "path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"ssh-bridge/internal/store"
)

const (
	maxOutputBytes  = 50 << 20
	maxPreviewBytes = 64 << 10
)

var errOutputLimit = errors.New("command output exceeded 50 MiB limit")

type Runner struct {
	Store     *store.Store
	OutputDir string
	Timeout   time.Duration
}

type eventWriter struct {
	mu      sync.Mutex
	w       *bufio.Writer
	started time.Time
	seq     int64
	size    int64
	preview strings.Builder
	limited atomic.Bool
}

type outputEvent struct {
	Sequence int64  `json:"sequence"`
	OffsetMS int64  `json:"offset_ms"`
	Stream   string `json:"stream"`
	Data     string `json:"data_base64"`
}

type streamWriter struct {
	parent *eventWriter
	stream string
}

func (w *streamWriter) Write(p []byte) (int, error) { return w.parent.write(w.stream, p) }

func (w *eventWriter) write(stream string, p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(p)) > maxOutputBytes {
		w.limited.Store(true)
		return 0, errOutputLimit
	}
	w.seq++
	e := outputEvent{Sequence: w.seq, OffsetMS: time.Since(w.started).Milliseconds(), Stream: stream, Data: base64.StdEncoding.EncodeToString(p)}
	line, err := json.Marshal(e)
	if err != nil {
		return 0, err
	}
	if _, err = w.w.Write(append(line, '\n')); err != nil {
		return 0, err
	}
	w.size += int64(len(p))
	if w.preview.Len() < maxPreviewBytes {
		remaining := maxPreviewBytes - w.preview.Len()
		chunk := p
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
		}
		if stream == "stderr" {
			w.preview.WriteString("[stderr] ")
		}
		w.preview.Write(chunk)
	}
	return len(p), nil
}

func fingerprintCallback(expected string) ssh.HostKeyCallback {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return ssh.InsecureIgnoreHostKey()
	}
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		actual := ssh.FingerprintSHA256(key)
		if actual != expected {
			return fmt.Errorf("host key mismatch: got %s", actual)
		}
		return nil
	}
}

func quotePOSIX(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func commandFor(e store.Execution) string {
	if e.WorkingDir == "" {
		return e.Command
	}
	return "cd -- " + quotePOSIX(e.WorkingDir) + " && " + e.Command
}

func (r *Runner) Run(ctx context.Context, e store.Execution, target store.Target, artifacts []store.Artifact) {
	dir := filepath.Join(r.OutputDir, e.CreatedAt.UTC().Format("2006/01/02"), e.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		r.fail(ctx, e.ID, "failed", nil, "create output directory: "+err.Error(), 0, "")
		return
	}
	path := filepath.Join(dir, "output.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		r.fail(ctx, e.ID, "failed", nil, "create output file: "+err.Error(), 0, "")
		return
	}
	w := &eventWriter{w: bufio.NewWriterSize(f, 64<<10), started: time.Now()}
	if err := r.Store.MarkRunning(ctx, e.ID, path); err != nil {
		f.Close()
		r.fail(ctx, e.ID, "failed", nil, "mark running: "+err.Error(), 0, "")
		return
	}

	status, exitCode, message := r.runSSH(ctx, e, target, artifacts, w)
	flushErr := w.w.Flush()
	closeErr := f.Close()
	if flushErr != nil && message == "" {
		status = "failed"
		message = "write output: " + flushErr.Error()
	}
	if closeErr != nil && message == "" {
		status = "failed"
		message = "close output: " + closeErr.Error()
	}
	r.fail(ctx, e.ID, status, exitCode, message, w.size, w.preview.String())
}

func (r *Runner) fail(ctx context.Context, id, status string, exitCode *int, message string, size int64, preview string) {
	_ = r.Store.FinishExecution(ctx, id, status, exitCode, message, size, preview)
}

func (r *Runner) runSSH(parent context.Context, e store.Execution, target store.Target, artifacts []store.Artifact, w *eventWriter) (string, *int, string) {
	ctx, cancel := context.WithTimeout(parent, r.Timeout)
	defer cancel()
	client, err := dialSSH(ctx, target)
	if err != nil {
		return "failed", nil, err.Error()
	}
	defer client.Close()
	command, cleanup, err := r.prepareArtifacts(client, e, artifacts)
	if err != nil {
		return "failed", nil, "upload input files: " + err.Error()
	}
	defer cleanup()
	session, err := client.NewSession()
	if err != nil {
		return "failed", nil, "create ssh session: " + err.Error()
	}
	defer session.Close()
	session.Stdout = &streamWriter{parent: w, stream: "stdout"}
	session.Stderr = &streamWriter{parent: w, stream: "stderr"}
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	select {
	case err := <-done:
		if w.limited.Load() {
			return "failed", nil, errOutputLimit.Error()
		}
		if err == nil {
			code := 0
			return "succeeded", &code, ""
		}
		var ee *ssh.ExitError
		if errors.As(err, &ee) {
			code := ee.ExitStatus()
			return "failed", &code, err.Error()
		}
		return "failed", nil, err.Error()
	case <-ctx.Done():
		client.Close()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "timeout", nil, "command timed out after " + r.Timeout.String()
		}
		return "failed", nil, ctx.Err().Error()
	}
}

func (r *Runner) prepareArtifacts(client *ssh.Client, e store.Execution, artifacts []store.Artifact) (string, func(), error) {
	if len(artifacts) == 0 {
		return commandFor(e), func() {}, nil
	}
	files, err := sftp.NewClient(client)
	if err != nil {
		return "", func() {}, fmt.Errorf("start SFTP: %w", err)
	}
	remoteDir := posixpath.Join("/tmp", "ssh-bridge-"+e.ID)
	uploaded := make([]string, 0, len(artifacts))
	cleanup := func() {
		for _, path := range uploaded {
			_ = files.Remove(path)
		}
		_ = files.RemoveDirectory(remoteDir)
		_ = files.Close()
	}
	if err := files.Mkdir(remoteDir); err != nil {
		_ = files.Close()
		return "", func() {}, fmt.Errorf("create remote temporary directory: %w", err)
	}
	if err := files.Chmod(remoteDir, 0o700); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("protect remote temporary directory: %w", err)
	}
	command := commandFor(e)
	for _, artifact := range artifacts {
		remotePath := posixpath.Join(remoteDir, artifact.ID)
		source, err := os.Open(artifact.LocalPath)
		if err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("open archived file %s: %w", artifact.OriginalName, err)
		}
		destination, err := files.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err != nil {
			source.Close()
			cleanup()
			return "", func() {}, fmt.Errorf("create remote file for %s: %w", artifact.OriginalName, err)
		}
		_, copyErr := io.Copy(destination, source)
		closeDestinationErr := destination.Close()
		closeSourceErr := source.Close()
		if copyErr != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("copy %s: %w", artifact.OriginalName, copyErr)
		}
		if closeDestinationErr != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("close remote file for %s: %w", artifact.OriginalName, closeDestinationErr)
		}
		if closeSourceErr != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("close archived file %s: %w", artifact.OriginalName, closeSourceErr)
		}
		uploaded = append(uploaded, remotePath)
		if err := files.Chmod(remotePath, 0o600); err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("protect remote file %s: %w", artifact.OriginalName, err)
		}
		if err := r.Store.MarkArtifactRemotePath(context.Background(), artifact.ID, remotePath); err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("record remote file %s: %w", artifact.OriginalName, err)
		}
		command = strings.ReplaceAll(command, "{{file:"+artifact.Placeholder+"}}", quotePOSIX(remotePath))
	}
	return command, cleanup, nil
}

func TestConnection(ctx context.Context, target store.Target, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := dialSSH(ctx, target)
	if err != nil {
		return err
	}
	return client.Close()
}

func dialSSH(ctx context.Context, target store.Target) (*ssh.Client, error) {
	var auth ssh.AuthMethod
	if target.AuthMethod == "password" {
		if target.Password == "" {
			return nil, errors.New("SSH password is not configured")
		}
		auth = ssh.Password(target.Password)
	} else {
		key, err := os.ReadFile(target.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("read private key: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		auth = ssh.PublicKeys(signer)
	}
	config := &ssh.ClientConfig{User: target.SSHUser, Auth: []ssh.AuthMethod{auth}, HostKeyCallback: fingerprintCallback(target.HostKeyFingerprint), Timeout: 10 * time.Second}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	netConn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target.Host, fmt.Sprint(target.Port)))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = netConn.SetDeadline(deadline)
	}
	conn, chans, reqs, err := ssh.NewClientConn(netConn, net.JoinHostPort(target.Host, fmt.Sprint(target.Port)), config)
	if err != nil {
		netConn.Close()
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}
	_ = netConn.SetDeadline(time.Time{})
	client := ssh.NewClient(conn, chans, reqs)
	return client, nil
}

func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
