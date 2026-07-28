package fiber

import (
	"testing"

	gf "github.com/gofiber/fiber/v3"
)

// Each Use call must be stored as its own group. Flattening them into one slice
// makes fiber read a string argument in ANY call as the path prefix for EVERY
// handler in the flattened list — so one path-scoped Use (serving a file, say)
// silently re-scopes auth/audit/tracing to that path and leaves the rest of the
// API unguarded. That is a security bug, not a cosmetic one, which is why the
// grouping is asserted directly.
func TestUseStoresOneGroupPerCall(t *testing.T) {
	c := New(Config{Port: 0})

	c.Use(func(ctx gf.Ctx) error { return ctx.Next() })
	c.Use("/api/v1/docs/file.json", func(ctx gf.Ctx) error { return ctx.Next() })
	c.Use(func(ctx gf.Ctx) error { return ctx.Next() }, func(ctx gf.Ctx) error { return ctx.Next() })
	c.Use() // no-op: an empty call must not add a group

	c.middlewareMu.RLock()
	defer c.middlewareMu.RUnlock()

	if len(c.middleware) != 3 {
		t.Fatalf("stored %d groups, want 3 (one per non-empty Use call)", len(c.middleware))
	}
	if len(c.middleware[0]) != 1 || len(c.middleware[1]) != 2 || len(c.middleware[2]) != 2 {
		t.Fatalf("group shapes = %d/%d/%d, want 1/2/2",
			len(c.middleware[0]), len(c.middleware[1]), len(c.middleware[2]))
	}
	if _, ok := c.middleware[1][0].(string); !ok {
		t.Error("the path argument did not stay with its own Use call")
	}
	for i, group := range c.middleware {
		if i == 1 {
			continue // the only call that legitimately carries a path
		}
		for _, arg := range group {
			if _, ok := arg.(string); ok {
				t.Errorf("a path argument leaked into group %d", i)
			}
		}
	}
}
