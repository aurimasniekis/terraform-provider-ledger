package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestUserRoutes(t *testing.T) {
	var seen []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" if-match="+r.Header.Get("If-Match")+" body="+strings.TrimSpace(string(raw)))
		switch {
		case r.Method == http.MethodPost:
			writeJSON(w, 201, User{ID: idA, Username: "alice", Role: RoleReadOnly, Version: 1})
		case r.Method == http.MethodPatch:
			if r.Header.Get("Content-Type") != "application/merge-patch+json" {
				t.Errorf("patch content type = %q", r.Header.Get("Content-Type"))
			}
			writeJSON(w, 200, User{ID: idA, Version: 2})
		case r.Method == http.MethodPut:
			writeJSON(w, 200, map[string]any{"sessions_ended": 2})
		case r.URL.Path == "/api/v1/users":
			if r.URL.Query().Get("cursor") == "" {
				writeJSON(w, 200, map[string]any{"items": []User{{ID: idA}}, "next_cursor": "c1"})
				return
			}
			writeJSON(w, 200, map[string]any{"items": []User{{ID: idB}}, "next_cursor": nil})
		default:
			writeJSON(w, 200, User{ID: idA})
		}
	})
	ctx := context.Background()
	name := "Alice A"
	if u, err := c.CreateUser(ctx, "alice", RoleReadOnly, &name); err != nil || u.ID != idA {
		t.Fatalf("create: %+v %v", u, err)
	}
	if _, err := c.CreateUser(ctx, "bob", RoleReadOnly, nil); err != nil {
		t.Fatal(err)
	}
	yes := true
	var cleared *string
	if _, err := c.UpdateUser(ctx, idA, 1, UserPatch{Disabled: &yes, DisplayName: &cleared}); err != nil {
		t.Fatal(err)
	}
	n, err := c.SetUserPassword(ctx, idA, Secret("correct horse battery"))
	if err != nil || n != 2 {
		t.Fatalf("password: %d %v", n, err)
	}
	f := false
	all, err := c.ListUsers(ctx, UserFilter{Roles: []string{RoleAdministrator, RoleReadOnly}, Disabled: &f, UsernamePrefix: "a"})
	if err != nil || len(all) != 2 {
		t.Fatalf("list: %v %v", all, err)
	}
	want := []string{
		`POST /api/v1/users if-match= body={"display_name":"Alice A","role":"read-only","username":"alice"}`,
		`POST /api/v1/users if-match= body={"role":"read-only","username":"bob"}`,
		`PATCH /api/v1/users/` + idA + ` if-match="1" body={"disabled":true,"display_name":null}`,
		`PUT /api/v1/users/` + idA + `/password if-match= body={"password":"correct horse battery"}`,
	}
	for i, w := range want {
		if seen[i] != w {
			t.Errorf("request %d = %s, want %s", i, seen[i], w)
		}
	}
	if q := seen[4]; !strings.Contains(q, "disabled=false") || !strings.Contains(q, "role=administrator&role=read-only") || !strings.Contains(q, "username%5Bprefix%5D=a") {
		t.Errorf("list query = %s", q)
	}
	if _, err := c.GetUser(ctx, "nope"); err == nil {
		t.Error("an invalid identifier must be refused before any request")
	}
}

func TestSetUserPasswordErrorHasNoPassword(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 403, map[string]any{"code": CodeRootTokenRequired, "message": "needs the root token", "request_id": "R1"})
	})
	_, err := c.SetUserPassword(context.Background(), idA, Secret("s3cret-value-xyz"))
	if !HasCode(err, CodeRootTokenRequired) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "s3cret-value-xyz") {
		t.Fatal("the password leaked into the error")
	}
}
