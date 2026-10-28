package httpapi

import (
	"net/netip"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
)

// resolveClientAddr parses the address Gin selected for throttling.
func resolveClientAddr(ctx huma.Context, addr *netip.Addr) []error {
	parsed, err := netip.ParseAddr(humagin.Unwrap(ctx).ClientIP())
	if err != nil {
		return []error{huma.Error500InternalServerError("Cannot determine the client address.", err)}
	}
	*addr = parsed
	return nil
}
