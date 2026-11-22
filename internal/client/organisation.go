package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// DefinitionKind is the collection of an organisation definition: categories, labels or buckets (ADR-0053 of go-ledger).
type DefinitionKind string

// The three kinds of organisation definition.
const (
	Categories DefinitionKind = "categories"
	Labels     DefinitionKind = "labels"
	Buckets    DefinitionKind = "buckets"
)

// Definition is a category, label or bucket as the Management API returns it. Timestamps stay strings, exactly as sent.
type Definition struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Name       string  `json:"name"`
	ParentID   *string `json:"parent_id"`
	Archived   bool    `json:"archived"`
	ArchivedAt *string `json:"archived_at"`
	Version    int64   `json:"version"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

// DefinitionFilter narrows a list. Nil fields are not sent. ParentID "none" lists top-level categories.
type DefinitionFilter struct {
	Archived   *bool
	ParentID   *string
	NamePrefix string
}

type definitionPage struct {
	Items      []Definition `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}

// CreateDefinition creates a definition. parentID is only meaningful for categories.
func (c *Client) CreateDefinition(ctx context.Context, kind DefinitionKind, name string, parentID *string) (Definition, error) {
	body := map[string]any{"name": name}
	if parentID != nil {
		if err := checkID("parent_id", *parentID); err != nil {
			return Definition{}, err
		}
		body["parent_id"] = *parentID
	}
	var d Definition
	err := c.do(ctx, request{Method: http.MethodPost, Path: "/" + string(kind), Body: body, Out: &d})
	return d, err
}

// GetDefinition reads one definition.
func (c *Client) GetDefinition(ctx context.Context, kind DefinitionKind, id string) (Definition, error) {
	if err := checkID("id", id); err != nil {
		return Definition{}, err
	}
	var d Definition
	err := c.do(ctx, request{Method: http.MethodGet, Path: "/" + string(kind) + "/" + id, Out: &d})
	return d, err
}

// ListDefinitions reads every page of a list, in the API's creation order.
func (c *Client) ListDefinitions(ctx context.Context, kind DefinitionKind, f DefinitionFilter) ([]Definition, error) {
	q := url.Values{"limit": {"200"}}
	if f.Archived != nil {
		q.Set("archived", strconv.FormatBool(*f.Archived))
	}
	if f.ParentID != nil {
		q.Set("parent_id", *f.ParentID)
	}
	if f.NamePrefix != "" {
		q.Set("name[prefix]", f.NamePrefix)
	}
	var all []Definition
	for {
		var page definitionPage
		if err := c.do(ctx, request{Method: http.MethodGet, Path: "/" + string(kind), Query: q, Out: &page}); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Items) == 0 {
			return all, nil
		}
		q.Set("cursor", *page.NextCursor)
	}
}

// UpdateDefinition applies a JSON merge patch based on version. A key with a nil value is sent as JSON null.
func (c *Client) UpdateDefinition(ctx context.Context, kind DefinitionKind, id string, version int64, patch map[string]any) (Definition, error) {
	if err := checkID("id", id); err != nil {
		return Definition{}, err
	}
	var d Definition
	err := c.do(ctx, request{Method: http.MethodPatch, Path: "/" + string(kind) + "/" + id, Version: version, Body: patch, Out: &d})
	return d, err
}

// ArchiveDefinition archives a definition; it keeps every assignment.
func (c *Client) ArchiveDefinition(ctx context.Context, kind DefinitionKind, id string, version int64) (Definition, error) {
	return c.transition(ctx, kind, id, version, "archive")
}

// UnarchiveDefinition makes an archived definition usable again.
func (c *Client) UnarchiveDefinition(ctx context.Context, kind DefinitionKind, id string, version int64) (Definition, error) {
	return c.transition(ctx, kind, id, version, "unarchive")
}

func (c *Client) transition(ctx context.Context, kind DefinitionKind, id string, version int64, verb string) (Definition, error) {
	if err := checkID("id", id); err != nil {
		return Definition{}, err
	}
	var d Definition
	err := c.do(ctx, request{Method: http.MethodPost, Path: "/" + string(kind) + "/" + id + "/" + verb, Version: version, Out: &d})
	return d, err
}

// DeleteDefinition deletes a definition based on version. When assignments still refer to it the API refuses with
// definition_in_use unless reassignTo names an active definition of the same kind. It returns how many moved.
func (c *Client) DeleteDefinition(ctx context.Context, kind DefinitionKind, id string, version int64, reassignTo string) (int, error) {
	if err := checkID("id", id); err != nil {
		return 0, err
	}
	var q url.Values
	if reassignTo != "" {
		if err := checkID("reassign_to", reassignTo); err != nil {
			return 0, err
		}
		q = url.Values{"reassign_to": {reassignTo}}
	}
	var out struct {
		Reassigned int `json:"reassigned"`
	}
	err := c.do(ctx, request{Method: http.MethodDelete, Path: "/" + string(kind) + "/" + id, Query: q, Version: version, Out: &out})
	return out.Reassigned, err
}

// ObjectType names what a category, label or bucket is assigned to.
type ObjectType string

// Object types the provider assigns to. Merchants are not included: they are keyed by a provisional name key in the
// pinned API (ADR-0053 of go-ledger) and need an administrator-only grant; see ADR-0004 of this repository.
const (
	ObjectTransaction ObjectType = "transaction"
	ObjectCommitment  ObjectType = "commitment"
	ObjectAccount     ObjectType = "account"
)

// Actor says who made an assignment.
type Actor struct {
	Kind   string  `json:"kind"`
	UserID *string `json:"user_id"`
	Via    *string `json:"via"`
}

