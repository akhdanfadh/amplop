package httpapi

import (
	"net/netip"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"

	"inbok/internal/session"
)

// resolveClientAddr parses the address Gin selected using its proxy rules.
func resolveClientAddr(ctx huma.Context, addr *netip.Addr) []error {
	parsed, err := netip.ParseAddr(humagin.Unwrap(ctx).ClientIP())
	if err != nil {
		return []error{huma.Error500InternalServerError("Cannot determine the client address.", err)}
	}
	*addr = parsed
	return nil
}

// resolveDevice reads the details a new session keeps about its browser:
// the User-Agent header and the address resolveClientAddr selects.
// The session feature cleans and cuts them before storage.
func resolveDevice(ctx huma.Context, device *session.Device) []error {
	device.UserAgent = ctx.Header("User-Agent")
	return resolveClientAddr(ctx, &device.Addr)
}
