// instruments/wiki.go
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Payload struct {
	Params map[string]string `json:"params"`
}

func main() {
	// 1. Parse JSON payload from stdin
	var pl Payload
	if err := json.NewDecoder(os.Stdin).Decode(&pl); err != nil {
		writeString("<h1>Error: invalid payload</h1>")
		return
	}

	// 2. Determine mode & page
	page := strings.ToLower(pl.Params["page"])
	if page == "" {
		page = "home"
	}
	if !isValidPage(page) {
		writeString("<h1>Error: invalid page name</h1>")
		return
	}
	listMode := pl.Params["list"] == "true"
	searchQ := pl.Params["search"]
	editMode := pl.Params["edit"] == "true"
	content, hasContent := pl.Params["content"]

	// 3. Ensure the host directory is there
	_ = os.MkdirAll("/wiki", 0o755)

	// 4. Load list of pages
	pages, err := listPages("/wiki")
	if err != nil {
		writeString("<h1>Error: cannot list pages</h1>")
		return
	}

	// 5. Save mode: write content and redirect to view
	path := filepath.Join("/wiki", page+".md")
	if hasContent {
		if err := ioutil.WriteFile(path, []byte(content), 0o644); err != nil {
			writeString("<h1>Error: cannot save page</h1>")
			return
		}
		// HTML redirect back to view
		writeString(fmt.Sprintf(`<!DOCTYPE html>
<meta http-equiv="refresh" content="0;url=/wiki?page=%s">`, page))
		return
	}

	// 6. Index mode: show all pages
	if listMode {
		renderIndex(page, pages)
		return
	}

	// 7. Search mode: filter pages by content
	if searchQ != "" {
		renderSearch(page, pages, searchQ)
		return
	}

	// 8. Edit mode: show textarea
	if editMode {
		var md string
		if b, err := ioutil.ReadFile(path); err == nil {
			md = string(b)
		}
		renderEditor(page, pages, md)
		return
	}

	// 9. View mode: render Markdown (very basic) or 404
	data, err := ioutil.ReadFile(path)
	if err != nil {
		writeString(fmt.Sprintf(`<h1>Page not found: %s</h1>
<p><a href="/wiki?page=%s&edit=true">Create it</a></p>`, page, page))
		return
	}
	renderPage(page, pages, string(data))
}

