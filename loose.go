package smsapibg

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// The API documents numbers as JSON numbers, but a response that changed
// one to a string must not turn a message that was sent into an error (the
// caller might then send it again). These types accept both.

// looseFloat decodes a JSON number or a string holding one.
type looseFloat float64

func (f *looseFloat) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseFloat(string(unquote(b)), 64)
	if err != nil {
		return err
	}
	*f = looseFloat(v)
	return nil
}

// looseInt decodes a JSON integer or a string holding one.
type looseInt int64

func (n *looseInt) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseInt(string(unquote(b)), 10, 64)
	if err != nil {
		return err
	}
	*n = looseInt(v)
	return nil
}

// looseString decodes a JSON string or a bare number (an ID sent unquoted).
type looseString string

func (s *looseString) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = looseString(v)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*s = looseString(n.String())
	return nil
}

// unquote strips the quotes of a JSON string and maps null to "0", so the
// numeric types read "1.5", 1.5 and null alike.
func unquote(b []byte) []byte {
	b = bytes.TrimSpace(b)
	if string(b) == "null" {
		return []byte("0")
	}
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		return b[1 : len(b)-1]
	}
	return b
}
