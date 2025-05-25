// instruments/wiki.go
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

/* ---------- Configuration ---------- */

const (
	defaultWikiDir = "/wiki"
	maxPageBytes   = 1 << 20 // 1 MiB
)

/* ---------- Input payload ---------- */

// Payload is the JSON structure received on STDIN.
type Payload struct {
	Params map[string]string `json:"params"`
}

/* ---------- main ---------- */

func main() {
	// 0. Decode JSON payload.
	var pl Payload
	if err := json.NewDecoder(os.Stdin).Decode(&pl); err != nil {
		writeString("<h1>Error: invalid payload</h1>")
		return
	}

	/* ----- Set-up paths & flags ----- */

	wikiDir := getenv("WIKI_DIR", defaultWikiDir)
	readOnly := os.Getenv("WIKI_READONLY") == "1"

	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		writeString("<h1>Error: cannot create wiki directory</h1>")
		return
	}

	// Route parameters.
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
	tagQ := strings.ToLower(pl.Params["tag"])
	editMode := pl.Params["edit"] == "true" && !readOnly
	deleteMode := pl.Params["delete"] == "true" && !readOnly
	renameMode := pl.Params["rename"] == "true" && !readOnly
	newName := strings.ToLower(pl.Params["new"]) // for rename
	theme := pl.Params["theme"]                  // dark | light | ""

	content, hasContent := pl.Params["content"]

	// Load all page names + metadata once.
	pages, recent, tags, err := listPages(wikiDir)
	if err != nil {
		writeString("<h1>Error: cannot list pages</h1>")
		return
	}

	pagePath := filepath.Join(wikiDir, page+".md")

	/* ---------- Mutating operations ---------- */

	// Save (write) content.
	if hasContent && !readOnly {
		if len(content) > maxPageBytes {
			writeString("<h1>Error: content too large</h1>")
			return
		}
		if err := os.WriteFile(pagePath, []byte(content), 0o644); err != nil {
			writeString("<h1>Error: cannot save page</h1>")
			return
		}
		redirect(fmt.Sprintf("/wiki?page=%s&theme=%s", page, theme))
		return
	}

	// Delete.
	if deleteMode {
		if rmErr := os.Remove(pagePath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			writeString("<h1>Error: cannot delete page</h1>")
			return
		}
		redirect(fmt.Sprintf("/wiki?list=true&theme=%s", theme))
		return
	}

	// Rename.
	if renameMode {
		if !isValidPage(newName) {
			writeString("<h1>Error: invalid new page name</h1>")
			return
		}
		dst := filepath.Join(wikiDir, newName+".md")
		if err := os.Rename(pagePath, dst); err != nil {
			writeString("<h1>Error: cannot rename page</h1>")
			return
		}
		redirect(fmt.Sprintf("/wiki?page=%s&theme=%s", newName, theme))
		return
	}

	/* ---------- Read-only operations ---------- */

	switch {
	case listMode:
		renderIndex(page, pages, recent, theme, readOnly)
		return
	case tagQ != "":
		renderTagIndex(tagQ, pages, tags, theme, readOnly)
		return
	case searchQ != "":
		renderSearch(page, pages, searchQ, wikiDir, theme, readOnly)
		return
	case editMode:
		var md string
		if b, err := os.ReadFile(pagePath); err == nil {
			md = string(b)
		}
		renderEditor(page, pages, md, theme)
		return
	default:
		data, err := os.ReadFile(pagePath)
		if err != nil {
			writeString(fmt.Sprintf(`<h1>Page not found: %s</h1>
<p><a href="/wiki?page=%s&edit=true&theme=%s">Create it</a></p>`,
				page, page, theme))
			return
		}
		renderPage(page, pages, string(data), wikiDir, theme, readOnly)
	}
}

/* ---------- Helpers ---------- */

// getenv returns env value or default.
func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// redirect writes a minimal HTML redirect.
func redirect(to string) {
	writeString(fmt.Sprintf(`<!DOCTYPE html><meta http-equiv="refresh" content="0;url=%s">`, to))
}

// writeString writes directly to stdout (TinyGo-friendly).
func writeString(s string) { fmt.Fprint(os.Stdout, s) }

/* ---------- File system utilities ---------- */

