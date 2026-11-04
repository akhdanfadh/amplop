package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"inbok/internal/config"
)

// healthTimeout bounds one check.
//
// NOTE: It stays below the image's HEALTHCHECK timeout, so a slow server
// reports unhealthy instead of the check being killed.
const healthTimeout = 3 * time.Second

// health requests the health operation on the address serve listens on,
// giving up after timeout. Returns nil if it answers 200, or an error
// naming what failed.
func health(ctx context.Context, timeout time.Duration) error {
	addr, err := config.HTTPAddr()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// An all-interfaces address such as 0.0.0.0:8080 dials this machine,
	// as net.Dial documents; Go falls back from [::] to 0.0.0.0 itself.
	url := "http://" + addr + "/api/v0/health"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// A plain transport ignores HTTP_PROXY, which Docker can set in a
	// container: the check must reach this server, not a proxy.
	client := &http.Client{Transport: &http.Transport{}}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health check: %s answered HTTP %d", url, response.StatusCode)
	}
	return nil
}

// workerHealth returns a handler answering every request with 200 and no
// body, including the one health sends.
func workerHealth() http.Handler {
	// The listener exists only for health checks, so nothing needs routing.
	return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
}
