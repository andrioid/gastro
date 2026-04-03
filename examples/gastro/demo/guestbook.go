package demo

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	ID        string
	Name      string
	Message   string
	CreatedAt time.Time
}

var (
	mu      sync.RWMutex
	entries []Entry
	nextID  int
)

func init() {
	entries = []Entry{
		{ID: "1", Name: "Remy", Message: "Anyone can cook... and anyone can build web apps with Gastro!", CreatedAt: time.Now().Add(-2 * time.Hour)},
		{ID: "2", Name: "Linguini", Message: "I don't really know what I'm doing, but the file-based routing makes it easy.", CreatedAt: time.Now().Add(-1 * time.Hour)},
		{ID: "3", Name: "Colette", Message: "If you can read, you can cook. If you know Go, you can use Gastro.", CreatedAt: time.Now().Add(-30 * time.Minute)},
	}
	nextID = 4
}

func AddEntry(name, message string) Entry {
	mu.Lock()
	defer mu.Unlock()

	entry := Entry{
		ID:        strconv.Itoa(nextID),
		Name:      name,
		Message:   message,
		CreatedAt: time.Now(),
	}
	nextID++
	entries = append(entries, entry)
	return entry
}

func UpdateEntry(id, message string) (Entry, bool) {
	mu.Lock()
	defer mu.Unlock()

	for i, e := range entries {
		if e.ID == id {
			entries[i].Message = message
			return entries[i], true
		}
	}
	return Entry{}, false
}

func DeleteEntry(id string) bool {
	mu.Lock()
	defer mu.Unlock()

	for i, e := range entries {
		if e.ID == id {
			entries = append(entries[:i], entries[i+1:]...)
			return true
		}
	}
	return false
}

func GetEntry(id string) (Entry, bool) {
	mu.RLock()
	defer mu.RUnlock()

	for _, e := range entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

func ListEntries() []Entry {
	mu.RLock()
	defer mu.RUnlock()

	result := make([]Entry, len(entries))
	copy(result, entries)

	// Newest first
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

func SearchEntries(query string) []Entry {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return ListEntries()
	}

	mu.RLock()
	defer mu.RUnlock()

	var result []Entry
	// Iterate in reverse for newest-first ordering
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if strings.Contains(strings.ToLower(e.Name), q) || strings.Contains(strings.ToLower(e.Message), q) {
			result = append(result, e)
		}
	}
	return result
}
