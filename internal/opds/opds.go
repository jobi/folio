package opds

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/folio-books/folio/internal/config"
	"github.com/folio-books/folio/internal/db"
)

// Handler serves OPDS 1.2 Atom XML feeds and OPDS 2.0 JSON feeds.
type Handler struct {
	Store *db.Store
	Cfg   *config.Config
}

func NewHandler(store *db.Store, cfg *config.Config) *Handler {
	return &Handler{Store: store, Cfg: cfg}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/opds", h.handleRoot)
	mux.HandleFunc("/opds/", h.handleSubroutes)
}

func (h *Handler) baseURL(r *http.Request) string {
	if h.Cfg.BaseURL != "" {
		return h.Cfg.BaseURL
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}
	if host == "" {
		host = fmt.Sprintf("localhost:%d", h.Cfg.Port)
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

// Atom XML structs for OPDS 1.2
type AtomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Xmlns   string      `xml:"xmlns,attr"`
	XmlnsDC string      `xml:"xmlns:dc,attr,omitempty"`
	XmlnsOPDS string    `xml:"xmlns:opds,attr,omitempty"`
	ID      string      `xml:"id"`
	Title   string      `xml:"title"`
	Updated string      `xml:"updated"`
	Author  *AtomAuthor `xml:"author,omitempty"`
	Links   []AtomLink  `xml:"link"`
	Entries []AtomEntry `xml:"entry"`
}

type AtomAuthor struct {
	Name string `xml:"name"`
}

type AtomLink struct {
	Rel   string `xml:"rel,attr,omitempty"`
	Href  string `xml:"href,attr"`
	Type  string `xml:"type,attr,omitempty"`
	Title string `xml:"title,attr,omitempty"`
}

type AtomEntry struct {
	ID         string         `xml:"id"`
	Title      string         `xml:"title"`
	Updated    string         `xml:"updated"`
	Authors    []AtomAuthor   `xml:"author,omitempty"`
	Summary    *AtomText      `xml:"summary,omitempty"`
	Content    *AtomText      `xml:"content,omitempty"`
	Language   string         `xml:"dc:language,omitempty"`
	Publisher  string         `xml:"dc:publisher,omitempty"`
	Issued     string         `xml:"dc:issued,omitempty"`
	Identifier string         `xml:"dc:identifier,omitempty"`
	Categories []AtomCategory `xml:"category,omitempty"`
	Links      []AtomLink     `xml:"link"`
}

type AtomText struct {
	Type string `xml:"type,attr,omitempty"`
	Body string `xml:",chardata"`
}

type AtomCategory struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr"`
}

const (
	mimeOPDSNav = "application/atom+xml;profile=opds-catalog;kind=navigation"
	mimeOPDSAcq = "application/atom+xml;profile=opds-catalog;kind=acquisition"
	mimeEPUB    = "application/epub+zip"
)

