package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"net"

	_ "github.com/lib/pq"
	"golang.org/x/crypto/ssh"
)

// rejectExec, returned by a handler, makes the agent refuse the exec request.
const rejectExec = "\x00reject"

func sqlOpen(dsn string) (*sql.DB, error) { return sql.Open("postgres", dsn) }

// pipeSession returns the client end of a session channel on a loopback
// SSH connection whose server side plays an agent: exec runs handle, shell
// greets and echoes input until the client closes.
func pipeSession(handle func(cmd string, stdin []byte) (string, uint32)) (ssh.Channel, <-chan *ssh.Request, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	hostKey, _ := ssh.NewSignerFromKey(priv)
	serverCfg := &ssh.ServerConfig{NoClientAuth: true}
	serverCfg.AddHostKey(hostKey)

	// Loopback TCP rather than net.Pipe: both SSH peers send their version
	// line before reading, which deadlocks on an unbuffered pipe.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	clientSide, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		ln.Close()
		return nil, nil, err
	}
	serverSide, err := ln.Accept()
	ln.Close()
	if err != nil {
		clientSide.Close()
		return nil, nil, err
	}
	go func() {
		_, chans, reqs, err := ssh.NewServerConn(serverSide, serverCfg)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		for nc := range chans {
			ch, chReqs, err := nc.Accept()
			if err != nil {
				return
			}
			go servePipeSession(ch, chReqs, handle)
		}
	}()

	conn, chans, reqs, err := ssh.NewClientConn(clientSide, "agent", &ssh.ClientConfig{
		User: "gateway", HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		return nil, nil, err
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		for nc := range chans {
			_ = nc.Reject(ssh.Prohibited, "no")
		}
	}()
	ch, chReqs, err := conn.OpenChannel("session", nil)
	if err != nil {
		return nil, nil, err
	}
	return &closingChannel{Channel: ch, conn: conn}, chReqs, nil
}

// closingChannel tears the in-memory connection down with the channel.
type closingChannel struct {
	ssh.Channel
	conn ssh.Conn
}

func (c *closingChannel) Close() error {
	err := c.Channel.Close()
	_ = c.conn.Close()
	return err
}

func servePipeSession(ch ssh.Channel, reqs <-chan *ssh.Request, handle func(string, []byte) (string, uint32)) {
	defer ch.Close()
	for req := range reqs {
		switch req.Type {
		case "pty-req", "window-change":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		case "exec":
			var p struct{ Command, User string }
			_ = ssh.Unmarshal(req.Payload, &p)
			if handle == nil {
				_ = req.Reply(false, nil)
				continue
			}
			out, status := handle(p.Command, nil)
			if out == rejectExec {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			_, _ = io.WriteString(ch, out)
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], status)
			_, _ = ch.SendRequest("exit-status", false, b[:])
			return
		case "shell":
			_ = req.Reply(true, nil)
			go func() {
				fmt.Fprint(ch, "welcome\r\n")
				buf := make([]byte, 256)
				for {
					n, err := ch.Read(buf)
					if n > 0 {
						fmt.Fprintf(ch, "echo:%s", buf[:n])
					}
					if err != nil {
						_ = ch.Close()
						return
					}
				}
			}()
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}
