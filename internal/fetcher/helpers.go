package fetcher

import (
	"encoding/json"
)

// jsonMarshalImpl wraps encoding/json Marshal.
func jsonMarshalImpl(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// jsonUnmarshalImpl wraps encoding/json Unmarshal.
func jsonUnmarshalImpl(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
