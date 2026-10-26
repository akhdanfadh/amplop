package httpapi

// response is every success body, e.g. {"data": {"status": "ok"}}. Its
// name and T's name form the schema name, e.g. ResponseHealthStatus, so T
// must be a named type.
type response[T any] struct {
	Data T `json:"data"`
}

// output is what a handler returns; Huma writes Body as JSON.
type output[T any] struct {
	Body response[T]
}

// respondData wraps data in the success body.
func respondData[T any](data T) *output[T] {
	return &output[T]{Body: response[T]{Data: data}}
}
