package kube

import "testing"

// FuzzParseScope checks that any scope ParseScope accepts survives a round
// trip through its stored form, and that the binding name stays a valid
// Kubernetes object name.
func FuzzParseScope(f *testing.F) {
	for _, s := range []string{"payments/edit", "*/view", " ns/system:aggregate-to-view ", "/", "a/", "*/", "NS/edit", "a/b/c"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		sc, err := ParseScope(s)
		if err != nil {
			return
		}
		again, err := ParseScope(sc.String())
		if err != nil {
			t.Fatalf("ParseScope(%q) = %+v, but its String() %q does not parse: %v", s, sc, sc.String(), err)
		}
		if again != sc {
			t.Fatalf("round trip of %q: got %+v, want %+v", s, again, sc)
		}
		if name := BindingName("perm-0123456789", sc); len(name) > 253 {
			t.Fatalf("BindingName for %q is %d bytes, over the 253 limit", s, len(name))
		}
	})
}
