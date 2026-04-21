package main

import (
	"bytes"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	t.Parallel()
	key := testKey(t)
	cases := []string{"", "hello", "STRIPE_sk_test_xxxxx", strings.Repeat("A", 4096)}
	for _, plain := range cases {
		ct, err := EncryptSecret(key, plain)
		if err != nil {
			t.Fatalf("encrypt %q: %v", plain, err)
		}
		got, err := DecryptSecret(key, ct)
		if err != nil {
			t.Fatalf("decrypt %q: %v", plain, err)
		}
		if got != plain {
			t.Errorf("round-trip mismatch: got %q, want %q", got, plain)
		}
	}
}

func TestEncryptSecret_NonceRandomness(t *testing.T) {
	t.Parallel()
	key := testKey(t)
	plain := "same-plaintext"
	a, err := EncryptSecret(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncryptSecret(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Errorf("two encryptions produced identical ciphertext — nonce is not random")
	}
}

func TestDecryptSecret_WrongKey(t *testing.T) {
	t.Parallel()
	k1 := testKey(t)
	k2 := testKey(t)
	ct, err := EncryptSecret(k1, "hi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptSecret(k2, ct); err == nil {
		t.Errorf("expected decrypt failure with wrong key")
	}
}

func TestDecryptSecret_CiphertextTooShort(t *testing.T) {
	t.Parallel()
	key := testKey(t)
	if _, err := DecryptSecret(key, []byte{0x00}); err == nil {
		t.Errorf("expected error on short ciphertext")
	}
}

func TestRequireAdminToken(t *testing.T) {
	// Not t.Parallel — subtests use t.Setenv.
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("empty env denies", func(t *testing.T) {
		t.Setenv("HATCH_ADMIN_TOKEN", "")
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer whatever")
		rr := httptest.NewRecorder()
		requireAdminToken(ok).ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status=%d want 401", rr.Code)
		}
	})

	t.Run("missing header", func(t *testing.T) {
		t.Setenv("HATCH_ADMIN_TOKEN", "sekret")
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		requireAdminToken(ok).ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status=%d want 401", rr.Code)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		t.Setenv("HATCH_ADMIN_TOKEN", "sekret")
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer nope")
		rr := httptest.NewRecorder()
		requireAdminToken(ok).ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status=%d want 401", rr.Code)
		}
	})

	t.Run("right token passes", func(t *testing.T) {
		t.Setenv("HATCH_ADMIN_TOKEN", "sekret")
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer sekret")
		rr := httptest.NewRecorder()
		requireAdminToken(ok).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("status=%d want 200", rr.Code)
		}
	})
}

func TestSubstitute_SecretPresent(t *testing.T) {
	t.Parallel()
	spec := &ComposeSpec{
		Version: 1,
		Services: map[string]*ComposeService{
			"api": {
				Build: ".",
				Env: map[string]string{
					"STRIPE_KEY":   "${SECRET_STRIPE_SECRET_KEY}",
					"DATABASE_URL": "postgres://u:p@db/app?sslcert=${SECRET_DB_SSLCERT}",
					"UNKNOWN":      "${SECRET_MISSING}",
					"PLAIN":        "${PR}",
				},
			},
		},
	}
	Substitute(spec, SubstitutionContext{
		PR: 42,
		Secrets: map[string]string{
			"STRIPE_SECRET_KEY": "sk_test_abc",
			"DB_SSLCERT":        "CERTDATA",
		},
	})
	env := spec.Services["api"].Env
	if env["STRIPE_KEY"] != "sk_test_abc" {
		t.Errorf("STRIPE_KEY=%q", env["STRIPE_KEY"])
	}
	if env["DATABASE_URL"] != "postgres://u:p@db/app?sslcert=CERTDATA" {
		t.Errorf("DATABASE_URL=%q", env["DATABASE_URL"])
	}
	if env["UNKNOWN"] != "${SECRET_MISSING}" {
		t.Errorf("missing secret should be left untouched, got %q", env["UNKNOWN"])
	}
	if env["PLAIN"] != "42" {
		t.Errorf("PLAIN=%q, want 42", env["PLAIN"])
	}
}

func TestSubstitute_SecretNilMapLeavesToken(t *testing.T) {
	t.Parallel()
	spec := &ComposeSpec{
		Version: 1,
		Services: map[string]*ComposeService{
			"api": {Build: ".", Env: map[string]string{"X": "${SECRET_FOO}"}},
		},
	}
	Substitute(spec, SubstitutionContext{PR: 1})
	if got := spec.Services["api"].Env["X"]; got != "${SECRET_FOO}" {
		t.Errorf("got %q, want token left untouched", got)
	}
}

// --- Handler tests (validation / auth path, no DB) -------------------------

func TestUpsertSecretHandler_ValidationRejects(t *testing.T) {
	t.Parallel()

	// pool is nil: we never reach the DB because validation fails first.
	h := upsertSecretHandler(nil)

	cases := []struct {
		name string
		body string
	}{
		{"invalid repo", `{"repo":"no-slash","name":"FOO","value":"bar"}`},
		{"invalid name lowercase", `{"repo":"a/b","name":"foo","value":"bar"}`},
		{"empty value", `{"repo":"a/b","name":"FOO","value":""}`},
		{"unknown field", `{"repo":"a/b","name":"FOO","value":"x","extra":true}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/secrets", strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status=%d want 400, body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestListSecretsHandler_InvalidRepo(t *testing.T) {
	t.Parallel()
	h := listSecretsHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/secrets?repo=nope", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400", rr.Code)
	}
}

func TestDeleteSecretHandler_InvalidInput(t *testing.T) {
	t.Parallel()
	h := deleteSecretHandler(nil)
	cases := []string{
		"/api/secrets?repo=a/b&name=lower",
		"/api/secrets?repo=bad&name=FOO",
	}
	for _, u := range cases {
		req := httptest.NewRequest(http.MethodDelete, u, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("url=%s status=%d want 400", u, rr.Code)
		}
	}
}