// isValidPage allows [a-z0-9_-] (60 chars max).
func isValidPage(name string) bool {
	if name == "" || len(name) > 60 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' ||
			r >= '0' && r <= '9' ||
			r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// listPages returns:
//
//	pages: sorted slice of names,
//	recent: map[name]mtime,
//	tags: map[tag][]page
func listPages(dir string) ([]string, map[string]time.Time, map[string][]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	var pages []string
	recent := make(map[string]time.Time)
	tags := make(map[string][]string)

	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".md")
		pages = append(pages, base)

		// mtime
		if info, err := e.Info(); err == nil {
			recent[base] = info.ModTime()
		}

		// tags: read first 1 kB only.
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		buf := make([]byte, 1024)
		n, _ := f.Read(buf)
		_ = f.Close()
		firstLines := strings.Split(string(buf[:n]), "\n")
		if len(firstLines) > 0 && strings.HasPrefix(strings.ToLower(firstLines[0]), "tags:") {
			line := strings.TrimSpace(firstLines[0][5:])
			for _, t := range strings.Split(line, ",") {
				tag := strings.ToLower(strings.TrimSpace(t))
				if tag != "" {
					tags[tag] = append(tags[tag], base)
				}
			}
		}
	}
	sort.Strings(pages)
	return pages, recent, tags, nil
}

/* ---------- HTML renderers ---------- */

// renderHead returns basic <head> with optional dark theme & user CSS.
func renderHead(title, theme string, includeCustomCSS bool) string {
	bootstrap := "https://cdn.jsdelivr.net/npm/bootstrap@5.3.2/dist/css/bootstrap.min.css"
	if theme == "dark" {
		bootstrap = "https://cdn.jsdelivr.net/npm/bootswatch@5.3.2/dist/darkly/bootstrap.min.css"
	}
	head := `<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><title>` +
		html.EscapeString(title) + `</title><link rel="stylesheet" href="` + bootstrap + `">`
	if includeCustomCSS {
		if css, err := os.ReadFile(filepath.Join(getenv("WIKI_DIR", defaultWikiDir), "_style.css")); err == nil {
			head += "<style>" + string(css) + "</style>"
		}
	}
	head += `</head><body>`
	return head
}

/* ----- Sidebar (common) ----- */

func renderSidebar(current, searchQ, tagQ, theme string, pages []string, readOnly bool) string {
	var b strings.Builder
	b.WriteString(`<nav class="col-md-3 col-lg-2 d-md-block bg-light sidebar collapse pt-4">
  <div class="position-sticky px-3">`)

	// Search form
	b.WriteString(`<form class="mb-3" method="get" action="/wiki">
    <input type="hidden" name="page" value="` + current + `">
    <input type="hidden" name="theme" value="` + theme + `">
    <div class="input-group input-group-sm">
      <input type="text" name="search" class="form-control" placeholder="Search…" value="` + html.EscapeString(searchQ) + `">
      <button class="btn btn-outline-secondary" type="submit">Go</button>
    </div></form>`)

	// Tag form
	b.WriteString(`<form class="mb-3" method="get" action="/wiki">
    <input type="hidden" name="theme" value="` + theme + `">
    <div class="input-group input-group-sm">
      <input type="text" name="tag" class="form-control" placeholder="Tag…" value="` + html.EscapeString(tagQ) + `">
      <button class="btn btn-outline-secondary" type="submit">List</button>
    </div></form>`)

	// Quick links
	b.WriteString(`<div class="mb-3">`)
	b.WriteString(`<a href="/wiki?list=true&theme=` + theme + `" class="btn btn-sm btn-outline-primary w-100 mb-1">All Pages</a>`)
	if !readOnly {
		b.WriteString(`<a href="/wiki?edit=true&page=` + current + `&theme=` + theme + `" class="btn btn-sm btn-outline-success w-100">New / Edit</a>`)
	}
	b.WriteString(`</div><ul class="nav flex-column">`)

	for _, p := range pages {
		active := ""
		if p == current {
			active = " active fw-bold"
		}
		b.WriteString(`<li class="nav-item"><a class="nav-link` + active + `" href="/wiki?page=` + p + `&theme=` + theme + `">` + p + `</a></li>`)
	}
	b.WriteString(`</ul></div></nav>`)
	return b.String()
}

/* ----- Index page ----- */

