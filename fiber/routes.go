package fiber

// Register adds a [RegisterFunc] that will be called during [Component.Start]
// with the root [gf.Router] scoped to [Config.PathPrefix].
//
// Register must be called before [samsara.Application.Run]. All registered
// funcs are applied in registration order when Start builds the Fiber app.
// On each restart they are re-applied automatically.
//
// Example:
//
//	srv.Register(func(r gf.Router) {
//	    r.Get("/users", handleGetUsers)
//	    r.Post("/users", handleCreateUser)
//
//	    // Sub-groups work naturally:
//	    v2 := r.Group("/v2")
//	    v2.Get("/users", handleGetUsersV2)
//	})
func (c *Component) Register(fn RegisterFunc) {
	c.routesMu.Lock()
	c.routes = append(c.routes, fn)
	c.routesMu.Unlock()
}

// Use adds global middleware that is applied to all domain routes after the
// built-in middleware stack (recover, CORS, security headers, compress) but
// before domain route handlers.
//
// Typical use: authentication, distributed tracing, rate limiting.
//
// Must be called before [Component.Start]. Calling Use after Start has no
// effect on the current run, but the middleware will be applied on restart.
//
// Example:
//
//	srv.Use(authMiddleware, tracingMiddleware)
//
// Each call is stored as its own group and replayed as its own app.Use call at
// Start. Flattening them into one slice would be a security bug, not just a
// cosmetic one: fiber's Use treats a string argument as the path prefix for every
// handler in that call, so a single path-scoped Use — Use("/docs/x", static) —
// would silently re-scope every other global middleware (auth, audit, tracing) to
// that one path, leaving the rest of the API unguarded.
func (c *Component) Use(args ...any) {
	if len(args) == 0 {
		return
	}
	group := make([]any, len(args))
	copy(group, args)

	c.middlewareMu.Lock()
	c.middleware = append(c.middleware, group)
	c.middlewareMu.Unlock()
}
