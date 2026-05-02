package gastro

import (
	"log"
	"net/http"
)

// PageErrorHandler is the contract for user-supplied page render error
// handlers wired via WithErrorHandler.
//
// It is invoked by the generated page handler when template Execute
// returns an error. The error reported here is always a *render* error
// (template execution failure on a successfully-parsed page). Parse
// errors surface earlier — at New() in production mode (log.Fatal) and
// at request time in dev mode (re-parse failure).
//
// Signature shape mirrors http.HandlerFunc with an extra error tail so
// the contract is recognisable to anyone who has written stdlib HTTP
// middleware. The handler is responsible for the response: write headers,
// write a body, log, or stay silent — whatever fits the deployment.
//
// Body-write semantics. Template Execute streams output, so the response
// may already be partially committed when the error fires. Use
// HeaderCommitted(w) and BodyWritten(w) to decide whether a fresh status
// + body can still be written. DefaultErrorHandler shows the canonical
// pattern: write 500 only if the response is still uncommitted; log
// otherwise.
type PageErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

// DefaultErrorHandler is the page-render error handler used when no
// WithErrorHandler option is passed to New(). It logs the error and, if
// the response has not yet committed headers or a body, writes a 500
// Internal Server Error.
//
// Wave 4 / C4 (plans/frictions-plan.md §3 Wave 4): this is the published
// contract for the default behaviour. Production deployments can wrap or
// replace it via WithErrorHandler — for example to render a templated
// error page, emit a request ID, or report to an error tracker.
//
// Mirrors Recover's gating logic (recover.go): writing a 500 after the
// stream has already emitted bytes would interleave with whatever the
// template managed to flush, confusing the client and corrupting the
// page. Logging is always safe; writing is conditional.
func DefaultErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("gastro: page render failed for %s %s: %v", r.Method, r.URL.Path, err)

	if HeaderCommitted(w) || BodyWritten(w) {
		return
	}

	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}
