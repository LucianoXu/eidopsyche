package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
)

type Handler interface {
	Handle(ctx context.Context, conn *Conn, req *Request) (any, *Error)
}

type Conn struct {
	rw        net.Conn
	mu        sync.Mutex
	encoder   *json.Encoder
	closed    chan struct{}
	closeOnce sync.Once
}

func (c *Conn) PushEvent(event string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.encoder.Encode(Event{Event: event, Data: raw})
}

func (c *Conn) Closed() <-chan struct{} { return c.closed }

func (c *Conn) close() {
	c.closeOnce.Do(func() { close(c.closed) })
	c.rw.Close()
}

type Server struct {
	socket  string
	handler Handler
	ln      net.Listener
}

func NewServer(socket string, h Handler) *Server {
	return &Server{socket: socket, handler: h}
}

func (s *Server) Listen() error {
	if err := os.RemoveAll(s.socket); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", s.socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.socket, 0o600); err != nil {
		ln.Close()
		return err
	}
	s.ln = ln
	return nil
}

func (s *Server) Serve(ctx context.Context) error {
	defer s.ln.Close()
	go func() {
		<-ctx.Done()
		s.ln.Close()
	}()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handleConn(ctx, c)
	}
}

func (s *Server) handleConn(ctx context.Context, raw net.Conn) {
	conn := &Conn{
		rw:      raw,
		encoder: json.NewEncoder(raw),
		closed:  make(chan struct{}),
	}
	defer conn.close()
	dec := json.NewDecoder(bufio.NewReader(raw))
	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			if !errors.Is(err, io.EOF) {
				_ = writeErr(conn, 0, ErrInvalidRequest, err.Error())
			}
			return
		}
		go func(r Request) {
			result, ipcErr := s.handler.Handle(ctx, conn, &r)
			if ipcErr != nil {
				_ = writeErr(conn, r.ID, ipcErr.Code, ipcErr.Message)
				return
			}
			_ = writeOK(conn, r.ID, result)
		}(req)
	}
}

func writeOK(c *Conn, id int64, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.encoder.Encode(Response{ID: id, Result: raw})
}

func writeErr(c *Conn, id int64, code, msg string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.encoder.Encode(Response{ID: id, Error: &Error{Code: code, Message: msg}})
}
