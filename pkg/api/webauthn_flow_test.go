package api

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
)

const (
	waRPID   = "localhost"
	waOrigin = "https://localhost"
)

var b64url = base64.RawURLEncoding

// --- minimal CBOR encoder (enough for attestation objects and COSE keys) ----------------

func cborHead(major byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 256:
		return []byte{major<<5 | 24, byte(n)}
	default:
		b := []byte{major<<5 | 25, 0, 0}
		binary.BigEndian.PutUint16(b[1:], uint16(n))
		return b
	}
}

func cborInt(v int) []byte {
	if v >= 0 {
		return cborHead(0, uint64(v))
	}
	return cborHead(1, uint64(-1-v))
}

func cborBytes(b []byte) []byte { return append(cborHead(2, uint64(len(b))), b...) }
func cborText(s string) []byte  { return append(cborHead(3, uint64(len(s))), s...) }

// cborMap encodes key/value pairs given as already-encoded items.
func cborMap(items ...[]byte) []byte {
	out := cborHead(5, uint64(len(items)/2))
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

// --- software authenticator -------------------------------------------------------------

type softAuthenticator struct {
	key    *ecdsa.PrivateKey
	credID []byte
	count  uint32
}

func newSoftAuthenticator(t *testing.T) *softAuthenticator {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &softAuthenticator{key: k, credID: id}
}

func (a *softAuthenticator) coseKey() []byte {
	x := a.key.PublicKey.X.FillBytes(make([]byte, 32))
	y := a.key.PublicKey.Y.FillBytes(make([]byte, 32))
	return cborMap(cborInt(1), cborInt(2), cborInt(3), cborInt(-7), cborInt(-1), cborInt(1),
		cborInt(-2), cborBytes(x), cborInt(-3), cborBytes(y))
}

func authData(flags byte, count uint32, attested []byte) []byte {
	rp := sha256.Sum256([]byte(waRPID))
	out := append(rp[:], flags)
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], count)
	out = append(out, c[:]...)
	return append(out, attested...)
}

func clientData(typ, challenge string) []byte {
	b, _ := json.Marshal(map[string]string{"type": typ, "challenge": challenge, "origin": waOrigin})
	return b
}

// attestation answers a registration challenge with a "none" attestation.
func (a *softAuthenticator) attestation(challenge string) map[string]any {
	attested := make([]byte, 16) // zero AAGUID
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(a.credID)))
	attested = append(attested, l[:]...)
	attested = append(attested, a.credID...)
	attested = append(attested, a.coseKey()...)

	obj := cborMap(cborText("fmt"), cborText("none"), cborText("attStmt"), cborMap(),
		cborText("authData"), cborBytes(authData(0x41, 0, attested))) // UP | AT
	return map[string]any{
		"id": b64url.EncodeToString(a.credID), "rawId": b64url.EncodeToString(a.credID), "type": "public-key",
		"response": map[string]string{
			"attestationObject": b64url.EncodeToString(obj),
			"clientDataJSON":    b64url.EncodeToString(clientData("webauthn.create", challenge)),
		},
	}
}

// assertion signs a login challenge. flags carries UP plus any backup bits.
func (a *softAuthenticator) assertion(t *testing.T, challenge string, userID string, flags byte) map[string]any {
	t.Helper()
	a.count++
	ad := authData(flags, a.count, nil)
	cd := clientData("webauthn.get", challenge)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"id": b64url.EncodeToString(a.credID), "rawId": b64url.EncodeToString(a.credID), "type": "public-key",
		"response": map[string]string{
			"authenticatorData": b64url.EncodeToString(ad),
			"clientDataJSON":    b64url.EncodeToString(cd),
			"signature":         b64url.EncodeToString(sig),
			"userHandle":        b64url.EncodeToString([]byte(userID)),
		},
	}
}

func challengeOf(t *testing.T, r resp) string {
	t.Helper()
	var opts struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	r.json(t, &opts)
	if opts.PublicKey.Challenge == "" {
		t.Fatalf("no challenge in %s", r.body)
	}
	return opts.PublicKey.Challenge
}

func newWebAuthnEnv(t *testing.T) *env {
	t.Helper()
	wa, err := webauthn.New(&webauthn.Config{RPDisplayName: "Orion Belt", RPID: waRPID, RPOrigins: []string{waOrigin}})
	if err != nil {
		t.Fatal(err)
	}
	return newEnv(t, func(o *Options) { o.WebAuthn = wa })
}

// --- tests --------------------------------------------------------------------------------

