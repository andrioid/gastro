package main

import (
	"net/http"
	"strings"

	"gastro-website/demo"
)

// handleGuestbookPost handles traditional HTML form submissions using
// the Post/Redirect/Get pattern. Validates input, adds an entry,
// and redirects back to the forms page with status query parameters.
func handleGuestbookPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/docs/forms?error=Invalid+form+data", http.StatusSeeOther)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	message := strings.TrimSpace(r.FormValue("message"))

	if name == "" {
		http.Redirect(w, r, "/docs/forms?error=Name+is+required", http.StatusSeeOther)
		return
	}
	if message == "" {
		http.Redirect(w, r, "/docs/forms?error=Message+is+required", http.StatusSeeOther)
		return
	}

	demo.AddEntry(name, message)
	http.Redirect(w, r, "/docs/forms?success=Entry+added", http.StatusSeeOther)
}
