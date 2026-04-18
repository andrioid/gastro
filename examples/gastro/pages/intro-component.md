```gastro
---
type Props struct {
    Title  string
    Author string
}

p := gastro.Props()
Title := p.Title
Author := p.Author
---
<article>
    <h2>{{ .Title }}</h2>
    <p>By {{ .Author }}</p>
</article>
```