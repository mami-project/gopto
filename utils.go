package pto3

import (
	"fmt"
	"log"
	"time"
)

// AsString tries to typeswitch an interface to a string, printing its value if not.
func AsString(v interface{}) string {
	switch cv := v.(type) {
	case string:
		return cv
	default:
		return fmt.Sprintf("%v", v)
	}
}

// AsStringArray tries to typeswitch an interface to a string or a string array
func AsStringArray(v interface{}) ([]string, bool) {
	switch cv := v.(type) {
	case string:
		return []string{cv}, true
	case []string:
		return cv, true
	case []interface{}:
		out := make([]string, len(cv))
		for i, iv := range cv {
			out[i] = AsString(iv)
		}
		return out, true
	default:
		log.Printf("tried to call AsStringArray on %v of type %T", cv, cv)
		return nil, false
	}
}

// AsTime tries to typeswitch an interface to a time.Time.
func AsTime(v interface{}) (time.Time, error) {
	switch cv := v.(type) {
	case time.Time:
		return cv, nil
	case string:
		return time.Parse(time.RFC3339, cv)
	case int64:
		return time.Unix(cv, 0), nil
	case int:
		return time.Unix(int64(cv), 0), nil
	default:
		return time.Parse(time.RFC3339, AsString(cv))
	}
}
