package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/orion-belt-dev/orion-belt/pkg/apperr"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

func TestFailMapsKindsAndHidesInternals(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logs := &syncBuffer{}
	s := &APIServer{logger: common.NewLoggerTo(common.DEBUG, logs)}
	cases := []struct {
		err     error
		status  int
		code    string
		message string
		logged  bool
	}{
		{fmt.Errorf("get user: %w", database.ErrNotFound), 404, "not_found", "record not found", false},
		{apperr.Conflictf("request is not pending"), 409, "conflict", "request is not pending", false},
		{errors.New("pq: connection refused to 10.0.0.5"), 500, "internal", "internal error", true},
		{fmt.Errorf("ping: %w", database.ErrConnectionFailed), 503, "unavailable", "database connection failed", true},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
		before := len(logs.String())
		s.fail(c, tc.err)
		var body map[string]string
		_ = json.NewDecoder(io.Reader(w.Body)).Decode(&body)
		if w.Code != tc.status || body["code"] != tc.code || body["error"] != tc.message {
			t.Errorf("%v: %d %v, want %d %s %q", tc.err, w.Code, body, tc.status, tc.code, tc.message)
		}
		if strings.Contains(body["error"], "10.0.0.5") {
			t.Errorf("internal detail sent to the caller: %v", body)
		}
		if logged := len(logs.String()) > before; logged != tc.logged {
			t.Errorf("%v: logged=%v, want %v", tc.err, logged, tc.logged)
		}
	}
}