func renderIndex(current string, pages []string, recent map[string]time.Time, theme string, readOnly bool) {
	var b strings.Builder
	b.WriteString(renderHead("All Pages", theme, true))
	b.WriteString(`<div class="container-fluid"><div class="row">`)
	b.WriteString(renderSidebar(current, "", "", theme, pages, readOnly))

	// main
	b.WriteString(`<main class="col-md-9 ms-sm-auto col-lg-10 px-md-4 pt-4">
<h1>All Pages</h1><ul>`)

	for _, p := range pages {
		b.WriteString(linkWithOps(p, theme, readOnly))
	}
	b.WriteString(`</ul><h5 class="mt-4">Recent</h5><ul>`)

	type pt struct {
		Name string
		T    time.Time
	}
	var rec []pt
	for n, t := range recent {
		rec = append(rec, pt{n, t})
	}
	sort.Slice(rec, func(i, j int) bool { return rec[i].T.After(rec[j].T) })
	for i := 0; i < len(rec) && i < 5; i++ {
		b.WriteString(`<li><a href="/wiki?page=` + rec[i].Name + `&theme=` + theme +
			`">` + rec[i].Name + `</a> <span class="text-muted small">` +
			rec[i].T.Format("2006-01-02 15:04") + `</span></li>`)
	}
	b.WriteString(`</ul></main></div></div></body></html>`)
	writeString(b.String())
}

/* ----- Tag index ----- */

func renderTagIndex(tag string, pages []string, tags map[string][]string, theme string, readOnly bool) {
	tag = strings.ToLower(tag)
	list := tags[tag]

	var b strings.Builder
	b.WriteString(renderHead("Tag: "+tag, theme, true))
	b.WriteString(`<div class="container-fluid"><div class="row">`)
	b.WriteString(renderSidebar("", "", tag, theme, pages, readOnly))

	b.WriteString(`<main class="col-md-9 ms-sm-auto col-lg-10 px-md-4 pt-4">
  <h1>Tag “` + html.EscapeString(tag) + `”</h1>`)
	if len(list) == 0 {
		b.WriteString("<p>No pages with this tag.</p>")
	} else {
		b.WriteString("<ul>")
		for _, p := range list {
			b.WriteString(`<li><a href="/wiki?page=` + p + `&theme=` + theme + `">` + p + `</a></li>`)
		}
		b.WriteString("</ul>")
	}
	b.WriteString(`</main></div></div></body></html>`)
	writeString(b.String())
}

/* ----- Search ----- */

func renderSearch(current string, pages []string, q, dir, theme string, readOnly bool) {
	qLower := strings.ToLower(q)
	type result struct{ Page, Snippet string }
	var results []result

	for _, p := range pages {
		data, _ := os.ReadFile(filepath.Join(dir, p+".md"))
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
			results = append(results, result{p, snippet})
		}
	}

	var b strings.Builder
	b.WriteString(renderHead("Search: "+q, theme, true))
	b.WriteString(`<div class="container-fluid"><div class="row">`)
	b.WriteString(renderSidebar(current, q, "", theme, pages, readOnly))

	b.WriteString(`<main class="col-md-9 ms-sm-auto col-lg-10 px-md-4 pt-4">
  <h1>Search “` + html.EscapeString(q) + `”</h1>`)
	if len(results) == 0 {
		b.WriteString("<p>No matches.</p>")
	} else {
		for _, r := range results {
			b.WriteString(`<div class="mb-3"><h5><a href="/wiki?page=` + r.Page + `&theme=` + theme + `">` +
				r.Page + `</a></h5><p>` + wikify(r.Snippet) + `…</p></div>`)
		}
	}
	b.WriteString(`</main></div></div></body></html>`)
	writeString(b.String())
}

/* ----- Editor ----- */

func renderEditor(current string, pages []string, md, theme string) {
	var b strings.Builder
	b.WriteString(renderHead("Edit "+current, theme, true))
	b.WriteString(`<div class="container-fluid"><div class="row">`)
	b.WriteString(renderSidebar(current, "", "", theme, pages, false))
	b.WriteString(`<main class="col-md-9 ms-sm-auto col-lg-10 px-md-4 pt-4">
<h1>Edit “` + current + `”</h1>
<form method="get" action="/wiki">
  <input type="hidden" name="page" value="` + current + `">
  <input type="hidden" name="theme" value="` + theme + `">
  <textarea name="content" class="form-control mb-3" rows="20">` +
		html.EscapeString(md) + `</textarea>
  <button class="btn btn-primary" type="submit">Save</button>
  <a class="btn btn-secondary ms-2" href="/wiki?page=` + current + `&theme=` + theme + `">Cancel</a>
</form>
</main></div></div></body></html>`)
	writeString(b.String())
}

