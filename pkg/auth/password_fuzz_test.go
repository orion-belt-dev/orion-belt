package auth

import "testing"

// FuzzVerifyPassword feeds malformed stored hashes to VerifyPassword: it must
// return false, never panic or allocate without bound.
func FuzzVerifyPassword(f *testing.F) {
	good, err := HashPassword("correct horse battery")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good, "correct horse battery")
	f.Add(good, "wrong")
	f.Add("$argon2id$v=19$m=65536,t=0,p=4$c2FsdHNhbHRzYWx0c2E$aGFzaA", "x")
	f.Add("$argon2id$v=19$m=65536,t=1,p=0$c2FsdHNhbHRzYWx0c2E$aGFzaA", "x")
	f.Add("$argon2id$v=19$m=4294967295,t=1,p=4$c2FsdHNhbHRzYWx0c2E$aGFzaA", "x")
	f.Add("$argon2id$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2E$", "x")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, encoded, password string) {
		ok := VerifyPassword(encoded, password)
		if ok && encoded == good && password != "correct horse battery" {
			t.Fatalf("wrong password %q verified against a real hash", password)
		}
	})
}
