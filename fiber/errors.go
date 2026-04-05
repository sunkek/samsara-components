package fiber

import (
	"errors"
	"net/http"

	gf "github.com/gofiber/fiber/v3"
)

// ErrorResponse is the JSON shape returned by [DefaultErrorHandler] for all
// error responses.
//
// Callers that supply a custom [Config.ErrorHandler] may use any response
// shape they prefer.
type ErrorResponse struct {
	// Error is the human-readable error message.
	Error string `json:"error"`
}

// DefaultErrorHandler maps errors to HTTP status codes and writes a JSON
// [ErrorResponse] body.
//
// Mapping rules (evaluated in order):
//  1. [*gf.Error] — uses the error's own Code field.
//  2. HTTPStatuser — any error implementing StatusCode() int is honoured.
//  3. Anything else — 500 Internal Server Error.
//
// To integrate your own error library, supply a custom [gf.ErrorHandler] via
// [Config.ErrorHandler] and call [DefaultErrorHandler] as a fallback:
//
//	cfg.ErrorHandler = func(c gf.Ctx, err error) error {
//	    var myErr *myapp.Error
//	    if errors.As(err, &myErr) {
//	        return c.Status(myErr.HTTPStatus()).JSON(fiber.ErrorResponse{Error: myErr.Error()})
//	    }
//	    return fiber.DefaultErrorHandler(c, err)
//	}
func DefaultErrorHandler(c gf.Ctx, err error) error {
	body := ErrorResponse{Error: err.Error()}

	// Fiber's own error type carries an explicit HTTP status code.
	var fe *gf.Error
	if errors.As(err, &fe) {
		return c.Status(fe.Code).JSON(body)
	}

	// Any error that knows its own HTTP status code.
	var se HTTPStatuser
	if errors.As(err, &se) {
		return c.Status(se.StatusCode()).JSON(body)
	}

	return c.Status(http.StatusInternalServerError).JSON(body)
}

// HTTPStatuser is an optional interface errors can implement to supply their
// own HTTP status code. [DefaultErrorHandler] checks for this before falling
// back to 500.
//
// Example implementation:
//
//	type NotFoundError struct{ Resource string }
//	func (e *NotFoundError) Error() string      { return e.Resource + " not found" }
//	func (e *NotFoundError) StatusCode() int    { return http.StatusNotFound }
type HTTPStatuser interface {
	StatusCode() int
}
