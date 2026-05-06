package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

type Client struct {
	conn    net.Conn
	enc     *json.Encoder
	dec     *json.Decoder
	mu      sync.Mutex
	nextID  atomic.Int64
	pending map[int64]chan *Response
	events  chan Event
	errCh   chan error
	closed  chan struct{}
}

func Dial(socket string) (*Client, error) {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, err
	}
	c := &Client{
		conn:    conn,
		enc:     json.NewEncoder(conn),
		dec:     json.NewDecoder(bufio.NewReader(conn)),
		pending: make(map[int64]chan *Response),
		events:  make(chan Event, 64),
		errCh:   make(chan error, 1),
		closed:  make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *Client) Close() error {
	close(c.closed)
	return c.conn.Close()
}

func (c *Client) Events() <-chan Event { return c.events }

func (c *Client) Errors() <-chan error { return c.errCh }

func (c *Client) Call(method string, params any, result any) (*Error, error) {
	id := c.nextID.Add(1)
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	ch := make(chan *Response, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	c.mu.Lock()
	err := c.enc.Encode(Request{ID: id, Method: method, Params: raw})
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error, nil
		}
		if result != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return nil, fmt.Errorf("decode result: %w", err)
			}
		}
		return nil, nil
	case <-c.closed:
		return nil, errors.New("client closed")
	}
}

func (c *Client) readLoop() {
	for {
		var env struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result,omitempty"`
			Error  *Error          `json:"error,omitempty"`
			Event  string          `json:"event,omitempty"`
			Data   json.RawMessage `json:"data,omitempty"`
		}
		if err := c.dec.Decode(&env); err != nil {
			c.errCh <- err
			return
		}
		if env.Event != "" {
			c.events <- Event{Event: env.Event, Data: env.Data}
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[env.ID]
		c.mu.Unlock()
		if ok {
			ch <- &Response{ID: env.ID, Result: env.Result, Error: env.Error}
		}
	}
}
