package main

import (
	"strings"

	gastro "gastro-website/.gastro"
	"gastro-website/demo"
)

// renderEntryListHTML renders a list of guestbook entries as an HTML string
// wrapped in the guestbook-list container div. The mode parameter ("ds",
// "htmx", or "") controls which interaction attributes are added to entries.
// The listID parameter sets the id of the wrapper div.
func renderEntryListHTML(entries []demo.Entry, mode string, listID string) (string, error) {
	if len(entries) == 0 {
		return `<div id="` + listID + `" class="guestbook-list" role="list" aria-live="polite" aria-label="Guestbook entries"><p class="guestbook-empty">No entries found.</p></div>`, nil
	}

	var b strings.Builder
	b.WriteString(`<div id="` + listID + `" class="guestbook-list" role="list" aria-live="polite" aria-label="Guestbook entries">`)

	for _, e := range entries {
		html, err := gastro.Render.GuestbookEntry(gastro.GuestbookEntryProps{
			ID:      e.ID,
			Name:    e.Name,
			Message: e.Message,
			Time:    e.CreatedAt.Format("Jan 2, 3:04 PM"),
			Mode:    mode,
		})
		if err != nil {
			return "", err
		}
		b.WriteString(html)
	}

	b.WriteString(`</div>`)
	return b.String(), nil
}
