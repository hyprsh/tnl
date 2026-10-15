package harness

import (
	"io"
	"net"
	"testing"
)

// Service is a TCP service on the host's loopback, standing in for something
// the Outpost's host can reach.
type Service struct {
	ln net.Listener
}

// NewEchoService starts a Service that echoes every byte back.
func NewEchoService(t testing.TB) *Service {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				io.Copy(c, c)
			}()
		}
	}()
	return &Service{ln: ln}
}

// Addr is the Service's host:port.
func (s *Service) Addr() string { return s.ln.Addr().String() }
