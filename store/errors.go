package store

import "errors"

var ErrKeyNotFound = errors.New("key not found")
var ErrKeyEmpty = errors.New("key cannot be empty")
