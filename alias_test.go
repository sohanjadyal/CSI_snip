package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// Many clients race to claim the same custom alias. Exactly one may get a 201;
// everyone else must get the same 409 a sequential duplicate gets, and the
// winner's link and delete token must be the ones that stay stored.
func TestConcurrentCreatesSameAlias(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()

	const rounds = 100
	const clients = 32

	for round := 0; round < rounds; round++ {
		alias := fmt.Sprintf("dup-%d", round)

		type result struct {
			status int
			url    string
			token  string
		}
		results := make([]result, clients)

		start := make(chan struct{})
		var ready, done sync.WaitGroup
		ready.Add(clients)
		done.Add(clients)
		for i := 0; i < clients; i++ {
			go func(i int) {
				defer done.Done()
				url := fmt.Sprintf("https://example.com/%d/%d", round, i)
				body := fmt.Sprintf(`{"url": %q, "alias": %q}`, url, alias)
				ready.Done()
				<-start // release every client at once
				rec := do(t, srv, "POST", "/api/links", body)
				res := result{status: rec.Code, url: url}
				if rec.Code == http.StatusCreated {
					var out map[string]any
					json.Unmarshal(rec.Body.Bytes(), &out)
					res.token, _ = out["delete_token"].(string)
				}
				results[i] = res
			}(i)
		}
		ready.Wait()
		close(start)
		done.Wait()

		var winners []result
		for _, r := range results {
			switch r.status {
			case http.StatusCreated:
				winners = append(winners, r)
			case http.StatusConflict:
			default:
				t.Fatalf("round %d: unexpected status %d", round, r.status)
			}
		}
		if len(winners) != 1 {
			t.Fatalf("round %d: %d of %d concurrent creates of %q got 201, want exactly 1 (the rest 409)",
				round, len(winners), clients, alias)
		}

		// The stored link must be the winner's, not an overwrite by a loser.
		rec := do(t, srv, "GET", "/api/links/"+alias, "")
		var stored Link
		json.Unmarshal(rec.Body.Bytes(), &stored)
		if stored.URL != winners[0].url {
			t.Fatalf("round %d: stored url %q, want the 201 winner's %q", round, stored.URL, winners[0].url)
		}

		// ...and the token the winner was given must still work.
		rec = do(t, srv, "DELETE", "/api/links/"+alias, "", "X-Delete-Token", winners[0].token)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("round %d: winner's delete token got %d, want 204", round, rec.Code)
		}
	}
}

// Store.Create is the atomic step behind the handler: of any number of
// concurrent calls with the same code, exactly one stores its link and returns
// true; the others return false and leave the stored link alone.
func TestStoreCreateIsAtomic(t *testing.T) {
	store := NewStore(t.TempDir() + "/links.json")
	defer store.Close()

	const rounds = 200
	const callers = 64

	for round := 0; round < rounds; round++ {
		code := fmt.Sprintf("code-%d", round)

		created := make([]bool, callers)
		start := make(chan struct{})
		var ready, done sync.WaitGroup
		ready.Add(callers)
		done.Add(callers)
		for i := 0; i < callers; i++ {
			go func(i int) {
				defer done.Done()
				link := &Link{Code: code, URL: fmt.Sprintf("https://example.com/%d/%d", round, i)}
				ready.Done()
				<-start // every caller is released together
				created[i] = store.Create(link)
			}(i)
		}
		ready.Wait()
		close(start)
		done.Wait()

		winner := -1
		for i, ok := range created {
			if !ok {
				continue
			}
			if winner != -1 {
				t.Fatalf("round %d: callers %d and %d both created %q, want exactly one", round, winner, i, code)
			}
			winner = i
		}
		if winner == -1 {
			t.Fatalf("round %d: no caller created %q, want exactly one", round, code)
		}

		want := fmt.Sprintf("https://example.com/%d/%d", round, winner)
		if got := store.Get(code); got == nil || got.URL != want {
			t.Fatalf("round %d: stored link is %+v, want the winner's url %q", round, got, want)
		}
	}

	// A later create for a taken code is refused and keeps the first link.
	if store.Create(&Link{Code: "code-0", URL: "https://example.com/late"}) {
		t.Fatal("Create returned true for a code that is already taken")
	}
	if got := store.Get("code-0"); got == nil || got.URL == "https://example.com/late" {
		t.Fatalf("a refused Create overwrote the stored link: %+v", got)
	}
}

// Many concurrent clients attempt to claim reserved route names as custom
// aliases. All requests must be rejected with 400 Bad Request, none may
// succeed or conflict (0 201s, 0 409s), and the alias must never be stored.
func TestConcurrentCreatesReservedAliases(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()

	reservedNames := []string{"api", "static", "health", "robots", "favicon"}
	const clients = 32

	for _, name := range reservedNames {
		type result struct {
			status int
			err    string
		}
		results := make([]result, clients)

		start := make(chan struct{})
		var ready, done sync.WaitGroup
		ready.Add(clients)
		done.Add(clients)

		for i := 0; i < clients; i++ {
			go func(i int) {
				defer done.Done()
				alias := name
				switch i % 3 {
				case 1:
					alias = strings.ToUpper(name)
				case 2:
					alias = strings.ToUpper(name[:1]) + strings.ToLower(name[1:])
				}
				url := fmt.Sprintf("https://example.com/%s/%d", name, i)
				body := fmt.Sprintf(`{"url": %q, "alias": %q}`, url, alias)
				ready.Done()
				<-start // release every client simultaneously
				rec := do(t, srv, "POST", "/api/links", body)
				var errResp map[string]string
				json.Unmarshal(rec.Body.Bytes(), &errResp)
				results[i] = result{status: rec.Code, err: errResp["error"]}
			}(i)
		}

		ready.Wait()
		close(start)
		done.Wait()

		for i, r := range results {
			if r.status != http.StatusBadRequest {
				t.Fatalf("alias %q, client %d: got status %d, want 400", name, i, r.status)
			}
			if r.err != "That custom name is reserved." {
				t.Fatalf("alias %q, client %d: got error %q, want %q", name, i, r.err, "That custom name is reserved.")
			}
		}

		// Ensure nothing was saved in the store under this code
		if link := srv.store.Get(name); link != nil {
			t.Fatalf("alias %q was stored: %+v", name, link)
		}
	}

	// Verify that system routes (e.g., GET /api/links) are completely unaffected
	rec := do(t, srv, "GET", "/api/links", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/links got %d, want 200", rec.Code)
	}
}

