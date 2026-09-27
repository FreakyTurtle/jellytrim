package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// healthcheck asks the running server for /healthz. The container image has
// no curl or wget, so the binary checks itself.
func healthcheck(listen string) int {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck: bad listen address:", err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/healthz", nil)
	if err != nil {
		return 1
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", res.StatusCode)
		return 1
	}
	return 0
}
