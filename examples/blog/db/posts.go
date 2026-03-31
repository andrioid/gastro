package db

import (
	"fmt"
	"time"
)

type Post struct {
	Slug      string
	Title     string
	Body      string
	Author    string
	CreatedAt time.Time
	Draft     bool
}

// FullName returns the author attribution string.
func (p Post) FullName() string {
	return "By " + p.Author
}

func ListPublished() ([]Post, error) {
	return []Post{
		{
			Slug:      "hello-world",
			Title:     "Hello World",
			Body:      "<p>Welcome to my blog. This is my first post built with Gastro.</p>",
			Author:    "Ada",
			CreatedAt: time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			Slug:      "go-is-great",
			Title:     "Go Is Great",
			Body:      "<p>Here's why I like Go: it's simple, fast, and deploys as a single binary.</p>",
			Author:    "Ada",
			CreatedAt: time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC),
		},
	}, nil
}

func GetBySlug(slug string) (Post, error) {
	posts, _ := ListPublished()
	for _, p := range posts {
		if p.Slug == slug {
			return p, nil
		}
	}
	return Post{}, fmt.Errorf("post not found: %s", slug)
}
