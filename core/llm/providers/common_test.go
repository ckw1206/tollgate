package providers

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
)

// An upstream that drops a keep-alive connection the client reuses (uvicorn closes idle
// connections after 5s) must not surface as a 502: the POST is retried on a fresh connection.
func TestPost_RetriesOnStaleKeepAlive(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	sawKey := make(chan bool, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				for n := 0; ; n++ {
					req, err := http.ReadRequest(br)
					if err != nil {
						return
					}
					io.Copy(io.Discard, req.Body)
					_, has := req.Header["X-Idempotency-Key"]
					sawKey <- has
					if n == 1 {
						return // second request on this connection: close without answering
					}
					io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}")
				}
			}(c)
		}
	}()

	c := &http.Client{Transport: &http.Transport{}}
	url := "http://" + ln.Addr().String() + "/v1/chat/completions"
	for i := range 2 {
		resp, err := post(context.Background(), c, url, nil, []byte(`{"model":"m"}`))
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	for range 3 { // first, dropped, retried
		if <-sawKey {
			t.Fatal("X-Idempotency-Key must not be sent upstream")
		}
	}
}
