package fiber

import gf "github.com/gofiber/fiber/v3"

// RealIP returns the client's real IP address. It prefers the X-Forwarded-For
// header (set by reverse proxies) over the direct connection IP.
//
// Only use X-Forwarded-For if your service sits behind a trusted proxy. If
// clients can send arbitrary headers, use c.IP() directly instead.
func RealIP(c gf.Ctx) string {
	if xff := c.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return c.IP()
}

// SkipperFunc is a predicate that returns true when a middleware should be
// skipped for the current request. Used with [ExcludeRoutes].
type SkipperFunc func(c gf.Ctx) bool

// ExcludeRoutes returns a [SkipperFunc] that skips middleware for all routes
// except those in the allow-list. Useful for applying middleware to most
// routes while excluding a specific set (e.g. public endpoints).
//
// Example — apply auth middleware to everything except /health and /metrics:
//
//	skipper := fiber.ExcludeRoutes(
//	    fiber.Route{Method: "GET", Path: "/api/health"},
//	    fiber.Route{Method: "GET", Path: "/api/metrics"},
//	)
//	srv.Use(func(c gf.Ctx) error {
//	    if skipper(c) {
//	        return c.Next()
//	    }
//	    return authMiddleware(c)
//	})
func ExcludeRoutes(routes ...Route) SkipperFunc {
	return func(c gf.Ctx) bool {
		for _, r := range routes {
			if c.Method() == r.Method && c.Path() == r.Path {
				return false // this route is in the allow-list — do not skip
			}
		}
		return true // skip middleware for all other routes
	}
}

// Route identifies an HTTP endpoint by method and path.
// Used with [ExcludeRoutes].
type Route struct {
	Method string
	Path   string
}
