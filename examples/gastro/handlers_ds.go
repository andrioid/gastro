package main

import (
	"encoding/json"
	"net/http"
	"strings"

	gastro "gastro-website/.gastro"
	"gastro-website/demo"

	"github.com/andrioid/gastro/pkg/gastro/datastar"
)

// dsSignals represents the JSON blob Datastar sends. For @get requests it
// arrives in the ?datastar= query parameter. For @post with the default JSON
// content type it arrives in the request body.
type dsSignals struct {
	Search  string `json:"search"`
	Message string `json:"message"`
}

// parseDsSignalsFromQuery extracts Datastar signals from the ?datastar= query parameter.
func parseDsSignalsFromQuery(r *http.Request) dsSignals {
	raw := r.URL.Query().Get("datastar")
	var s dsSignals
	if raw != "" {
		json.Unmarshal([]byte(raw), &s)
	}
	return s
}

// parseDsSignalsFromBody extracts Datastar signals from the JSON request body.
func parseDsSignalsFromBody(r *http.Request) (dsSignals, error) {
	var s dsSignals
	err := json.NewDecoder(r.Body).Decode(&s)
	return s, err
}

// handleDsSearch handles Datastar search-as-you-type. Reads the search query
// from Datastar signals and returns the filtered guestbook list as an SSE patch.
func handleDsSearch(w http.ResponseWriter, r *http.Request) {
	signals := parseDsSignalsFromQuery(r)
	entries := demo.SearchEntries(signals.Search)

	html, err := renderEntryListHTML(entries, "ds", "guestbook-list")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sse := datastar.NewSSE(w, r)
	sse.PatchElements(html)
}

// handleDsAdd handles Datastar form submission for adding a guestbook entry.
// Uses contentType 'form' on the client, so form data arrives as standard
// form-encoded fields. Validates input, adds the entry, and patches both
// the entry list and the form.
func handleDsAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	message := strings.TrimSpace(r.FormValue("message"))

	sse := datastar.NewSSE(w, r)

	if name == "" || message == "" {
		errorMsg := "Both name and message are required."
		formHTML, err := gastro.Render.GuestbookForm(gastro.GuestbookFormProps{
			Error: errorMsg,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sse.PatchElements(formHTML)
		return
	}

	demo.AddEntry(name, message)

	listHTML, err := renderEntryListHTML(demo.ListEntries(), "ds", "guestbook-list")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sse.PatchElements(listHTML)

	formHTML, err := gastro.Render.GuestbookForm(gastro.GuestbookFormProps{
		Success: "Entry added!",
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sse.PatchElements(formHTML)
}

// handleDsEdit handles Datastar inline edit. Returns the edit form for
// a single guestbook entry, replacing the read-only entry via SSE patch.
func handleDsEdit(w http.ResponseWriter, r *http.Request) {
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
		Mode:    "ds",
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sse := datastar.NewSSE(w, r)
	sse.PatchElements(html)
}

// handleDsSave handles Datastar inline edit save. The client sends signals
// as a JSON body (Datastar's default content type). Updates the entry message
// and returns the updated read-only entry row via SSE patch.
func handleDsSave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	signals, err := parseDsSignalsFromBody(r)
	if err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	message := strings.TrimSpace(signals.Message)
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
		Mode:    "ds",
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sse := datastar.NewSSE(w, r)
	sse.PatchElements(html)
}
