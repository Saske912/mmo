package registry

import "errors"

var (
	errInvalidSpec   = errors.New("invalid cell spec")
	errInvalidBounds = errors.New("invalid bounds: min must be less than max")
)
