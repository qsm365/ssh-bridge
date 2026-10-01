package executor

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"ssh-bridge/internal/store"
)

func TestPasswordSSHConnection(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	config := &ssh.ServerConfig{PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if meta.User() == "operator" && string(password) == "correct-password" {
			return nil, nil
		}
		return nil, errors.New("invalid credentials")
	}}
	config.AddHostKey(signer)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				serverConn, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer serverConn.Close()
				go ssh.DiscardRequests(requests)
				for channel := range channels {
					if channel.ChannelType() != "session" {
						channel.Reject(ssh.Prohibited, "unsupported")
						continue
					}
					active, channelRequests, err := channel.Accept()
					if err != nil {
						return
					}
					go func() {
						defer active.Close()
						for request := range channelRequests {
							if request.Type != "exec" {
								request.Reply(false, nil)
								continue
							}
							request.Reply(true, nil)
							active.Write([]byte("command output\n"))
							active.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							return
						}
					}()
				}
			}()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	target := store.Target{Host: "127.0.0.1", Port: port, SSHUser: "operator", AuthMethod: "password", Password: "correct-password", HostKeyFingerprint: ssh.FingerprintSHA256(signer.PublicKey())}
	if err := TestConnection(context.Background(), target, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := &eventWriter{w: bufio.NewWriter(&output), started: time.Now()}
	runner := Runner{Timeout: 3 * time.Second}
	status, exitCode, message := runner.runSSH(context.Background(), store.Execution{Command: "echo test"}, target, nil, writer)
	if status != "succeeded" || exitCode == nil || *exitCode != 0 || message != "" || !bytes.Contains([]byte(writer.preview.String()), []byte("command output")) {
		t.Fatalf("password command failed: status=%s, exit=%v, message=%s", status, exitCode, message)
	}
	target.Password = "wrong-password"
	if err := TestConnection(context.Background(), target, 3*time.Second); err == nil {
		t.Fatal("wrong password accepted")
	}
}
