package fiber

// Register adds a [RegisterFunc] that will be called during [Component.Start]
// with the root [gf.Router] scoped to [Config.PathPrefix].
//
// It is safe to call Register before or after Start. If the component is
// already running, the RegisterFunc is applied immediately on the live app —
// Fiber supports hot route registration. On the next restart all registered
// funcs are re-applied in order.
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

	// If Start has already been called, apply immediately on the live app.
	c.mu.RLock()
	app := c.app
	c.mu.RUnlock()

	if app != nil {
		root := app.Group(c.cfg.pathPrefix())
		fn(root)
	}
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
func (c *Component) Use(args ...any) {
	c.middlewareMu.Lock()
	c.middleware = append(c.middleware, args...)
	c.middlewareMu.Unlock()
}