/* ----- Page view + backlinks ----- */

func renderPage(current string, pages []string, md, dir, theme string, readOnly bool) {
	backlinks := findBacklinks(current, pages, dir)

	var b strings.Builder
	b.WriteString(renderHead(current, theme, true))
	b.WriteString(`<div class="container-fluid"><div class="row">`)
	b.WriteString(renderSidebar(current, "", "", theme, pages, readOnly))

	b.WriteString(`<main class="col-md-9 ms-sm-auto col-lg-10 px-md-4 pt-4">`)
	// Quick title (first "# " wins).
	title := extractTitle(md, current)
	b.WriteString(`<h1>` + wikify(html.EscapeString(title)) + `</h1>`)

	// Render body (still trivial).
	for _, line := range strings.Split(md, "\n") {
		switch {
		case strings.HasPrefix(line, "# "):
			// skip (used as title)
		case strings.HasPrefix(line, "## "):
			b.WriteString("<h2>" + wikify(html.EscapeString(strings.TrimPrefix(line, "## "))) + "</h2>")
		case strings.TrimSpace(line) == "":
			b.WriteString("<p></p>")
		default:
			b.WriteString("<p>" + wikify(html.EscapeString(line)) + "</p>")
		}
	}

	// Backlinks
	if len(backlinks) > 0 {
		b.WriteString("<h5 class=\"mt-4\">Linked from</h5><ul>")
		for _, p := range backlinks {
			b.WriteString(`<li><a href="/wiki?page=` + p + `&theme=` + theme + `">` + p + `</a></li>`)
		}
		b.WriteString("</ul>")
	}

	// Ops
	if !readOnly {
		b.WriteString(fmt.Sprintf(`<p class="mt-4">
<a class="btn btn-sm btn-outline-secondary" href="/wiki?page=%s&edit=true&theme=%s">Edit</a>
<a class="btn btn-sm btn-outline-warning ms-2" href="/wiki?page=%s&rename=true&new=&theme=%s"
   onclick="var n=prompt('New name for %s:'); if(n){location.href='/wiki?page=%s&rename=true&new='+encodeURIComponent(n)+'&theme=%s'}; return false;">
   Rename
</a>
<a class="btn btn-sm btn-outline-danger ms-2" href="/wiki?page=%s&delete=true&theme=%s"
   onclick="return confirm('Delete page %s?')">Delete</a>
</p>`, current, theme, current, theme, current, current, theme, current, theme, current))
	}
	b.WriteString(`</main></div></div></body></html>`)
	writeString(b.String())
}

// extractTitle returns first H1 or fallback.
func extractTitle(md, fallback string) string {
	for _, l := range strings.Split(md, "\n") {
		if strings.HasPrefix(l, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(l, "# "))
		}
	}
	return fallback
}

// findBacklinks scans all pages for [[current]].
func findBacklinks(current string, pages []string, dir string) []string {
	var list []string
	target := "[[" + current + "]]"
	for _, p := range pages {
		if p == current {
			continue
		}
		// small read: max 64 kB
		data, _ := os.ReadFile(filepath.Join(dir, p+".md"))
		if strings.Contains(strings.ToLower(string(data)), strings.ToLower(target)) {
			list = append(list, p)
		}
	}
	sort.Strings(list)
	return list
}

/* ---------- Utility ---------- */

// linkWithOps builds list item with (edit/delete) icons.
func linkWithOps(p, theme string, readOnly bool) string {
	var b strings.Builder
	b.WriteString(`<li><a href="/wiki?page=` + p + `&theme=` + theme + `">` + p + `</a>`)
	if !readOnly {
		b.WriteString(` <a href="/wiki?page=` + p + `&edit=true&theme=` + theme + `" title="Edit">&#9998;</a>`)
		b.WriteString(` <a href="/wiki?page=` + p + `&delete=true&theme=` + theme + `" onclick="return confirm('Delete page ` + p + `?')" title="Delete">&#128465;</a>`)
	}
	b.WriteString("</li>")
	return b.String()
}

// wikify replaces [[Page]] → link.
func wikify(s string) string {
	for {
		i := strings.Index(s, "[[")
		j := strings.Index(s, "]]")
		if i < 0 || j < 0 || j < i {
			break
		}
		title := s[i+2 : j]
		link := strings.ToLower(title)
		s = s[:i] + `<a href="/wiki?page=` + link + `">` + html.EscapeString(title) + `</a>` + s[j+2:]
	}
	return s
}
