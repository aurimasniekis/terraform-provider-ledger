package client

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// Document is a decoded JSON object of a read-only report (ADR-0011). The report data sources describe their own shape, so
// the client does not copy it into Go structs: it keeps the document as the API sent it. Numbers stay json.Number and
// monetary amounts are strings in the API, so nothing is ever converted through a binary floating-point value.
type Document map[string]any

// UnmarshalJSON decodes with UseNumber so integers and decimals keep their written form.
func (d *Document) UnmarshalJSON(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return err
	}
	*d = m
	return nil
}

// GetDocument reads one report with a GET. path is relative to the API prefix and already escaped.
func (c *Client) GetDocument(ctx context.Context, path string, q url.Values) (Document, error) {
	var d Document
	err := c.do(ctx, request{Method: http.MethodGet, Path: path, Query: q, Out: &d})
	return d, err
}

// pageSize is the page size requested from list endpoints.
const pageSize = 200

// ListDocuments reads the items of a cursor-paged list, at most max of them. truncated is true when more were available.
func (c *Client) ListDocuments(ctx context.Context, path string, q url.Values, max int) (items []any, truncated bool, err error) {
	if max <= 0 {
		max = 1
	}
	q = cloneValues(q)
	limit := pageSize
	if max < limit {
		limit = max
	}
	q.Set("limit", itoa(limit))
	for {
		var page struct {
			Items      []any   `json:"items"`
			NextCursor *string `json:"next_cursor"`
		}
		if err := c.getPage(ctx, path, q, &page); err != nil {
			return nil, false, err
		}
		more := page.NextCursor != nil && *page.NextCursor != "" && len(page.Items) > 0
		for _, it := range page.Items {
			if len(items) == max {
				return items, true, nil
			}
			items = append(items, it)
		}
		if !more {
			return items, false, nil
		}
		if len(items) == max {
			return items, true, nil
		}
		q.Set("cursor", *page.NextCursor)
	}
}

func (c *Client) getPage(ctx context.Context, path string, q url.Values, page *struct {
	Items      []any   `json:"items"`
	NextCursor *string `json:"next_cursor"`
}) error {
	// Items are decoded through Document-style number handling: re-decode the raw page with UseNumber.
	var raw json.RawMessage
	if err := c.do(ctx, request{Method: http.MethodGet, Path: path, Query: q, Out: &raw}); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(page)
}

func cloneValues(q url.Values) url.Values {
	out := url.Values{}
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }
