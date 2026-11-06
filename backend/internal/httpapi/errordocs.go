package httpapi

const errorDocs = `## Shared errors

Errors use application/problem+json. The HTTP status describes the broad
failure; type identifies the specific problem. The body includes type,
status, title, and detail. Quote the X-Request-ID response header when
reporting a failure.

Each operation documents its specific failures with examples. Its default
response covers the shared ones, which Problem types below marks Any, using
the same Problem schema. A 405 lists the route's methods in the Allow
header.

Body errors apply to operations that accept a body. A default response
describes the body format, not a replacement HTTP status.

Write requests require Sec-Fetch-Site or Origin. Command-line clients can
send an Origin matching the request's target.

Writes that need no session, such as sign-in, also limit how fast one client
can send each of them. Past that limit they answer 429 with type
too-many-requests and Retry-After, before reading the request body.

A request still waiting when its time runs out, e.g. for a busy resource,
answers 503 with type service-busy and Retry-After. A write may have taken
effect before the time ran out.

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
