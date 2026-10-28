package httpapi

const errorDocs = `## Shared errors

Errors use application/problem+json. The HTTP status describes the broad
failure; type identifies the specific problem. The body includes type,
status, title, and detail. Quote the X-Request-ID response header when
reporting a failure.

Each operation documents its specific failures with examples. Its default
response covers other errors using the same Problem schema, including:

| Status | Meaning |
| --- | --- |
| 400 | The request cannot be parsed. |
| 401 | A valid session is required. |
| 403 | The write request's source or the account's access is not allowed. |
| 404 | The route or resource was not found. |
| 405 | The route does not support this method; see the Allow header. |
| 408 | Receiving the request body took too long. |
| 413 | The request body is too large. |
| 415 | The request content type is unsupported. |
| 422 | Parsed input violates the request rules. |
| 500 | An unexpected server failure occurred. |

Body errors apply to operations that accept a body. A default response
describes the body format, not a replacement HTTP status.

Write requests require Sec-Fetch-Site or Origin. Command-line clients can
send an Origin matching the request's target.

Input errors can include an errors list with field locations and safe
correction messages. For example, a 422 response from POST /setup:

~~~json
{
  "type": "/api/v0/problems/validation-error",
  "status": 422,
  "title": "Invalid request",
  "detail": "Correct the rejected request inputs.",
  "errors": [
    {"location": "body.password", "message": "Use 15 to 128 characters."}
  ]
}
~~~

Use the actual HTTP status for request decisions. Handle unfamiliar problem
types by showing their safe detail. An intermediary, such as a proxy, may
return an error without a Problem Details body.
`
