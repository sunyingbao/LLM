package handler

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"code.byted.org/middleware/hertz/pkg/app"
	hertzserver "code.byted.org/middleware/hertz/pkg/app/server"
)

func TestCancelOnCloseCancelsStreamContextAndClosesPipe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	body := cancelOnClose(pr, cancel)

	if err := body.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stream context was not canceled")
	}

	if _, err := pw.Write([]byte("event: queue\n\n")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PipeWriter.Write() error = %v, want %v", err, io.ErrClosedPipe)
	}

	if err := body.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestHertzSSEBodyStreamCloseCancelsBackendOnClientDisconnect(t *testing.T) {
	addr := freeTCPAddr(t)
	h := hertzserver.New(hertzserver.WithHostPorts(addr), hertzserver.WithSenseClientDisconnection(true))
	t.Cleanup(func() {
		_ = h.Close()
	})

	bodyClosed := make(chan struct{})
	streamCanceled := make(chan struct{})
	h.GET("/ready", func(context.Context, *app.RequestContext) {})
	h.GET("/sse", func(ctx context.Context, c *app.RequestContext) {
		streamCtx, cancel := context.WithCancel(ctx)
		pr, pw := io.Pipe()
		c.SetContentType("text/event-stream; charset=utf-8")
		c.Response.Header.Set("Cache-Control", "no-cache")
		c.Response.ImmediateHeaderFlush = true
		c.SetBodyStream(&closeNotifyReadCloser{
			ReadCloser: cancelOnClose(pr, cancel),
			onClose: func() {
				close(bodyClosed)
			},
		}, -1)
		go func() {
			defer pw.Close()
			if _, err := pw.Write([]byte("event: queue\ndata: {}\n\n")); err != nil {
				return
			}
			<-streamCtx.Done()
			close(streamCanceled)
		}()
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- h.Run()
	}()
	t.Cleanup(func() {
		_ = h.Close()
		select {
		case <-errCh:
		case <-time.After(time.Second):
			t.Fatal("hertz server did not stop")
		}
	})
	waitHTTPReady(t, "http://"+addr+"/ready")

	resp, err := http.Get("http://" + addr + "/sse")
	if err != nil {
		t.Fatalf("GET /sse: %v", err)
	}
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			_ = resp.Body.Close()
			t.Fatalf("read SSE handshake: %v", err)
		}
		if line == "\n" || line == "\r\n" {
			break
		}
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}

	select {
	case <-bodyClosed:
	case <-time.After(time.Second):
		t.Fatal("hertz did not close the SSE body stream after client disconnect")
	}
	select {
	case <-streamCanceled:
	case <-time.After(time.Second):
		t.Fatal("SSE backend context was not canceled after body stream close")
	}
}

func freeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close probe listener: %v", err)
	}
	return addr
}

type closeNotifyReadCloser struct {
	io.ReadCloser
	once    sync.Once
	onClose func()
}

func (r *closeNotifyReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(func() {
		if r.onClose != nil {
			r.onClose()
		}
	})
	return err
}

func waitHTTPReady(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not ready: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
