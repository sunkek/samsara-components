package fiber

import (
	swaggo "github.com/gofiber/contrib/v3/swaggo"
	gf "github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/static"
)

// SwaggerConfig configures the optional Swagger UI integration.
// Pass to [WithSwagger] to enable it.
type SwaggerConfig struct {
	// JSONPath is the filesystem path to the swagger.json file to serve.
	// Example: "./docs/swagger.json"
	JSONPath string

	// UIPath is the URL path under [Config.PathPrefix] where the Swagger UI
	// is mounted. Defaults to "/docs".
	UIPath string
}

func (s SwaggerConfig) uiPath() string {
	if s.UIPath != "" {
		return s.UIPath
	}
	return "/docs"
}

// WithSwagger enables the Swagger UI at [SwaggerConfig.UIPath] and serves the
// swagger.json spec from [SwaggerConfig.JSONPath].
//
// The UI is mounted as a standard [RegisterFunc], so it participates in the
// normal route registration lifecycle and is available immediately if the
// component is already running.
//
// Example:
//
//	srv := fiber.New(cfg)
//	fiber.WithSwagger(fiber.SwaggerConfig{
//	    JSONPath: "./docs/swagger.json",
//	})(srv)
//
// The UI is then available at http://host:port/api/docs.
func WithSwagger(cfg SwaggerConfig) Option {
	return func(c *Component) {
		jsonRoute := cfg.uiPath() + "/swagger.json"
		uiRoute := cfg.uiPath() + "/*"
		rootRedirect := "/"

		c.Register(func(r gf.Router) {
			// Serve the raw JSON spec.
			r.Use(jsonRoute, static.New(cfg.JSONPath))

			// Serve the Swagger UI, pointing it at our JSON endpoint.
			r.Get(uiRoute, swaggo.New(swaggo.Config{
				URL: c.cfg.pathPrefix() + jsonRoute,
			}))

			// Redirect root to the docs page.
			r.Get(rootRedirect, func(ctx gf.Ctx) error {
				return ctx.Redirect().To(c.cfg.pathPrefix() + cfg.uiPath())
			})
		})
	}
}
