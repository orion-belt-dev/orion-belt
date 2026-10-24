# Error handling

One convention for every package, so failures map to the right HTTP status,
callers see a message they can act on, and internal detail stays in the logs.

## Kinds

`pkg/apperr` gives an error a kind:

| Kind | HTTP | `code` | Use for |
|---|---|---|---|
| `Invalid` | 400 | `invalid` | Malformed input, or a request that breaks a rule |
| `Unauthenticated` | 401 | `unauthenticated` | No valid credentials |
| `Forbidden` | 403 | `forbidden` | Valid credentials, action not allowed |
| `NotFound` | 404 | `not_found` | The thing named does not exist |
| `Conflict` | 409 | `conflict` | Clashes with current state ("request is not pending") |
| `Unavailable` | 503 | `unavailable` | A dependency is down; retrying may work |
| `Internal` | 500 | `internal` | Anything else: a bug or a failure the caller cannot fix |

## Creating errors

- An error the caller can act on gets a kind and a message written for them:

  ```go
  return apperr.Conflictf("request is not pending")
  return apperr.Wrap(apperr.Unavailable, "database unavailable", err)
  ```

- Everything else is wrapped as usual, with context, and stays `Internal`:

  ```go
  return fmt.Errorf("load grants for %s: %w", machineID, err)
  ```

- Sentinels callers check carry a kind. `database.ErrNotFound`,
  `ErrAlreadyExists`, `ErrInvalidInput`, `ErrPermissionDenied`,
  `ErrConnectionFailed` and `ErrAlreadyInitialized` do, so a wrapped
  `ErrNotFound` still answers 404 however deep it is. Compare with
  `errors.Is`, never `==` on a wrapped error.

## Answering in the API

Handlers answer a failed call with `s.fail(c, err)`:

- The status comes from the kind.
- The body is `{"error": "<message>", "code": "<kind>"}`.
- For a kinded error the message is its public message. For `Internal` it is
  always `internal error`: the cause is logged with the method and route and
  never sent, so driver errors, hostnames and SQL never reach a client.
- `Internal` and `Unavailable` are logged at error level; the others are the
  caller's to fix and are not logged as failures.

Request validation that never reaches a service (a missing field, a bad
duration) may still answer directly with `c.JSON(400, gin.H{"error": …})`;
the message is written for the caller.

`TestEveryEndpointReportsStorageFailure` runs every endpoint against a closed
database and fails if any response contains the driver's error text.

## Logging

- Log an error once, where it is handled, not at every layer it passes.
- Never log secrets: no keys, tokens, passwords or TOTP codes, in messages or
  wrapped errors.
- Use `logger.Warn` for an expected failure worth noticing (a denied access),
  `logger.Error` for one an operator should look at.