func TestWebAuthn_RegisterLoginAndDelete(t *testing.T) {
	e := newWebAuthnEnv(t)
	ctx := context.Background()
	authr := newSoftAuthenticator(t)

	e.expect(e.do("POST", "/api/v1/webauthn/register/finish", e.aliceKey, authr.attestation("x")), 400) // nothing begun
	ch := challengeOf(t, e.do("POST", "/api/v1/webauthn/register/begin", e.aliceKey, map[string]any{}))
	e.expect(e.do("POST", "/api/v1/webauthn/register/finish", e.aliceKey, authr.attestation("wrong-challenge")), 400)

	ch = challengeOf(t, e.do("POST", "/api/v1/webauthn/register/begin", e.aliceKey, map[string]any{}))
	r := e.do("POST", "/api/v1/webauthn/register/finish", e.aliceKey, authr.attestation(ch))
	e.expect(r, 200)
	credRowID := r.obj(t)["id"].(string)
	if u, _ := e.store.GetUser(ctx, e.alice.ID); !u.WebAuthnEnabled {
		t.Error("registration did not enable WebAuthn on the account")
	}
	if l := e.do("GET", "/api/v1/webauthn/credentials", e.aliceKey, nil).list(t); len(l) != 1 || l[0]["cred_id"] != b64url.EncodeToString(authr.credID) {
		t.Errorf("credentials = %v", l)
	}

	begin := func(user string) resp {
		return e.do("POST", "/api/v1/public/webauthn/login/begin", "", map[string]any{"username": user})
	}
	finish := func(body any) resp { return e.do("POST", "/api/v1/public/webauthn/login/finish", "", body) }

	e.expect(begin("nobody"), 401)
	e.expect(begin("bob"), 400) // no credentials
	e.expect(e.do("POST", "/api/v1/public/webauthn/login/begin", "", `{`), 400)
	e.expect(finish(`{`), 400)
	e.expect(finish(map[string]any{"response": map[string]any{}}), 400)
	e.expect(finish(map[string]any{"username": "nobody", "response": map[string]any{}}), 401)
	e.expect(finish(map[string]any{"username": "bob", "response": map[string]any{}}), 400) // no login begun

	lc := challengeOf(t, begin("alice"))
	e.expect(finish(map[string]any{"username": "alice", "response": map[string]any{"id": "x"}}), 400)
	e.expect(finish(map[string]any{"username": "alice", "response": authr.assertion(t, "forged", e.alice.ID, 0x01)}), 401)
	r = finish(map[string]any{"username": "alice", "response": authr.assertion(t, lc, e.alice.ID, 0x01)})
	e.expect(r, 200)
	login := r.obj(t)
	if login["session_token"] == "" || login["access_token"] == nil {
		t.Errorf("login = %v", login)
	}
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "X-Session-Token", login["session_token"].(string)), 200)
	if !e.audited("auth.login.webauthn") {
		t.Error("WebAuthn login not audited")
	}
	if c, _ := e.store.GetWebAuthnCredentialByCredID(ctx, authr.credID); c.SignCount != authr.count {
		t.Errorf("stored sign count = %d, want %d", c.SignCount, authr.count)
	}

	e.expect(e.do("DELETE", "/api/v1/webauthn/credentials/"+credRowID, e.bobKey, nil), 404)
	e.expect(e.do("DELETE", "/api/v1/webauthn/credentials/"+credRowID, e.aliceKey, nil), 200)
	if u, _ := e.store.GetUser(ctx, e.alice.ID); u.WebAuthnEnabled {
		t.Error("removing the last credential left WebAuthn enabled")
	}
}

// Credentials registered before backup flags were stored are matched to the
// flags the authenticator reports instead of failing validation.
func TestWebAuthn_LegacyCredentialFlagsAreAligned(t *testing.T) {
	e := newWebAuthnEnv(t)
	ctx := context.Background()
	authr := newSoftAuthenticator(t)
	ch := challengeOf(t, e.do("POST", "/api/v1/webauthn/register/begin", e.aliceKey, map[string]any{}))
	e.expect(e.do("POST", "/api/v1/webauthn/register/finish", e.aliceKey, authr.attestation(ch)), 200)

	stored, _ := e.store.GetWebAuthnCredentialByCredID(ctx, authr.credID)
	stored.FlagsKnown = false
	_ = e.store.UpdateWebAuthnCredential(ctx, stored)

	lc := challengeOf(t, e.do("POST", "/api/v1/public/webauthn/login/begin", "", map[string]any{"username": "alice"}))
	r := e.do("POST", "/api/v1/public/webauthn/login/finish", "", map[string]any{
		"username": "alice", "response": authr.assertion(t, lc, e.alice.ID, 0x01|0x08), // UP | BE
	})
	e.expect(r, 200)
	if c, _ := e.store.GetWebAuthnCredentialByCredID(ctx, authr.credID); !c.FlagsKnown || !c.BackupEligible {
		t.Errorf("flags after login = known %v eligible %v", c.FlagsKnown, c.BackupEligible)
	}
}

func TestWebAuthn_RoutesAbsentWhenDisabled(t *testing.T) {
	e := newEnv(t, nil)
	e.expect(e.do("POST", "/api/v1/webauthn/register/begin", e.aliceKey, nil), 404)
	alignUnknownCredentialFlags(nil, nil, nil) // tolerates missing inputs
}

func TestWebAuthnUserAdapter(t *testing.T) {
	e := newEnv(t, nil)
	wa, err := e.srv.loadWebAuthnUser(context.Background(), e.alice)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wa.WebAuthnID(), []byte(e.alice.ID)) || wa.WebAuthnName() != "alice" ||
		wa.WebAuthnDisplayName() != "alice" || wa.WebAuthnIcon() != "" || len(wa.WebAuthnCredentials()) != 0 {
		t.Errorf("adapter = %+v", wa)
	}
}
