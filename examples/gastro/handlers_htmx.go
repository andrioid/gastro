package main

import (
	"fmt"
	"net/http"
	"strings"

	gastro "gastro-website/.gastro"
	"gastro-website/demo"
)

// handleHtmxSearch handles HTMX search-as-you-type. Returns an HTML fragment
// containing the filtered guestbook list.
func handleHtmxSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	entries := demo.SearchEntries(query)

	html, err := renderEntryListHTML(entries, "htmx", "htmx-guestbook-list")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

// handleHtmxAdd handles HTMX form submission for adding a guestbook entry.
// Validates form data, adds the entry, and returns the updated list as an
// HTML fragment. On validation error, returns the form with an error message.
func handleHtmxAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	message := strings.TrimSpace(r.FormValue("message"))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if name == "" || message == "" {
		formHTML, err := gastro.Render.GuestbookForm(gastro.GuestbookFormProps{
			Error: "Both name and message are required.",
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Retarget to the form wrapper so the error shows in the form,
		// not in the guestbook list.
		w.Header().Set("HX-Retarget", "#guestbook-form-wrapper")
		w.Header().Set("HX-Reswap", "outerHTML")
		fmt.Fprint(w, formHTML)
		return
	}

	demo.AddEntry(name, message)

	listHTML, err := renderEntryListHTML(demo.ListEntries(), "htmx", "htmx-guestbook-list")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	formHTML, err := gastro.Render.GuestbookForm(gastro.GuestbookFormProps{
		Success: "Entry added!",
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Use hx-swap-oob to update both the list (primary target) and the
	// form (out-of-band). HTMX swaps the primary response into hx-target,
	// then processes any elements with hx-swap-oob="true" by their id.
	oobForm := strings.Replace(formHTML,
		`id="guestbook-form-wrapper"`,
		`id="guestbook-form-wrapper" hx-swap-oob="true"`,
		1)
	fmt.Fprint(w, listHTML+oobForm)
}

// handleHtmxEdit handles HTMX inline edit. Returns the edit form for a single
// guestbook entry as an HTML fragment that replaces the read-only row.
func handleHtmxEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	entry, ok := demo.GetEntry(id)
	if !ok {
		http.Error(w, "Entry not found", http.StatusNotFound)
		return
	}

	html, err := gastro.Render.GuestbookEntryEdit(gastro.GuestbookEntryEditProps{
		ID:      entry.ID,
		Name:    entry.Name,
		Message: entry.Message,
		Mode:    "htmx",
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

// handleHtmxSave handles HTMX inline edit save. Updates the entry and returns
// the updated read-only entry row as an HTML fragment.
func handleHtmxSave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	message := strings.TrimSpace(r.FormValue("message"))
	if message == "" {
		http.Error(w, "Message is required", http.StatusBadRequest)
		return
	}

	entry, ok := demo.UpdateEntry(id, message)
	if !ok {
		http.Error(w, "Entry not found", http.StatusNotFound)
		return
	}

	html, err := gastro.Render.GuestbookEntry(gastro.GuestbookEntryProps{
		ID:      entry.ID,
		Name:    entry.Name,
		Message: entry.Message,
		Time:    entry.CreatedAt.Format("Jan 2, 3:04 PM"),
		Mode:    "htmx",
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}