func (h *Handler) handleSubroutes(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimPrefix(r.URL.Path, "/opds")
	sub = strings.Trim(sub, "/")

	switch sub {
	case "":
		h.handleRoot(w, r)
	case "opensearch.xml":
		h.handleOpenSearch(w, r)
	case "recent":
		h.handleAcquisitionFeed(w, r, "Recent Additions", "urn:folio:opds:recent", "recent")
	case "books", "all":
		h.handleAcquisitionFeed(w, r, "All Books", "urn:folio:opds:books", "title")
	case "search":
		q := r.URL.Query().Get("q")
		h.handleAcquisitionFeed(w, r, fmt.Sprintf("Search Results: %s", q), "urn:folio:opds:search", "title")
	case "authors":
		h.handleFacetFeed(w, r, "authors")
	case "series":
		h.handleFacetFeed(w, r, "series")
	case "tags":
		h.handleFacetFeed(w, r, "tags")
	case "v2":
		h.handleOPDS2(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)
	nowISO := time.Now().UTC().Format(time.RFC3339)

	feed := AtomFeed{
		Xmlns:   "http://www.w3.org/2005/Atom",
		ID:      "urn:folio:opds:root",
		Title:   "Folio EPUB Library",
		Updated: nowISO,
		Author:  &AtomAuthor{Name: "Folio Self-Hosted Library"},
		Links: []AtomLink{
			{Rel: "self", Href: base + "/opds", Type: mimeOPDSNav},
			{Rel: "start", Href: base + "/opds", Type: mimeOPDSNav},
			{Rel: "search", Href: base + "/opds/opensearch.xml", Type: "application/opensearchdescription+xml", Title: "Search Folio Library"},
			{Rel: "search", Href: base + "/opds/search?q={searchTerms}", Type: mimeOPDSAcq, Title: "Search Books"},
		},
		Entries: []AtomEntry{
			{
				ID:      "urn:folio:opds:recent",
				Title:   "Recently Added",
				Updated: nowISO,
				Content: &AtomText{Type: "text", Body: "Latest EPUB books added to the library"},
				Links: []AtomLink{
					{Rel: "http://opds-spec.org/sort/new", Href: base + "/opds/recent", Type: mimeOPDSAcq},
				},
			},
			{
				ID:      "urn:folio:opds:books",
				Title:   "All Books (A–Z)",
				Updated: nowISO,
				Content: &AtomText{Type: "text", Body: "Browse the complete EPUB catalog alphabetically"},
				Links: []AtomLink{
					{Rel: "subsection", Href: base + "/opds/books", Type: mimeOPDSAcq},
				},
			},
			{
				ID:      "urn:folio:opds:authors",
				Title:   "By Author",
				Updated: nowISO,
				Content: &AtomText{Type: "text", Body: "Browse books grouped by author"},
				Links: []AtomLink{
					{Rel: "subsection", Href: base + "/opds/authors", Type: mimeOPDSNav},
				},
			},
			{
				ID:      "urn:folio:opds:series",
				Title:   "By Series",
				Updated: nowISO,
				Content: &AtomText{Type: "text", Body: "Browse book series in reading order"},
				Links: []AtomLink{
					{Rel: "subsection", Href: base + "/opds/series", Type: mimeOPDSNav},
				},
			},
			{
				ID:      "urn:folio:opds:tags",
				Title:   "By Subject & Tag",
				Updated: nowISO,
				Content: &AtomText{Type: "text", Body: "Browse books by genre and subject tags"},
				Links: []AtomLink{
					{Rel: "subsection", Href: base + "/opds/tags", Type: mimeOPDSNav},
				},
			},
		},
	}

	writeAtomXML(w, feed)
}

func (h *Handler) handleOpenSearch(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)
	w.Header().Set("Content-Type", "application/opensearchdescription+xml; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
  <ShortName>Folio Library</ShortName>
  <Description>Search EPUB books in Folio Library</Description>
  <InputEncoding>UTF-8</InputEncoding>
  <OutputEncoding>UTF-8</OutputEncoding>
  <Url type="%s" template="%s/opds/search?q={searchTerms}"/>
</OpenSearchDescription>`, mimeOPDSAcq, base)
}

func (h *Handler) buildDownloadHref(base string, b *db.Book, r *http.Request) string {
	raw := fmt.Sprintf("%s/api/books/%d/download/%s", base, b.ID, url.PathEscape(b.FileName))
	optParam := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("optimize")))
	if optParam == "0" || optParam == "false" || optParam == "off" {
		return raw
	}
	vals := url.Values{}
	if optParam != "" {
		vals.Set("optimize", optParam)
	} else {
		vals.Set("optimize", "crosspoint")
	}
	if prof := strings.TrimSpace(r.URL.Query().Get("profile")); prof != "" {
		vals.Set("profile", prof)
	}
	return raw + "?" + vals.Encode()
}

func (h *Handler) handleAcquisitionFeed(w http.ResponseWriter, r *http.Request, title, feedID, defaultSort string) {
	base := h.baseURL(r)
	q := r.URL.Query().Get("q")
	tag := r.URL.Query().Get("tag")
	series := r.URL.Query().Get("series")
	author := r.URL.Query().Get("author")
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = defaultSort
	}

	if author != "" {
		title = "Author: " + author
	} else if series != "" {
		title = "Series: " + series
		sortBy = "series"
	} else if tag != "" {
		title = "Subject: " + tag
	}

	books, err := h.Store.ListBooks(q, tag, series, author, "", sortBy)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	nowISO := time.Now().UTC().Format(time.RFC3339)
	entries := make([]AtomEntry, 0, len(books))

	for _, b := range books {
		updatedISO := time.Unix(b.UpdatedAt, 0).UTC().Format(time.RFC3339)
		var authors []AtomAuthor
		for _, a := range b.AuthorList {
			authors = append(authors, AtomAuthor{Name: a})
		}
		var cats []AtomCategory
		for _, t := range b.TagList {
			cats = append(cats, AtomCategory{Term: t, Label: t})
		}

		coverHref := fmt.Sprintf("%s/api/books/%d/cover", base, b.ID)
		downloadHref := h.buildDownloadHref(base, b, r)

		summaryText := b.Description
		if b.Series != "" {
			prefix := fmt.Sprintf("[%s #%g] ", b.Series, b.SeriesIndex)
			summaryText = prefix + summaryText
		}

		entry := AtomEntry{
			ID:         fmt.Sprintf("urn:uuid:%s", b.UUID),
			Title:      b.Title,
			Updated:    updatedISO,
			Authors:    authors,
			Summary:    &AtomText{Type: "text", Body: summaryText},
			Language:   b.Language,
			Publisher:  b.Publisher,
			Issued:     b.PublishedDate,
			Identifier: b.ISBN,
			Categories: cats,
			Links: []AtomLink{
				{
					Rel:  "http://opds-spec.org/image",
					Href: coverHref,
					Type: "image/png",
				},
				{
					Rel:  "http://opds-spec.org/image/thumbnail",
					Href: coverHref,
					Type: "image/png",
				},
				{
					Rel:   "http://opds-spec.org/acquisition",
					Href:  downloadHref,
					Type:  mimeEPUB,
					Title: "Download EPUB",
				},
				{
					Rel:   "http://opds-spec.org/acquisition/open-access",
					Href:  downloadHref,
					Type:  mimeEPUB,
					Title: "Download EPUB",
				},
			},
		}
		entries = append(entries, entry)
	}

	feed := AtomFeed{
		Xmlns:     "http://www.w3.org/2005/Atom",
		XmlnsDC:   "http://purl.org/dc/terms/",
		XmlnsOPDS: "http://opds-spec.org/2010/catalog",
		ID:        feedID,
		Title:     title,
		Updated:   nowISO,
		Author:    &AtomAuthor{Name: "Folio Self-Hosted Library"},
		Links: []AtomLink{
			{Rel: "self", Href: base + r.URL.RequestURI(), Type: mimeOPDSAcq},
			{Rel: "start", Href: base + "/opds", Type: mimeOPDSNav},
			{Rel: "up", Href: base + "/opds", Type: mimeOPDSNav},
			{Rel: "search", Href: base + "/opds/opensearch.xml", Type: "application/opensearchdescription+xml"},
		},
		Entries: entries,
	}

	writeAtomXML(w, feed)
}

func (h *Handler) handleFacetFeed(w http.ResponseWriter, r *http.Request, facet string) {
	base := h.baseURL(r)
	books, err := h.Store.ListBooks("", "", "", "", "", "title")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	counts := make(map[string]int)
	for _, b := range books {
		switch facet {
		case "authors":
			for _, a := range b.AuthorList {
				counts[a]++
			}
		case "series":
			if b.Series != "" {
				counts[b.Series]++
			}
		case "tags":
			for _, t := range b.TagList {
				counts[t]++
			}
		}
	}

	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return strings.ToLower(keys[i]) < strings.ToLower(keys[j])
	})

	nowISO := time.Now().UTC().Format(time.RFC3339)
	entries := make([]AtomEntry, 0, len(keys))
	paramName := strings.TrimSuffix(facet, "s")
	if facet == "series" {
		paramName = "series"
	}

	for _, k := range keys {
		href := fmt.Sprintf("%s/opds/books?%s=%s", base, paramName, url.QueryEscape(k))
		entries = append(entries, AtomEntry{
			ID:      fmt.Sprintf("urn:folio:opds:%s:%s", facet, url.PathEscape(k)),
			Title:   k,
			Updated: nowISO,
			Content: &AtomText{Type: "text", Body: fmt.Sprintf("%d book(s)", counts[k])},
			Links: []AtomLink{
				{Rel: "subsection", Href: href, Type: mimeOPDSAcq},
			},
		})
	}

	feed := AtomFeed{
		Xmlns:   "http://www.w3.org/2005/Atom",
		ID:      "urn:folio:opds:" + facet,
		Title:   "Folio Library — By " + strings.Title(facet),
		Updated: nowISO,
		Author:  &AtomAuthor{Name: "Folio Self-Hosted Library"},
		Links: []AtomLink{
			{Rel: "self", Href: base + "/opds/" + facet, Type: mimeOPDSNav},
			{Rel: "start", Href: base + "/opds", Type: mimeOPDSNav},
			{Rel: "up", Href: base + "/opds", Type: mimeOPDSNav},
		},
		Entries: entries,
	}

	writeAtomXML(w, feed)
}

// handleOPDS2 serves an OPDS 2.0 JSON catalog (supported natively by CrossPoint Reader).
func (h *Handler) handleOPDS2(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)
	q := r.URL.Query().Get("q")
	books, err := h.Store.ListBooks(q, "", "", "", "", "recent")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pubs := make([]map[string]any, 0, len(books))
	for _, b := range books {
		coverHref := fmt.Sprintf("%s/api/books/%d/cover", base, b.ID)
		downloadHref := h.buildDownloadHref(base, b, r)
		meta := map[string]any{
			"@type":       "http://schema.org/Book",
			"title":       b.Title,
			"identifier":  "urn:uuid:" + b.UUID,
			"author":      b.AuthorList,
			"language":    b.Language,
			"description": b.Description,
			"modified":    time.Unix(b.UpdatedAt, 0).UTC().Format(time.RFC3339),
		}
		if b.Publisher != "" {
			meta["publisher"] = b.Publisher
		}
		if b.Series != "" {
			meta["belongsTo"] = map[string]any{
				"series": map[string]any{
					"name":     b.Series,
					"position": b.SeriesIndex,
				},
			}
		}

		pubs = append(pubs, map[string]any{
			"metadata": meta,
			"links": []map[string]any{
				{
					"rel":  "http://opds-spec.org/acquisition",
					"href": downloadHref,
					"type": mimeEPUB,
				},
			},
			"images": []map[string]any{
				{
					"href": coverHref,
					"type": "image/png",
				},
			},
		})
	}

	catalog := map[string]any{
		"metadata": map[string]any{
			"title": "Folio EPUB Library (OPDS 2.0)",
		},
		"links": []map[string]any{
			{
				"rel":  "self",
				"href": base + "/opds/v2",
				"type": "application/opds+json",
			},
			{
				"rel":       "search",
				"href":      base + "/opds/v2{?q}",
				"type":      "application/opds+json",
				"templated": true,
			},
		},
		"publications": pubs,
	}

	w.Header().Set("Content-Type", "application/opds+json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(catalog)
}

func writeAtomXML(w http.ResponseWriter, feed AtomFeed) {
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(feed)
}
