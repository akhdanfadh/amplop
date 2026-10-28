package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Direct clients cannot choose their address through forwarding headers.
func TestProxyDefault(t *testing.T) {
	a := New(discard, Services{})
	a.router.GET("/client-ip", func(ctx *gin.Context) {
		ctx.String(http.StatusOK, ctx.ClientIP())
	})
	request := httptest.NewRequest(http.MethodGet, "/client-ip", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("X-Forwarded-For", "198.51.100.20")
	request.Header.Set("X-Real-IP", "198.51.100.30")
	response := httptest.NewRecorder()

	a.Handler().ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "192.0.2.10", response.Body.String())
}

func TestProxy(t *testing.T) {
	cases := []struct {
		name      string
		proxies   []string
		remote    string
		forwarded []string
		realIP    string
		want      string
	}{
		{
			name: "empty setting", proxies: []string{}, remote: "192.0.2.10:1234",
			forwarded: []string{"198.51.100.20"}, realIP: "198.51.100.30", want: "192.0.2.10",
		},
		{
			name: "default ignores real IP", remote: "192.0.2.10:1234",
			realIP: "198.51.100.30", want: "192.0.2.10",
		},
		{
			name: "trusted IPv4", proxies: []string{"10.0.0.0/8"}, remote: "10.1.2.3:1234",
			forwarded: []string{"198.51.100.20"}, realIP: "198.51.100.30", want: "198.51.100.20",
		},
		{
			name: "untrusted peer", proxies: []string{"10.0.0.0/8"}, remote: "192.0.2.10:1234",
			forwarded: []string{"198.51.100.20"}, realIP: "198.51.100.30", want: "192.0.2.10",
		},
		{
			name: "trusted chain", proxies: []string{"10.0.0.0/8"}, remote: "10.1.2.3:1234",
			forwarded: []string{"198.51.100.20, 10.2.3.4"}, want: "198.51.100.20",
		},
		{
			name: "untrusted hop stops spoofing", proxies: []string{"10.0.0.0/8"}, remote: "10.1.2.3:1234",
			forwarded: []string{"198.51.100.99, 192.0.2.10, 10.2.3.4"}, want: "192.0.2.10",
		},
		{
			name: "repeated headers", proxies: []string{"10.0.0.0/8"}, remote: "10.1.2.3:1234",
			forwarded: []string{"198.51.100.99", "192.0.2.10, 10.2.3.4"}, want: "192.0.2.10",
		},
		{
			name: "invalid forwarded address", proxies: []string{"10.0.0.0/8"}, remote: "10.1.2.3:1234",
			forwarded: []string{"invalid"}, want: "10.1.2.3",
		},
		{
			name: "real IP fallback", proxies: []string{"10.0.0.0/8"}, remote: "10.1.2.3:1234",
			realIP: "198.51.100.30", want: "198.51.100.30",
		},
		{
			name: "trusted IPv6", proxies: []string{"2001:db8:1::/48"}, remote: "[2001:db8:1::1]:1234",
			forwarded: []string{"2001:db8:2::1"}, want: "2001:db8:2::1",
		},
		{
			name: "untrusted IPv6", proxies: []string{"10.0.0.0/8"}, remote: "[2001:db8:1::1]:1234",
			forwarded: []string{"198.51.100.20"}, want: "2001:db8:1::1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := New(discard, Services{})
			if c.proxies != nil {
				require.NoError(t, a.SetTrustedProxies(c.proxies))
			}
			a.router.GET("/client-ip", func(ctx *gin.Context) {
				ctx.String(http.StatusOK, ctx.ClientIP())
			})
			request := httptest.NewRequest(http.MethodGet, "/client-ip", nil)
			request.RemoteAddr = c.remote
			for _, value := range c.forwarded {
				request.Header.Add("X-Forwarded-For", value)
			}
			request.Header.Set("X-Real-IP", c.realIP)
			response := httptest.NewRecorder()

			a.Handler().ServeHTTP(response, request)

			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, c.want, response.Body.String())
		})
	}
}
