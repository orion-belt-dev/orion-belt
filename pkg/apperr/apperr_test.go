package apperr

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestKindsMapToStatusAndCode(t *testing.T) {
	cases := map[Kind][2]any{
		Internal:        {http.StatusInternalServerError, "internal"},
		Invalid:         {http.StatusBadRequest, "invalid"},
		Unauthenticated: {http.StatusUnauthorized, "unauthenticated"},
		Forbidden:       {http.StatusForbidden, "forbidden"},
		NotFound:        {http.StatusNotFound, "not_found"},
		Conflict:        {http.StatusConflict, "conflict"},
		Unavailable:     {http.StatusServiceUnavailable, "unavailable"},
	}
	for k, want := range cases {
		if k.HTTPStatus() != want[0] || k.String() != want[1] {
			t.Errorf("%v: status %d code %q, want %v", k, k.HTTPStatus(), k.String(), want)
		}
	}
}

func TestKindSurvivesWrapping(t *testing.T) {
	sentinel := NotFoundf("record not found")
	err := fmt.Errorf("get request: %w", fmt.Errorf("query: %w", sentinel))
	if KindOf(err) != NotFound || Message(err) != "record not found" {
		t.Errorf("KindOf=%v Message=%q", KindOf(err), Message(err))
	}
	if !errors.Is(err, sentinel) {
		t.Error("sentinel lost through wrapping")
	}
}

func TestUnkindedErrorsStayInternalAndHidden(t *testing.T) {
	err := fmt.Errorf("pq: password authentication failed for user orion")
	if KindOf(err) != Internal || Message(err) != "internal error" {
		t.Errorf("KindOf=%v Message=%q", KindOf(err), Message(err))
	}
	wrapped := Wrap(Unavailable, "database unavailable", err)
	if Message(wrapped) != "database unavailable" || !errors.Is(wrapped, err) {
		t.Errorf("Wrap lost message or cause: %q", Message(wrapped))
	}
	if wrapped.Error() != "database unavailable: "+err.Error() {
		t.Errorf("Error() = %q", wrapped.Error())
	}
	if Message(New(Internal, "secret detail")) != "internal error" {
		t.Error("an Internal message reached the caller")
	}
	for _, e := range []*Error{Invalidf("a"), Forbiddenf("b"), Conflictf("c")} {
		if e.Error() == "" || KindOf(e) == Internal {
			t.Errorf("constructor gave %+v", e)
		}
	}
}
