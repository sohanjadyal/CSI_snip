package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func do(t *testing.T, srv *Server, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func create(t *testing.T, srv *Server, body string) map[string]any {
	t.Helper()
	rec := do(t, srv, "POST", "/api/links", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201: %s", rec.Code, rec.Body)
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func TestShortenAndFollow(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link := create(t, srv, `{"url": "https://go.dev"}`)
	rec := do(t, srv, "GET", "/"+link["code"].(string), "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://go.dev" {
		t.Fatalf("got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestURLWithoutScheme(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link := create(t, srv, `{"url": "go.dev"}`)

	rec := do(t, srv, "GET", "/"+link["code"].(string), "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://go.dev" {
		t.Fatalf("got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestSchemeIsNormalized(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	for url, want := range map[string]string{
		"HTTPS://go.dev":       "https://go.dev",
		"Http://go.dev/doc":    "http://go.dev/doc",
		"localhost:8080/admin": "https://localhost:8080/admin",
	} {
		link := create(t, srv, `{"url": "`+url+`"}`)
		rec := do(t, srv, "GET", "/"+link["code"].(string), "")
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
			t.Errorf("%s: got %d to %q, want %q", url, rec.Code, rec.Header().Get("Location"), want)
		}
	}
}

func TestNonHTTPURLIsRejected(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	for _, url := range []string{"ftp://example.com/file", "mailto:someone@example.com", "file:///etc/passwd", "https://", "http://"} {
		if rec := do(t, srv, "POST", "/api/links", `{"url": "`+url+`"}`); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", url, rec.Code)
		}
	}
}

func TestCustomAlias(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link := create(t, srv, `{"url": "https://hacktoberfest.com", "alias": "hack-2026"}`)
	if link["code"] != "hack-2026" {
		t.Fatalf("got code %v", link["code"])
	}
}

func TestBadInputIsRejected(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	for _, body := range []string{`{"url": ""}`, `not json`, `{"url": "https://go.dev", "alias": "a!"}`} {
		if rec := do(t, srv, "POST", "/api/links", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", body, rec.Code)
		}
	}
}

func TestStatsForUnknownLink(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	if rec := do(t, srv, "GET", "/api/links/nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}

func TestDeleteWithToken(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link := create(t, srv, `{"url": "https://go.dev", "alias": "gone"}`)
	rec := do(t, srv, "DELETE", "/api/links/gone", "", "X-Delete-Token", link["delete_token"].(string))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/links/gone", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("after delete: got %d", rec.Code)
	}
}

func TestDeleteWithoutToken(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	create(t, srv, `{"url": "https://go.dev", "alias": "keep"}`)
	rec := do(t, srv, "DELETE", "/api/links/keep", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("delete without token: got %d, want 403", rec.Code)
	}
	var errResp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil || errResp["error"] != "Wrong delete token." {
		t.Fatalf("unexpected error response: %s", rec.Body.String())
	}
	if rec := do(t, srv, "GET", "/api/links/keep", ""); rec.Code != http.StatusOK {
		t.Fatalf("link should still exist after unauthorized delete: got %d", rec.Code)
	}
}

func TestDeleteWithEmptyToken(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	create(t, srv, `{"url": "https://go.dev", "alias": "keep-empty"}`)
	rec := do(t, srv, "DELETE", "/api/links/keep-empty", "", "X-Delete-Token", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("delete with empty token: got %d, want 403", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/links/keep-empty", ""); rec.Code != http.StatusOK {
		t.Fatalf("link should still exist after unauthorized delete: got %d", rec.Code)
	}
}

func TestDeleteWithWrongToken(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	create(t, srv, `{"url": "https://go.dev", "alias": "keep2"}`)
	rec := do(t, srv, "DELETE", "/api/links/keep2", "", "X-Delete-Token", "wrong-token")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("delete with wrong token: got %d, want 403", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/links/keep2", ""); rec.Code != http.StatusOK {
		t.Fatalf("link should still exist after unauthorized delete: got %d", rec.Code)
	}
}

func TestDeleteTokenCrossLinkIsolation(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link1 := create(t, srv, `{"url": "https://go.dev", "alias": "link-one"}`)
	link2 := create(t, srv, `{"url": "https://github.com", "alias": "link-two"}`)

	token1 := link1["delete_token"].(string)
	token2 := link2["delete_token"].(string)

	// Try to delete link1 with link2's token
	if rec := do(t, srv, "DELETE", "/api/links/link-one", "", "X-Delete-Token", token2); rec.Code != http.StatusForbidden {
		t.Fatalf("deleting link1 with token2: got %d, want 403", rec.Code)
	}

	// Try to delete link2 with link1's token
	if rec := do(t, srv, "DELETE", "/api/links/link-two", "", "X-Delete-Token", token1); rec.Code != http.StatusForbidden {
		t.Fatalf("deleting link2 with token1: got %d, want 403", rec.Code)
	}

	// Verify both links are still present
	if rec := do(t, srv, "GET", "/api/links/link-one", ""); rec.Code != http.StatusOK {
		t.Fatalf("link-one should still exist: got %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/links/link-two", ""); rec.Code != http.StatusOK {
		t.Fatalf("link-two should still exist: got %d", rec.Code)
	}

	// Delete link1 with its own token
	if rec := do(t, srv, "DELETE", "/api/links/link-one", "", "X-Delete-Token", token1); rec.Code != http.StatusNoContent {
		t.Fatalf("authorized delete link1: got %d, want 204", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/links/link-one", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("link-one should be gone: got %d", rec.Code)
	}

	// link2 should still exist
	if rec := do(t, srv, "GET", "/api/links/link-two", ""); rec.Code != http.StatusOK {
		t.Fatalf("link-two should still exist: got %d", rec.Code)
	}
}

func TestDeleteUnauthorizedPreservesStats(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	create(t, srv, `{"url": "https://go.dev", "alias": "stat-link"}`)

	// Click twice
	do(t, srv, "GET", "/stat-link", "")
	do(t, srv, "GET", "/stat-link", "")

	// Unauthorized delete attempts
	do(t, srv, "DELETE", "/api/links/stat-link", "")
	do(t, srv, "DELETE", "/api/links/stat-link", "", "X-Delete-Token", "invalid")
	do(t, srv, "DELETE", "/api/links/stat-link", "", "X-Delete-Token", "")

	// Check stats
	rec := do(t, srv, "GET", "/api/links/stat-link", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("stats fetch failed: %d", rec.Code)
	}
	var stats Link
	json.Unmarshal(rec.Body.Bytes(), &stats)
	if stats.Clicks != 2 {
		t.Fatalf("clicks changed after unauthorized deletes: got %d, want 2", stats.Clicks)
	}
}

func TestConcurrentUnauthorizedDeletes(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link := create(t, srv, `{"url": "https://go.dev", "alias": "secure-concurrent"}`)
	validToken := link["delete_token"].(string)

	const attempts = 100
	var wg sync.WaitGroup
	wg.Add(attempts)

	for i := 0; i < attempts; i++ {
		go func(idx int) {
			defer wg.Done()
			var rec *httptest.ResponseRecorder
			if idx%2 == 0 {
				rec = do(t, srv, "DELETE", "/api/links/secure-concurrent", "")
			} else {
				rec = do(t, srv, "DELETE", "/api/links/secure-concurrent", "", "X-Delete-Token", "bad-token")
			}
			if rec.Code != http.StatusForbidden {
				t.Errorf("unauthorized delete got status %d, want 403", rec.Code)
			}
		}(i)
	}

	wg.Wait()

	// Link must still be available
	rec := do(t, srv, "GET", "/api/links/secure-concurrent", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("link deleted prematurely: got %d", rec.Code)
	}

	// Authorized delete must succeed
	recDel := do(t, srv, "DELETE", "/api/links/secure-concurrent", "", "X-Delete-Token", validToken)
	if recDel.Code != http.StatusNoContent {
		t.Fatalf("authorized delete failed: got %d", recDel.Code)
	}

	// Now it must be 404
	if recAfter := do(t, srv, "GET", "/api/links/secure-concurrent", ""); recAfter.Code != http.StatusNotFound {
		t.Fatalf("link still exists after authorized delete: got %d", recAfter.Code)
	}
}

func TestListLinks(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	create(t, srv, `{"url": "https://go.dev"}`)
	create(t, srv, `{"url": "https://github.com"}`)
	var links []Link
	json.Unmarshal(do(t, srv, "GET", "/api/links", "").Body.Bytes(), &links)
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2", len(links))
	}
}

func TestPersistence(t *testing.T) {
	storePath := t.TempDir() + "/links.json"

	// Create first server and add links
	srv1 := NewServer(storePath)
	create(t, srv1, `{"url": "https://go.dev", "alias": "persist1"}`)
	link2 := create(t, srv1, `{"url": "https://github.com", "alias": "persist2"}`)

	// Click one link to increment counter
	do(t, srv1, "GET", "/persist1", "")

	srv1.store.Close()

	// Create second server with same path
	srv2 := NewServer(storePath)
	defer srv2.store.Close()

	// Verify links were restored
	rec1 := do(t, srv2, "GET", "/api/links/persist1", "")
	if rec1.Code != http.StatusOK {
		t.Fatalf("persist1 not found after restart")
	}

	var restored Link
	json.Unmarshal(rec1.Body.Bytes(), &restored)
	if restored.Code != "persist1" || restored.URL != "https://go.dev" {
		t.Errorf("link data not restored correctly")
	}
	if restored.Clicks != 1 {
		t.Errorf("click count: got %d, want 1", restored.Clicks)
	}

	// Verify delete token was restored
	rec2 := do(t, srv2, "DELETE", "/api/links/persist2", "", "X-Delete-Token", link2["delete_token"].(string))
	if rec2.Code != http.StatusNoContent {
		t.Errorf("delete with restored token failed: got %d", rec2.Code)
	}
}

func TestConcurrentAccess(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()

	link := create(t, srv, `{"url": "https://example.com", "alias": "concurrent"}`)
	token := link["delete_token"].(string)

	// Concurrent clicks, creates, reads, and a delete
	done := make(chan bool)

	// 20 concurrent clicks
	for i := 0; i < 20; i++ {
		go func() {
			do(t, srv, "GET", "/concurrent", "")
			done <- true
		}()
	}

	// 10 concurrent creates
	for i := 0; i < 10; i++ {
		go func(n int) {
			create(t, srv, `{"url": "https://example.com"}`)
			done <- true
		}(i)
	}

	// 10 concurrent stats reads
	for i := 0; i < 10; i++ {
		go func() {
			do(t, srv, "GET", "/api/links/concurrent", "")
			done <- true
		}()
	}

	// Wait for all operations to complete
	for i := 0; i < 40; i++ {
		<-done
	}

	// Verify the link still exists and has correct click count
	rec := do(t, srv, "GET", "/api/links/concurrent", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("concurrent link not found")
	}

	var finalLink Link
	json.Unmarshal(rec.Body.Bytes(), &finalLink)
	if finalLink.Clicks != 20 {
		t.Errorf("clicks after concurrent access: got %d, want 20", finalLink.Clicks)
	}

	// Verify delete token still matches
	rec2 := do(t, srv, "DELETE", "/api/links/concurrent", "", "X-Delete-Token", token)
	if rec2.Code != http.StatusNoContent {
		t.Errorf("delete after concurrent access failed: got %d", rec2.Code)
	}

	// Verify it's actually deleted
	rec3 := do(t, srv, "GET", "/api/links/concurrent", "")
	if rec3.Code != http.StatusNotFound {
		t.Errorf("link should be deleted: got %d", rec3.Code)
	}
}

func TestRepeatedPersistence(t *testing.T) {
	storePath := t.TempDir() + "/links.json"
	srv := NewServer(storePath)

	// Create links and trigger multiple saves
	for i := 0; i < 10; i++ {
		create(t, srv, `{"url": "https://example.com"}`)
	}

	// Force some clicks to trigger more saves
	links := srv.store.List()
	if len(links) != 10 {
		t.Fatalf("expected 10 links, got %d", len(links))
	}

	for i := 0; i < 5; i++ {
		do(t, srv, "GET", "/"+links[i].Code, "")
	}

	srv.store.Close()

	// Reload and verify all data persisted correctly
	srv2 := NewServer(storePath)
	defer srv2.store.Close()

	links2 := srv2.store.List()
	if len(links2) != 10 {
		t.Fatalf("after reload: expected 10 links, got %d", len(links2))
	}

	clickCount := 0
	for _, link := range links2 {
		clickCount += link.Clicks
	}
	if clickCount != 5 {
		t.Errorf("after reload: expected 5 total clicks, got %d", clickCount)
	}
}

func TestConcurrentFollows(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link := create(t, srv, `{"url": "https://hacktoberfest.com", "alias": "hack-2026"}`)
	var wg sync.WaitGroup
	concurrentRequests := 10000
	wg.Add(concurrentRequests)

	for i := 0; i < concurrentRequests; i++ {
		go func() {
			defer wg.Done()

			rec := do(t, srv, "GET", "/"+link["code"].(string), "")

			if rec.Code != http.StatusFound {
				t.Errorf("got %d to %q", rec.Code, rec.Header().Get("Location"))
			}
		}()
	}

	wg.Wait()

	rec := do(t, srv, "GET", "/api/links/"+link["code"].(string), "")

	if rec.Code != http.StatusOK {
		t.Fatalf("stats: got status %d, want 200", rec.Code)
	}
	var stats Link
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("failed to decode stats: %v", err)
	}

	if stats.Clicks != concurrentRequests {
		t.Fatalf("clicks mismatch: got %d, want %d", stats.Clicks, concurrentRequests)
	}
}

func TestConcurrentFollowAndList(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link := create(t, srv, `{"url": "https://hacktoberfest.com", "alias": "hack-2026"}`)
	var wg sync.WaitGroup
	concurrentRequests := 10000
	listRequests := 100
	wg.Add(concurrentRequests + listRequests)

	for i := 0; i < concurrentRequests; i++ {
		go func() {
			defer wg.Done()

			rec := do(t, srv, "GET", "/"+link["code"].(string), "")

			if rec.Code != http.StatusFound {
				t.Errorf("got %d to %q", rec.Code, rec.Header().Get("Location"))
			}
		}()
	}

	for i := 0; i < listRequests; i++ {
		go func() {
			defer wg.Done()

			rec := do(t, srv, "GET", "/api/links", "")

			if rec.Code != http.StatusOK {
				t.Errorf("list: got status %d, want 200", rec.Code)
			}
		}()
	}

	wg.Wait()
}

func TestConcurrentFollowAndDelete(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link := create(t, srv, `{"url": "https://hacktoberfest.com", "alias": "hack-2026"}`)
	var wg sync.WaitGroup
	concurrentRequests := 10000
	wg.Add(concurrentRequests + 1)

	for i := 0; i < concurrentRequests; i++ {
		go func() {
			defer wg.Done()

			rec := do(t, srv, "GET", "/"+link["code"].(string), "")

			if rec.Code != http.StatusFound && rec.Code != http.StatusNotFound {
				t.Errorf("got %d to %q", rec.Code, rec.Header().Get("Location"))
			}
		}()
	}

	go func() {
		defer wg.Done()

		rec := do(t, srv, "DELETE", "/api/links/"+link["code"].(string), "", "X-Delete-Token", link["delete_token"].(string))

		if rec.Code != http.StatusNoContent && rec.Code != http.StatusNotFound {
			t.Errorf("delete: got status %d", rec.Code)
		}
	}()

	wg.Wait()

	rec := do(t, srv, "GET", "/api/links/"+link["code"].(string), "")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("after delete: got status %d, want 404", rec.Code)
	}
}

func TestReservedAliasesRejected(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()

	reservedCases := []string{
		"api", "API", "Api",
		"static", "STATIC", "Static",
		"health", "HEALTH", "Health",
		"robots", "ROBOTS", "Robots",
		"favicon", "FAVICON", "Favicon",
	}

	for _, alias := range reservedCases {
		body := `{"url": "https://go.dev", "alias": "` + alias + `"}`
		rec := do(t, srv, "POST", "/api/links", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("alias %q: got status %d, want 400", alias, rec.Code)
		}

		var errResp map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Errorf("alias %q: failed to decode response: %v", alias, err)
		}

		// Ensure the reserved name was not stored in links
		if link := srv.store.Get(alias); link != nil {
			t.Errorf("alias %q should not be stored, found: %+v", alias, link)
		}
	}
}

func TestReservedAliasSubstringsAllowed(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()

	allowedCases := []string{
		"api-v1", "my-static", "healthcheck", "robots-txt", "favicon-32",
	}

	for _, alias := range allowedCases {
		body := `{"url": "https://go.dev", "alias": "` + alias + `"}`
		link := create(t, srv, body)
		if link["code"] != alias {
			t.Errorf("got code %v, want %q", link["code"], alias)
		}

		rec := do(t, srv, "GET", "/"+alias, "")
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://go.dev" {
			t.Errorf("alias %q: got %d to %q, want 302 to https://go.dev", alias, rec.Code, rec.Header().Get("Location"))
		}
	}
}