// CategoryAssignment is an object's single category. Category is nil when the person chose none.
type CategoryAssignment struct {
	Category  *Definition `json:"category"`
	Origin    string      `json:"origin"`
	Source    *string     `json:"source"`
	By        Actor       `json:"by"`
	At        string      `json:"at"`
	Inherited bool        `json:"inherited"`
}

// LabelAssignment is one label on an object (or, in RemovedLabels, one the person took off).
type LabelAssignment struct {
	Label     Definition `json:"label"`
	Origin    string     `json:"origin"`
	Source    *string    `json:"source"`
	By        Actor      `json:"by"`
	At        string     `json:"at"`
	Inherited bool       `json:"inherited"`
}

// ObjectAssignments is everything assigned to one object.
type ObjectAssignments struct {
	ObjectType    string              `json:"object_type"`
	ObjectID      string              `json:"object_id"`
	Category      *CategoryAssignment `json:"category"`
	Labels        []LabelAssignment   `json:"labels"`
	RemovedLabels []LabelAssignment   `json:"removed_labels"`
	Buckets       []Definition        `json:"buckets"`
}

func objectPath(t ObjectType, id string) (string, error) {
	if err := checkID("object_id", id); err != nil {
		return "", err
	}
	return "/organisation/" + string(t) + "/" + id, nil
}

// GetObjectOrganisation reads what is assigned to an object. It is a 404 when the object does not exist.
func (c *Client) GetObjectOrganisation(ctx context.Context, t ObjectType, id string) (ObjectAssignments, error) {
	p, err := objectPath(t, id)
	if err != nil {
		return ObjectAssignments{}, err
	}
	var a ObjectAssignments
	err = c.do(ctx, request{Method: http.MethodGet, Path: p, Out: &a})
	return a, err
}

// SetObjectCategory records the person's category for an object; a nil categoryID records "none".
func (c *Client) SetObjectCategory(ctx context.Context, t ObjectType, id string, categoryID *string) (ObjectAssignments, error) {
	p, err := objectPath(t, id)
	if err != nil {
		return ObjectAssignments{}, err
	}
	var v any
	if categoryID != nil {
		if err := checkID("category_id", *categoryID); err != nil {
			return ObjectAssignments{}, err
		}
		v = *categoryID
	}
	var a ObjectAssignments
	err = c.do(ctx, request{Method: http.MethodPut, Path: p + "/category", Body: map[string]any{"category_id": v}, Out: &a})
	return a, err
}

// ResetObjectCategory forgets the person's category decision. It is a 404 when the object has none.
func (c *Client) ResetObjectCategory(ctx context.Context, t ObjectType, id string) error {
	p, err := objectPath(t, id)
	if err != nil {
		return err
	}
	return c.do(ctx, request{Method: http.MethodDelete, Path: p + "/category"})
}

// AddObjectLabel puts a label on an object as the person's.
func (c *Client) AddObjectLabel(ctx context.Context, t ObjectType, id, labelID string) (ObjectAssignments, error) {
	p, err := objectPath(t, id)
	if err != nil {
		return ObjectAssignments{}, err
	}
	if err := checkID("label_id", labelID); err != nil {
		return ObjectAssignments{}, err
	}
	var a ObjectAssignments
	err = c.do(ctx, request{Method: http.MethodPut, Path: p + "/labels/" + labelID, Out: &a})
	return a, err
}

// RemoveObjectLabel takes a label off an object and records that the person did. It is a 404 when it is not there.
func (c *Client) RemoveObjectLabel(ctx context.Context, t ObjectType, id, labelID string) error {
	p, err := objectPath(t, id)
	if err != nil {
		return err
	}
	if err := checkID("label_id", labelID); err != nil {
		return err
	}
	return c.do(ctx, request{Method: http.MethodDelete, Path: p + "/labels/" + labelID})
}

// BucketMember is one commitment or account in a bucket.
type BucketMember struct {
	ObjectType string `json:"object_type"`
	ObjectID   string `json:"object_id"`
	AddedBy    Actor  `json:"added_by"`
	AddedAt    string `json:"added_at"`
}

// ListBucketMembers reads every member of a bucket.
func (c *Client) ListBucketMembers(ctx context.Context, bucketID string) ([]BucketMember, error) {
	if err := checkID("bucket_id", bucketID); err != nil {
		return nil, err
	}
	q := url.Values{"limit": {"200"}}
	var all []BucketMember
	for {
		var page struct {
			Items      []BucketMember `json:"items"`
			NextCursor *string        `json:"next_cursor"`
		}
		if err := c.do(ctx, request{Method: http.MethodGet, Path: "/buckets/" + bucketID + "/members", Query: q, Out: &page}); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Items) == 0 {
			return all, nil
		}
		q.Set("cursor", *page.NextCursor)
	}
}

func memberPath(bucketID string, t ObjectType, id string) (string, error) {
	if err := checkID("bucket_id", bucketID); err != nil {
		return "", err
	}
	if err := checkID("object_id", id); err != nil {
		return "", err
	}
	return "/buckets/" + bucketID + "/members/" + string(t) + "/" + id, nil
}

// AddBucketMember puts a commitment or account in a bucket. It is idempotent.
func (c *Client) AddBucketMember(ctx context.Context, bucketID string, t ObjectType, id string) error {
	p, err := memberPath(bucketID, t, id)
	if err != nil {
		return err
	}
	return c.do(ctx, request{Method: http.MethodPut, Path: p})
}

// RemoveBucketMember takes an object out of a bucket. It is a 404 when it is not a member.
func (c *Client) RemoveBucketMember(ctx context.Context, bucketID string, t ObjectType, id string) error {
	p, err := memberPath(bucketID, t, id)
	if err != nil {
		return err
	}
	return c.do(ctx, request{Method: http.MethodDelete, Path: p})
}