// isValidPage ensures page names are [a-z0-9_-]+
func isValidPage(name string) bool {
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// listPages returns sorted list of markdown filenames (without .md)
func listPages(dir string) ([]string, error) {
	fis, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var pages []string
	for _, fi := range fis {
		if fi.IsDir() || filepath.Ext(fi.Name()) != ".md" {
			continue
		}
		pages = append(pages, strings.TrimSuffix(fi.Name(), ".md"))
	}
	sort.Strings(pages)
	return pages, nil
}

// writeString is a small helper to write to stdout
func writeString(s string) {
	fmt.Fprint(os.Stdout, s)
}

// renderIndex displays all pages in a list
func renderIndex(current string, pages []string) {
	var b strings.Builder
	b.WriteString(renderHead("All Pages"))
	b.WriteString(`<div class="container-fluid"><div class="row">`)
	b.WriteString(renderSidebar(current, "", pages))
	b.WriteString(`<main class="col-md-9 ms-sm-auto col-lg-10 px-md-4 pt-4">
  <h1>All Pages</h1><ul>`)
	for _, p := range pages {
		b.WriteString(fmt.Sprintf(`<li><a href="/wiki?page=%s">%s</a></li>`, p, p))
	}
	b.WriteString(`</ul></main></div></div></body></html>`)
	writeString(b.String())
}

// renderSearch filters pages by content substring
func renderSearch(current string, pages []string, q string) {
	qLower := strings.ToLower(q)
	type result struct{ Page, Snippet string }
	var results []result

	for _, p := range pages {
		data, _ := ioutil.ReadFile("/wiki/" + p + ".md")
		lower := strings.ToLower(string(data))
		if idx := strings.Index(lower, qLower); idx >= 0 {
			start := idx - 30
			if start < 0 {
				start = 0
			}
			end := idx + len(qLower) + 30
			if end > len(data) {
				end = len(data)
			}
			snippet := html.EscapeString(string(data[start:end]))
			results = append(results, result{Page: p, Snippet: snippet})
		}
	}

	var b strings.Builder
	b.WriteString(renderHead("Search: " + q))
	b.WriteString(`<div class="container-fluid"><div class="row">`)
	b.WriteString(renderSidebar(current, q, pages))
	b.WriteString(`<main class="col-md-9 ms-sm-auto col-lg-10 px-md-4 pt-4">
  <h1>Search Results for “` + html.EscapeString(q) + `”</h1>`)
	if len(results) == 0 {
		b.WriteString(`<p>No matches found.</p>`)
	} else {
		for _, r := range results {
			b.WriteString(`<div class="mb-4"><h4><a href="/wiki?page=` + r.Page + `">` + r.Page + `</a></h4>`)
			b.WriteString(`<p>` + wikify(r.Snippet) + `…</p></div>`)
		}
	}
	b.WriteString(`</main></div></div></body></html>`)
	writeString(b.String())
}

// renderEditor shows a <textarea> pre‑populated with Markdown
func renderEditor(current string, pages []string, md string) {
	var b strings.Builder
	b.WriteString(renderHead("Edit "+current))
	b.WriteString(`<div class="container-fluid"><div class="row">`)
	b.WriteString(renderSidebar(current, "", pages))
	b.WriteString(`<main class="col-md-9 ms-sm-auto col-lg-10 px-md-4 pt-4">
  <h1>Edit “` + current + `”</h1>
  <form method="get" action="/wiki">
    <input type="hidden" name="page" value="` + current + `">
    <textarea name="content" class="form-control mb-3" rows="20">` +
		html.EscapeString(md) +
		`</textarea>
    <button class="btn btn-primary" type="submit">Save</button>
    <a class="btn btn-secondary ms-2" href="/wiki?page=` + current + `">Cancel</a>
  </form>
  </main></div></div></body></html>`)
	writeString(b.String())
}

// renderPage displays a Markdown file as simple HTML
func renderPage(current string, pages []string, md string) {
	var b strings.Builder
	b.WriteString(renderHead(current))
	b.WriteString(`<div class="container-fluid"><div class="row">`)
	b.WriteString(renderSidebar(current, "", pages))
	b.WriteString(`<main class="col-md-9 ms-sm-auto col-lg-10 px-md-4 pt-4">`)

	for _, line := range strings.Split(md, "\n") {
		switch {
		case strings.HasPrefix(line, "# "):
			b.WriteString("<h1>" + wikify(html.EscapeString(strings.TrimPrefix(line, "# "))) + "</h1>")
		case strings.HasPrefix(line, "## "):
			b.WriteString("<h2>" + wikify(html.EscapeString(strings.TrimPrefix(line, "## "))) + "</h2>")
		case strings.TrimSpace(line) == "":
			b.WriteString("<p></p>")
		default:
			b.WriteString("<p>" + wikify(html.EscapeString(line)) + "</p>")
		}
	}

	b.WriteString(`<p class="mt-4">
    <a class="btn btn-sm btn-outline-secondary" href="/wiki?page=` + current + `&edit=true">
      Edit this page
    </a>
  </p>`)
	b.WriteString(`</main></div></div></body></html>`)
	writeString(b.String())
}

// renderHead writes the HTML <head> with Bootstrap CSS
func renderHead(title string) string {
	return `<!DOCTYPE html>
<html lang="en"><head>
<meta charset="UTF-8">
<title>` + html.EscapeString(title) + `</title>
<link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.2/dist/css/bootstrap.min.css"
      rel="stylesheet" integrity="sha384-…"
      crossorigin="anonymous">
</head><body>`
}

// renderSidebar builds the Bootstrap sidebar with search and links
func renderSidebar(current, searchQ string, pages []string) string {
	var b strings.Builder
	b.WriteString(`<nav class="col-md-3 col-lg-2 d-md-block bg-light sidebar collapse pt-4">
  <div class="position-sticky px-3">
    <form class="mb-3" method="get" action="/wiki">
      <input type="hidden" name="page" value="` + current + `">
      <div class="input-group input-group-sm">
        <input type="text" name="search" class="form-control" placeholder="Search…" 
               value="` + html.EscapeString(searchQ) + `">
        <button class="btn btn-outline-secondary" type="submit">Go</button>
      </div>
    </form>
    <div class="mb-3">
      <a href="/wiki?list=true" class="btn btn-sm btn-outline-primary w-100 mb-1">All Pages</a>
      <a href="/wiki?edit=true&page=` + current + `" class="btn btn-sm btn-outline-success w-100">New / Edit</a>
    </div>
    <ul class="nav flex-column">`)
	for _, p := range pages {
		active := ""
		if p == current {
			active = " active fw-bold"
		}
		b.WriteString(`<li class="nav-item">
      <a class="nav-link` + active + `" href="/wiki?page=` + p + `">` + p + `</a>
    </li>`)
	}
	b.WriteString(`</ul></div></nav>`)
	return b.String()
}

// wikify turns [[Page]] into <a href="/wiki?page=page">Page</a>
func wikify(s string) string {
	var out strings.Builder
	for {
		i := strings.Index(s, "[[")
		j := strings.Index(s, "]]")
		if i < 0 || j < 0 || j < i {
			out.WriteString(s)
			break
		}
		out.WriteString(s[:i])
		title := s[i+2 : j]
		link := strings.ToLower(title)
		out.WriteString(`<a href="/wiki?page=` + link + `">` + html.EscapeString(title) + `</a>`)
		s = s[j+2:]
	}
	return out.String()
}
